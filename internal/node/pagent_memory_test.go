package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/agentgit"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/memory"
)

type memRig struct {
	d     *Node
	s     *pagent.Store
	a     pagent.Agent
	th    string
	root  string
	today string
}

func newMemRig(t *testing.T, budget int64) *memRig {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	d, s, root := pagentNode(t)
	d.pagentCfg.ProfileDir = filepath.Join(root, "profile")
	d.pagentCfg.Defaults.Memory.CoreBudget = budget
	now := time.Date(2026, 11, 22, 9, 30, 0, 0, time.UTC)
	d.pagentNow = func() time.Time { return now }
	info, err := callPAgent[api.PAgentInfo](t, d.handlePAgentCreate, api.PAgentCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.AgentByID(context.Background(), info.ID)
	if err := s.InsertThread(context.Background(), pagent.Thread{ID: "th_1", AgentID: a.ID, Kind: "chat", StartedBy: "you", Title: "flat search"}); err != nil {
		t.Fatal(err)
	}
	return &memRig{d: d, s: s, a: a, th: "th_1", root: root, today: "2026-11-22"}
}

func (r *memRig) read(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.a.Dir, rel))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func (r *memRig) write(t *testing.T, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.a.Dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *memRig) setMode(t *testing.T, mode string) {
	t.Helper()
	def, err := pagent.LoadDefinition(r.a.Dir)
	if err != nil {
		t.Fatal(err)
	}
	def.Memory.Mode = mode
	if err := def.Save(r.a.Dir); err != nil {
		t.Fatal(err)
	}
}

func (r *memRig) log(t *testing.T, limit int) []agentgit.Commit {
	t.Helper()
	repo, err := agentgit.Open(context.Background(), r.a.Dir, agentgit.ModeNested)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := repo.Log(context.Background(), []string{"memory"}, limit)
	if err != nil {
		t.Fatal(err)
	}
	return commits
}

func (r *memRig) events(t *testing.T, kind string) []map[string]any {
	t.Helper()
	evs, err := r.s.Events(context.Background(), r.th, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, ev := range evs {
		if ev.Kind != kind {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(ev.Payload, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func (r *memRig) apply(t *testing.T, ops ...memory.Op) int {
	t.Helper()
	n, err := r.d.pagentApplyMemory(context.Background(), r.a, r.th, "capture", ops, "")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApplyAutoCommitsAndEvents(t *testing.T) {
	r := newMemRig(t, 0)
	ctx := context.Background()
	n, err := r.d.pagentApplyMemory(ctx, r.a, r.th, "capture", []memory.Op{
		{Action: "add", Text: "likes tea"},
		{Action: "add", File: "USER.md", Text: "answer briefly"},
		{Action: "add", File: "topics/flat.md", Text: "budget 1200"},
	}, "searched flats")
	if err != nil || n != 3 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != "- likes tea (2026-11-22)\n" {
		t.Fatalf("MEMORY.md = %q", got)
	}
	if got := r.read(t, "memory/USER.md"); got != "- answer briefly (2026-11-22)\n" {
		t.Fatalf("USER.md = %q", got)
	}
	if got := r.read(t, "memory/topics/flat.md"); !strings.Contains(got, "budget 1200") {
		t.Fatalf("topic = %q", got)
	}
	if got := r.read(t, "memory/log/2026-11.md"); got != "- 2026-11-22 09:30 searched flats\n" {
		t.Fatalf("work log = %q", got)
	}

	c := r.log(t, 1)[0]
	if c.Subject != `memory: capture from "flat search"` || c.Trailers["Argus-Source"] != "capture" ||
		c.Trailers["Argus-Thread"] != r.th || c.Trailers["Argus-Agent"] != "scout" || c.Trailers["Argus-Agent-Id"] != r.a.ID {
		t.Fatalf("commit = %+v", c)
	}
	evs := r.events(t, evMemoryChanged)
	if len(evs) != 1 || evs[0]["commit"] != c.Hash || evs[0]["source"] != "capture" ||
		evs[0]["summary"] != "saved 3 facts, logged work" || len(evs[0]["ops"].([]any)) != 3 {
		t.Fatalf("events = %+v", evs)
	}

	// Without a thread: plain subject, no thread trailer, no events.
	if _, err := r.d.pagentApplyMemory(ctx, r.a, "", "agent", []memory.Op{{Action: "remove", Match: "tea"}}, ""); err != nil {
		t.Fatal(err)
	}
	c = r.log(t, 1)[0]
	if c.Subject != "memory: agent" || c.Trailers["Argus-Thread"] != "" {
		t.Fatalf("threadless commit = %+v", c)
	}
	if len(r.events(t, evMemoryChanged)) != 1 {
		t.Fatal("threadless apply recorded an event")
	}

	// A long title is cut to 50 characters.
	long := strings.Repeat("x", 70)
	if err := r.s.InsertThread(ctx, pagent.Thread{ID: "th_2", AgentID: r.a.ID, Kind: "chat", StartedBy: "you", Title: long}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.pagentApplyMemory(ctx, r.a, "th_2", "capture", []memory.Op{{Action: "add", Text: "x"}}, ""); err != nil {
		t.Fatal(err)
	}
	if c = r.log(t, 1)[0]; c.Subject != `memory: capture from "`+long[:50]+`"` {
		t.Fatalf("long title subject = %q", c.Subject)
	}
}

func TestApplyModeOff(t *testing.T) {
	r := newMemRig(t, 0)
	r.setMode(t, "off")
	_, err := r.d.pagentApplyMemory(context.Background(), r.a, r.th, "agent", []memory.Op{{Action: "add", Text: "x"}}, "")
	if err == nil || !strings.Contains(err.Error(), "memory is off for this agent") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyReviewStagesProposal(t *testing.T) {
	r := newMemRig(t, 0)
	ctx := context.Background()
	r.setMode(t, "review")
	n, err := r.d.pagentApplyMemory(ctx, r.a, r.th, "capture", []memory.Op{{Action: "add", Text: "likes tea"}}, "searched flats")
	if err != nil || n != 0 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != "" {
		t.Fatalf("MEMORY.md written in review mode: %q", got)
	}
	if got := r.read(t, "memory/log/2026-11.md"); got != "" {
		t.Fatalf("work log written in review mode: %q", got)
	}
	props, err := callPAgent[api.PAgentMemoryProposalsResult](t, r.d.handlePAgentMemoryProposals, api.PAgentRef{AgentID: r.a.ID})
	if err != nil || len(props.Proposals) != 1 {
		t.Fatalf("proposals = %+v, %v", props, err)
	}
	pr := props.Proposals[0]
	if pr.Source != "capture" || pr.ThreadID != r.th || pr.Log != "searched flats" || len(pr.Ops) != 1 || pr.Ops[0].Text != "likes tea" || pr.Ops[0].File != "MEMORY.md" {
		t.Fatalf("proposal = %+v", pr)
	}
	evs := r.events(t, evMemoryProposed)
	if len(evs) != 1 || evs[0]["proposal_id"] != pr.ID {
		t.Fatalf("proposed events = %+v", evs)
	}

	res, err := callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: pr.ID, Accept: true})
	if err != nil || res.Applied != 1 || res.Commit == "" {
		t.Fatalf("decide = %+v, %v", res, err)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != "- likes tea (2026-11-22)\n" {
		t.Fatalf("MEMORY.md after accept = %q", got)
	}
	if got := r.read(t, "memory/log/2026-11.md"); !strings.Contains(got, "searched flats") {
		t.Fatalf("work log after accept = %q", got)
	}
	if c := r.log(t, 1)[0]; c.Hash != res.Commit || c.Trailers["Argus-Source"] != "capture" || c.Subject != `memory: capture from "flat search"` {
		t.Fatalf("accept commit = %+v", c)
	}
	if _, err := callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: pr.ID, Accept: true}); rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("second decide err = %v", err)
	}

	// Rejecting leaves the files alone.
	r.apply(t, memory.Op{Action: "add", Text: "dislikes rain"})
	props, _ = callPAgent[api.PAgentMemoryProposalsResult](t, r.d.handlePAgentMemoryProposals, api.PAgentRef{AgentID: r.a.ID})
	if _, err := callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: props.Proposals[0].ID}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.read(t, "memory/MEMORY.md"), "rain") {
		t.Fatal("rejected proposal applied")
	}
	if props, _ = callPAgent[api.PAgentMemoryProposalsResult](t, r.d.handlePAgentMemoryProposals, api.PAgentRef{AgentID: r.a.ID}); len(props.Proposals) != 0 {
		t.Fatalf("pending after reject = %+v", props)
	}
}

func TestApplyProfileAlwaysReview(t *testing.T) {
	r := newMemRig(t, 0)
	n := r.apply(t, memory.Op{Action: "add", File: "PROFILE.md", Text: "lives in Dhaka"}, memory.Op{Action: "add", Text: "likes tea"})
	if n != 1 {
		t.Fatalf("applied = %d", n)
	}
	props, _ := callPAgent[api.PAgentMemoryProposalsResult](t, r.d.handlePAgentMemoryProposals, api.PAgentRef{AgentID: r.a.ID})
	if len(props.Proposals) != 1 || props.Proposals[0].Ops[0].File != "PROFILE.md" || props.Proposals[0].Log != "" {
		t.Fatalf("proposals = %+v", props)
	}
	res, err := callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: props.Proposals[0].ID, Accept: true})
	if err != nil || res.ProfileCommit == "" || res.Commit != "" || res.Applied != 1 {
		t.Fatalf("decide = %+v, %v", res, err)
	}
	data, _ := os.ReadFile(filepath.Join(r.d.pagentCfg.ProfileDir, "PROFILE.md"))
	if string(data) != "- lives in Dhaka (2026-11-22)\n" {
		t.Fatalf("PROFILE.md = %q", data)
	}
	log, err := callPAgent[api.PAgentMemoryLogResult](t, r.d.handlePAgentMemoryLog, api.PAgentMemoryLogParams{AgentID: r.a.ID, Profile: true})
	if err != nil || len(log.Commits) != 2 || log.Commits[0].Hash != res.ProfileCommit || log.Commits[0].Source != "capture" {
		t.Fatalf("profile log = %+v, %v", log, err)
	}
}

func TestApplySecretBlocked(t *testing.T) {
	r := newMemRig(t, 0)
	ctx := context.Background()
	n := r.apply(t, memory.Op{Action: "add", Text: "aws key AKIAABCDEFGHIJKLMNOP"}, memory.Op{Action: "add", Text: "likes tea"})
	if n != 1 {
		t.Fatalf("applied = %d", n)
	}
	if got := r.read(t, "memory/MEMORY.md"); strings.Contains(got, "AKIA") || !strings.Contains(got, "likes tea") {
		t.Fatalf("MEMORY.md = %q", got)
	}
	evs := r.events(t, evMemoryBlocked)
	if len(evs) != 1 || evs[0]["rule"] != "aws-access-key" || strings.Contains(evs[0]["text"].(string), "AKIA") ||
		!strings.Contains(evs[0]["text"].(string), "«aws-access-key»") {
		t.Fatalf("blocked events = %+v", evs)
	}
	_, err := r.d.pagentApplyMemory(ctx, r.a, r.th, "agent", []memory.Op{{Action: "add", Text: "token ghp_abcdefghijklmnopqrstuvwxyz0123456789"}}, "")
	if err == nil || !strings.Contains(err.Error(), "looks like a secret") {
		t.Fatalf("all-secret err = %v", err)
	}
	if len(r.log(t, 0)) != 2 {
		t.Fatal("blocked write committed")
	}
}

func TestDecideRevalidates(t *testing.T) {
	r := newMemRig(t, 0)
	r.apply(t, memory.Op{Action: "add", Text: "old fact"}, memory.Op{Action: "add", Text: "keep me"})
	r.setMode(t, "review")
	r.apply(t, memory.Op{Action: "update", Match: "old fact", Text: "new fact"}, memory.Op{Action: "add", Text: "another"})
	r.apply(t, memory.Op{Action: "remove", Match: "keep"})
	props, _ := callPAgent[api.PAgentMemoryProposalsResult](t, r.d.handlePAgentMemoryProposals, api.PAgentRef{AgentID: r.a.ID})
	if len(props.Proposals) != 2 {
		t.Fatalf("proposals = %+v", props)
	}

	// The user edits the file by hand before accepting.
	r.write(t, "memory/MEMORY.md", "- other wording (2026-11-20)\n")

	res, err := callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: props.Proposals[0].ID, Accept: true})
	if err != nil || res.Applied != 1 || len(res.Rejected) != 1 || !strings.Contains(res.Rejected[0], `no entry matches "old fact"`) {
		t.Fatalf("decide = %+v, %v", res, err)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != "- other wording (2026-11-20)\n- another (2026-11-22)\n" {
		t.Fatalf("MEMORY.md = %q", got)
	}
	commits := r.log(t, 2)
	if commits[1].Subject != "manual edit" || commits[1].Trailers["Argus-Source"] != "you" {
		t.Fatalf("hand edit not committed first: %+v", commits)
	}

	_, err = callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: props.Proposals[1].ID, Accept: true})
	if rpcCode(err) != api.CodeInvalidRequest || !strings.Contains(err.Error(), "can't apply") {
		t.Fatalf("all-stale decide err = %v", err)
	}
	if pending, _ := r.s.PendingProposals(context.Background(), r.a.ID); len(pending) != 1 {
		t.Fatalf("stale proposal no longer pending: %+v", pending)
	}
}

func TestUndoConflict(t *testing.T) {
	r := newMemRig(t, 0)
	r.apply(t, memory.Op{Action: "add", Text: "fact one"})
	first := r.log(t, 1)[0].Hash
	r.apply(t, memory.Op{Action: "update", Match: "fact one", Text: "fact one revised"})
	before := r.read(t, "memory/MEMORY.md")

	_, err := callPAgent[api.PAgentMemoryUndoResult](t, r.d.handlePAgentMemoryUndo, api.PAgentMemoryUndoParams{AgentID: r.a.ID, Commit: first[:10]})
	if rpcCode(err) != api.CodeInvalidRequest || !strings.Contains(err.Error(), "can't undo: later changes touched the same lines") {
		t.Fatalf("undo err = %v", err)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != before {
		t.Fatalf("files changed by failed undo: %q", got)
	}

	r.apply(t, memory.Op{Action: "add", Text: "fact two"})
	last := r.log(t, 1)[0].Hash
	res, err := callPAgent[api.PAgentMemoryUndoResult](t, r.d.handlePAgentMemoryUndo, api.PAgentMemoryUndoParams{AgentID: r.a.ID, Commit: last})
	if err != nil || res.Commit == "" {
		t.Fatalf("undo = %+v, %v", res, err)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != before {
		t.Fatalf("MEMORY.md after undo = %q", got)
	}
	if _, err := callPAgent[api.PAgentMemoryUndoResult](t, r.d.handlePAgentMemoryUndo, api.PAgentMemoryUndoParams{AgentID: r.a.ID, Commit: "deadbeef"}); rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("unknown commit err = %v", err)
	}
}

func TestUserEditsCommittedFirst(t *testing.T) {
	r := newMemRig(t, 0)
	r.apply(t, memory.Op{Action: "add", Text: "a"})
	r.write(t, "memory/MEMORY.md", r.read(t, "memory/MEMORY.md")+"- hand note password: hunter2hunter2 (2026-11-01)\n")
	r.apply(t, memory.Op{Action: "add", Text: "b"})

	commits := r.log(t, 2)
	if commits[1].Subject != "manual edit" || commits[1].Trailers["Argus-Source"] != "you" || commits[0].Trailers["Argus-Source"] != "capture" {
		t.Fatalf("commits = %+v", commits)
	}
	repo, _ := agentgit.Open(context.Background(), r.a.Dir, agentgit.ModeNested)
	added, err := repo.AddedLines(context.Background(), commits[0].Hash)
	if err != nil || len(added) != 1 || added[0] != "- b (2026-11-22)" {
		t.Fatalf("argus commit added %q, %v", added, err)
	}
	if added, _ := repo.AddedLines(context.Background(), commits[1].Hash); len(added) != 1 || !strings.Contains(added[0], "hand note") {
		t.Fatalf("hand commit added %q", added)
	}
	warn := r.events(t, evMemoryError)
	if len(warn) != 1 || !strings.Contains(warn[0]["message"].(string), "looks like a secret") || warn[0]["commit"] != commits[1].Hash {
		t.Fatalf("hand edit warning = %+v", warn)
	}
}

func TestApplyOverBudgetArchivesOldest(t *testing.T) {
	r := newMemRig(t, 60)
	r.write(t, "memory/MEMORY.md", "- mid (2026-06-01)\n- oldest (2025-01-01)\n- old (2026-01-01)\n")
	r.apply(t, memory.Op{Action: "add", Text: "brand new fact"})
	mem := r.read(t, "memory/MEMORY.md")
	if len(mem) > 60 || !strings.Contains(mem, "brand new fact") || strings.Contains(mem, "oldest") {
		t.Fatalf("MEMORY.md = %q", mem)
	}
	arch := r.read(t, "memory/topics/archive.md")
	if !strings.HasPrefix(arch, "# Archive\n- oldest (2025-01-01)\n") || strings.Contains(arch, "brand new") {
		t.Fatalf("archive = %q", arch)
	}
	evs := r.events(t, evMemoryChanged)
	if !strings.Contains(evs[len(evs)-1]["summary"].(string), "archived") {
		t.Fatalf("summary = %v", evs[len(evs)-1]["summary"])
	}
	// The archive move is part of the Argus commit.
	repo, _ := agentgit.Open(context.Background(), r.a.Dir, agentgit.ModeNested)
	added, _ := repo.AddedLines(context.Background(), r.log(t, 1)[0].Hash)
	if !strings.Contains(strings.Join(added, "\n"), "oldest") {
		t.Fatalf("archive not in commit: %q", added)
	}
}

func TestMemoryShowAndLog(t *testing.T) {
	r := newMemRig(t, 1000)
	r.apply(t, memory.Op{Action: "add", Text: "likes tea"}, memory.Op{Action: "add", File: "topics/flat.md", Text: "x"})
	show, err := callPAgent[api.PAgentMemoryShowResult](t, r.d.handlePAgentMemoryShow, api.PAgentRef{AgentID: r.a.ID})
	if err != nil || show.Memory != "- likes tea (2026-11-22)\n" || show.Usage != int64(len(show.Memory)) || show.Budget != 1000 ||
		len(show.Topics) != 1 || show.Topics[0] != "flat.md" || show.Profile != "" {
		t.Fatalf("show = %+v, %v", show, err)
	}
	log, err := callPAgent[api.PAgentMemoryLogResult](t, r.d.handlePAgentMemoryLog, api.PAgentMemoryLogParams{AgentID: r.a.ID, Limit: 1})
	if err != nil || len(log.Commits) != 1 || log.Commits[0].Source != "capture" || log.Commits[0].ThreadID != r.th || log.Commits[0].When.IsZero() {
		t.Fatalf("log = %+v, %v", log, err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}`).MatchString(log.Commits[0].Hash) {
		t.Fatalf("hash = %q", log.Commits[0].Hash)
	}
}

func TestApplyRejectsUnknownFile(t *testing.T) {
	r := newMemRig(t, 0)
	_, err := r.d.pagentApplyMemory(context.Background(), r.a, r.th, "agent", []memory.Op{{Action: "add", File: "../agent.yaml", Text: "x"}}, "")
	if err == nil || !strings.Contains(err.Error(), "unknown memory file") {
		t.Fatalf("err = %v", err)
	}
	if len(r.events(t, evMemoryError)) != 1 {
		t.Fatal("no error event")
	}
}
