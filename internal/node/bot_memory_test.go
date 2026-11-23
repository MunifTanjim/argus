package node

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/memory"
	"github.com/MunifTanjim/argus/internal/botgit"
)

type memRig struct {
	d     *Node
	s     *bot.Store
	a     bot.Bot
	th    string
	root  string
	today string
}

func newMemRig(t *testing.T, budget int64) *memRig {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	d, s, root := botNode(t)
	d.botCfg.ProfileDir = filepath.Join(root, "profile")
	d.botCfg.Defaults.Memory.CoreBudget = budget
	now := time.Date(2026, 11, 22, 9, 30, 0, 0, time.UTC)
	d.botNow = func() time.Time { return now }
	info, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.BotByID(context.Background(), info.ID)
	if err := s.InsertThread(context.Background(), bot.Thread{ID: "th_1", BotID: a.ID, Kind: "chat", StartedBy: "you", Title: "flat search"}); err != nil {
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
	def, err := bot.LoadDefinition(r.a.Dir)
	if err != nil {
		t.Fatal(err)
	}
	def.Memory.Mode = mode
	if err := def.Save(r.a.Dir); err != nil {
		t.Fatal(err)
	}
}

func (r *memRig) log(t *testing.T, limit int) []botgit.Commit {
	t.Helper()
	repo, err := botgit.Open(context.Background(), r.a.Dir, botgit.ModeNested)
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
	n, err := r.d.botApplyMemory(context.Background(), r.a, r.th, "capture", ops, "")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApplyAutoCommitsAndEvents(t *testing.T) {
	r := newMemRig(t, 0)
	ctx := context.Background()
	n, err := r.d.botApplyMemory(ctx, r.a, r.th, "capture", []memory.Op{
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
		c.Trailers["Argus-Thread"] != r.th || c.Trailers["Argus-Bot"] != "scout" || c.Trailers["Argus-Bot-Id"] != r.a.ID {
		t.Fatalf("commit = %+v", c)
	}
	evs := r.events(t, evMemoryChanged)
	if len(evs) != 1 || evs[0]["commit"] != c.Hash || evs[0]["source"] != "capture" ||
		evs[0]["summary"] != "saved 3 facts, logged work" || len(evs[0]["ops"].([]any)) != 3 {
		t.Fatalf("events = %+v", evs)
	}

	// Without a thread: plain subject, no thread trailer, and the event goes
	// to the most recently active chat thread.
	if _, err := r.d.botApplyMemory(ctx, r.a, "", "bot", []memory.Op{{Action: "remove", Match: "tea"}}, ""); err != nil {
		t.Fatal(err)
	}
	c = r.log(t, 1)[0]
	if c.Subject != "memory: bot" || c.Trailers["Argus-Thread"] != "" {
		t.Fatalf("threadless commit = %+v", c)
	}
	if evs := r.events(t, evMemoryChanged); len(evs) != 2 || evs[1]["commit"] != c.Hash {
		t.Fatalf("threadless events = %+v", evs)
	}

	// A long title is cut to 50 characters.
	long := strings.Repeat("x", 70)
	if err := r.s.InsertThread(ctx, bot.Thread{ID: "th_2", BotID: r.a.ID, Kind: "chat", StartedBy: "you", Title: long}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.botApplyMemory(ctx, r.a, "th_2", "capture", []memory.Op{{Action: "add", Text: "x"}}, ""); err != nil {
		t.Fatal(err)
	}
	if c = r.log(t, 1)[0]; c.Subject != `memory: capture from "`+long[:50]+`"` {
		t.Fatalf("long title subject = %q", c.Subject)
	}
}

func TestApplyModeOff(t *testing.T) {
	r := newMemRig(t, 0)
	r.setMode(t, "off")
	_, err := r.d.botApplyMemory(context.Background(), r.a, r.th, "bot", []memory.Op{{Action: "add", Text: "x"}}, "")
	if err == nil || !strings.Contains(err.Error(), "memory is off for this bot") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyReviewStagesProposal(t *testing.T) {
	r := newMemRig(t, 0)
	ctx := context.Background()
	r.setMode(t, "review")
	n, err := r.d.botApplyMemory(ctx, r.a, r.th, "capture", []memory.Op{{Action: "add", Text: "likes tea"}}, "searched flats")
	if err != nil || n != 0 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != "" {
		t.Fatalf("MEMORY.md written in review mode: %q", got)
	}
	if got := r.read(t, "memory/log/2026-11.md"); got != "" {
		t.Fatalf("work log written in review mode: %q", got)
	}
	props, err := callBot[api.BotMemoryProposalsResult](t, r.d.handleBotMemoryProposals, api.BotRef{BotID: r.a.ID})
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

	res, err := callBot[api.BotMemoryDecideResult](t, r.d.handleBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: pr.ID, Accept: true})
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
	if _, err := callBot[api.BotMemoryDecideResult](t, r.d.handleBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: pr.ID, Accept: true}); rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("second decide err = %v", err)
	}

	// Rejecting leaves the files alone.
	r.apply(t, memory.Op{Action: "add", Text: "dislikes rain"})
	props, _ = callBot[api.BotMemoryProposalsResult](t, r.d.handleBotMemoryProposals, api.BotRef{BotID: r.a.ID})
	if _, err := callBot[api.BotMemoryDecideResult](t, r.d.handleBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: props.Proposals[0].ID}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.read(t, "memory/MEMORY.md"), "rain") {
		t.Fatal("rejected proposal applied")
	}
	if props, _ = callBot[api.BotMemoryProposalsResult](t, r.d.handleBotMemoryProposals, api.BotRef{BotID: r.a.ID}); len(props.Proposals) != 0 {
		t.Fatalf("pending after reject = %+v", props)
	}
}

func TestApplyProfileAlwaysReview(t *testing.T) {
	r := newMemRig(t, 0)
	n := r.apply(t, memory.Op{Action: "add", File: "PROFILE.md", Text: "lives in Dhaka"}, memory.Op{Action: "add", Text: "likes tea"})
	if n != 1 {
		t.Fatalf("applied = %d", n)
	}
	props, _ := callBot[api.BotMemoryProposalsResult](t, r.d.handleBotMemoryProposals, api.BotRef{BotID: r.a.ID})
	if len(props.Proposals) != 1 || props.Proposals[0].Ops[0].File != "PROFILE.md" || props.Proposals[0].Log != "" {
		t.Fatalf("proposals = %+v", props)
	}
	res, err := callBot[api.BotMemoryDecideResult](t, r.d.handleBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: props.Proposals[0].ID, Accept: true})
	if err != nil || res.ProfileCommit == "" || res.Commit != "" || res.Applied != 1 {
		t.Fatalf("decide = %+v, %v", res, err)
	}
	data, _ := os.ReadFile(filepath.Join(r.d.botCfg.ProfileDir, "PROFILE.md"))
	if string(data) != "- lives in Dhaka (2026-11-22)\n" {
		t.Fatalf("PROFILE.md = %q", data)
	}
	log, err := callBot[api.BotMemoryLogResult](t, r.d.handleBotMemoryLog, api.BotMemoryLogParams{BotID: r.a.ID, Profile: true})
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
	_, err := r.d.botApplyMemory(ctx, r.a, r.th, "bot", []memory.Op{{Action: "add", Text: "token ghp_abcdefghijklmnopqrstuvwxyz0123456789"}}, "")
	if err == nil || !strings.Contains(err.Error(), "looks like a secret") {
		t.Fatalf("all-secret err = %v", err)
	}
	if len(r.log(t, 0)) != 2 {
		t.Fatal("blocked write committed")
	}

	// Until and Match are scanned too, and never echoed unmasked.
	for _, op := range []memory.Op{
		{Action: "add", Text: "x", Until: "AKIAABCDEFGHIJKLMNOP"},
		{Action: "remove", Match: "sk-abcdefghijklmnopqrstuvwx"},
	} {
		if _, err := r.d.botApplyMemory(ctx, r.a, r.th, "bot", []memory.Op{op}, ""); err == nil || !strings.Contains(err.Error(), "looks like a secret") {
			t.Fatalf("op %+v err = %v", op, err)
		}
	}
	evs = r.events(t, evMemoryBlocked)
	if len(evs) != 4 || evs[2]["field"] != "until" || evs[3]["field"] != "match" {
		t.Fatalf("blocked events = %+v", evs)
	}
	raw, _ := r.s.Events(ctx, r.th, 0)
	for _, ev := range raw {
		if strings.Contains(string(ev.Payload), "AKIA") || strings.Contains(string(ev.Payload), "sk-abc") {
			t.Fatalf("event leaks a secret: %s", ev.Payload)
		}
	}

	// A malformed Until or a newline in any field is rejected; nothing is written.
	before := r.read(t, "memory/MEMORY.md")
	for _, op := range []memory.Op{
		{Action: "add", Text: "x", Until: "next week"},
		{Action: "add", Text: "x", Until: "2026-12-01\n- injected"},
		{Action: "remove", Match: "likes\ntea"},
		{Action: "add", Text: "y\n- injected"},
	} {
		_, err := r.d.botApplyMemory(ctx, r.a, r.th, "bot", []memory.Op{op}, "")
		if err == nil || strings.Contains(err.Error(), "injected") {
			t.Fatalf("op %+v err = %v", op, err)
		}
	}
	if got := r.read(t, "memory/MEMORY.md"); got != before {
		t.Fatalf("MEMORY.md changed: %q", got)
	}
	if len(r.log(t, 0)) != 2 {
		t.Fatal("invalid op committed")
	}
}

func TestDecideRefusedWhenOff(t *testing.T) {
	r := newMemRig(t, 0)
	r.setMode(t, "review")
	r.apply(t, memory.Op{Action: "add", Text: "likes tea"})
	props, _ := callBot[api.BotMemoryProposalsResult](t, r.d.handleBotMemoryProposals, api.BotRef{BotID: r.a.ID})
	r.setMode(t, "off")
	_, err := callBot[api.BotMemoryDecideResult](t, r.d.handleBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: props.Proposals[0].ID, Accept: true})
	if rpcCode(err) != api.CodeInvalidRequest || !strings.Contains(err.Error(), "memory is off for this bot") {
		t.Fatalf("decide err = %v", err)
	}
	if pending, _ := r.s.PendingProposals(context.Background(), r.a.ID); len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != "" {
		t.Fatalf("MEMORY.md = %q", got)
	}
}

func TestThreadlessEventsWithoutChatOnlyLog(t *testing.T) {
	r := newMemRig(t, 0)
	ctx := context.Background()
	if err := r.s.CloseThread(ctx, r.th); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.botApplyMemory(ctx, r.a, "", "tidy", []memory.Op{{Action: "add", Text: "x"}}, ""); err != nil {
		t.Fatal(err)
	}
	if evs := r.events(t, evMemoryChanged); len(evs) != 0 {
		t.Fatalf("event recorded without an active chat: %+v", evs)
	}
}

func TestDecideRevalidates(t *testing.T) {
	r := newMemRig(t, 0)
	r.apply(t, memory.Op{Action: "add", Text: "old fact"}, memory.Op{Action: "add", Text: "keep me"})
	r.setMode(t, "review")
	r.apply(t, memory.Op{Action: "update", Match: "old fact", Text: "new fact"}, memory.Op{Action: "add", Text: "another"})
	r.apply(t, memory.Op{Action: "remove", Match: "keep"})
	props, _ := callBot[api.BotMemoryProposalsResult](t, r.d.handleBotMemoryProposals, api.BotRef{BotID: r.a.ID})
	if len(props.Proposals) != 2 {
		t.Fatalf("proposals = %+v", props)
	}

	// The user edits the file by hand before accepting.
	r.write(t, "memory/MEMORY.md", "- other wording (2026-11-20)\n")

	res, err := callBot[api.BotMemoryDecideResult](t, r.d.handleBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: props.Proposals[0].ID, Accept: true})
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

	_, err = callBot[api.BotMemoryDecideResult](t, r.d.handleBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: props.Proposals[1].ID, Accept: true})
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

	_, err := callBot[api.BotMemoryUndoResult](t, r.d.handleBotMemoryUndo, api.BotMemoryUndoParams{BotID: r.a.ID, Commit: first[:10]})
	if rpcCode(err) != api.CodeInvalidRequest || !strings.Contains(err.Error(), "can't undo: later changes touched the same lines") {
		t.Fatalf("undo err = %v", err)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != before {
		t.Fatalf("files changed by failed undo: %q", got)
	}

	r.apply(t, memory.Op{Action: "add", Text: "fact two"})
	last := r.log(t, 1)[0].Hash
	res, err := callBot[api.BotMemoryUndoResult](t, r.d.handleBotMemoryUndo, api.BotMemoryUndoParams{BotID: r.a.ID, Commit: last})
	if err != nil || res.Commit == "" {
		t.Fatalf("undo = %+v, %v", res, err)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != before {
		t.Fatalf("MEMORY.md after undo = %q", got)
	}
	if _, err := callBot[api.BotMemoryUndoResult](t, r.d.handleBotMemoryUndo, api.BotMemoryUndoParams{BotID: r.a.ID, Commit: "deadbeef"}); rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("unknown commit err = %v", err)
	}
}

func TestUndoFromThreadRecordsEvent(t *testing.T) {
	r := newMemRig(t, 0)
	r.apply(t, memory.Op{Action: "add", Text: "fact one"})
	c := r.log(t, 1)[0].Hash
	res, err := callBot[api.BotMemoryUndoResult](t, r.d.handleBotMemoryUndo,
		api.BotMemoryUndoParams{BotID: r.a.ID, Commit: c[:10], ThreadID: r.th})
	if err != nil {
		t.Fatal(err)
	}
	evs := r.events(t, evMemoryUndone)
	if len(evs) != 1 || evs[0]["commit"] != c || evs[0]["revert"] != res.Commit {
		t.Fatalf("memory.undone events = %v", evs)
	}
}

func TestUndoKeepsUserProvenance(t *testing.T) {
	r := newMemRig(t, 0)
	ctx := context.Background()
	r.write(t, "memory/MEMORY.md", "- hand written (2026-11-01)\n")
	r.apply(t, memory.Op{Action: "remove", Match: "hand written"})
	removal := r.log(t, 1)[0].Hash
	if got := r.read(t, "memory/MEMORY.md"); strings.Contains(got, "hand written") {
		t.Fatalf("not removed: %q", got)
	}
	res, err := callBot[api.BotMemoryUndoResult](t, r.d.handleBotMemoryUndo, api.BotMemoryUndoParams{BotID: r.a.ID, Commit: removal})
	if err != nil {
		t.Fatal(err)
	}
	c := r.log(t, 1)[0]
	if c.Hash != res.Commit || c.Trailers["Argus-Source"] != "you" || c.Trailers["Argus-Bot"] != "scout" || c.Trailers["Argus-Bot-Id"] != r.a.ID {
		t.Fatalf("undo commit = %+v", c)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != "- hand written (2026-11-01)\n" {
		t.Fatalf("MEMORY.md after undo = %q", got)
	}
	repo, _ := botgit.Open(ctx, r.a.Dir, botgit.ModeNested)
	sources, err := repo.BlameSources(ctx, "memory/MEMORY.md")
	if err != nil || sources[1] != "you" {
		t.Fatalf("blame sources = %v, %v", sources, err)
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
	repo, _ := botgit.Open(context.Background(), r.a.Dir, botgit.ModeNested)
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
	repo, _ := botgit.Open(context.Background(), r.a.Dir, botgit.ModeNested)
	added, _ := repo.AddedLines(context.Background(), r.log(t, 1)[0].Hash)
	if !strings.Contains(strings.Join(added, "\n"), "oldest") {
		t.Fatalf("archive not in commit: %q", added)
	}
}

func TestMemoryShowAndLog(t *testing.T) {
	r := newMemRig(t, 1000)
	r.apply(t, memory.Op{Action: "add", Text: "likes tea"}, memory.Op{Action: "add", File: "topics/flat.md", Text: "x"})
	show, err := callBot[api.BotMemoryShowResult](t, r.d.handleBotMemoryShow, api.BotRef{BotID: r.a.ID})
	if err != nil || show.Memory != "- likes tea (2026-11-22)\n" || show.Usage != int64(len(show.Memory)) || show.Budget != 1000 ||
		len(show.Topics) != 1 || show.Topics[0] != "flat.md" || show.Profile != "" {
		t.Fatalf("show = %+v, %v", show, err)
	}
	log, err := callBot[api.BotMemoryLogResult](t, r.d.handleBotMemoryLog, api.BotMemoryLogParams{BotID: r.a.ID, Limit: 1})
	if err != nil || len(log.Commits) != 1 || log.Commits[0].Source != "capture" || log.Commits[0].ThreadID != r.th || log.Commits[0].When.IsZero() {
		t.Fatalf("log = %+v, %v", log, err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}`).MatchString(log.Commits[0].Hash) {
		t.Fatalf("hash = %q", log.Commits[0].Hash)
	}
}

func TestApplyRejectsUnknownFile(t *testing.T) {
	r := newMemRig(t, 0)
	_, err := r.d.botApplyMemory(context.Background(), r.a, r.th, "bot", []memory.Op{{Action: "add", File: "../bot.yaml", Text: "x"}}, "")
	if err == nil || !strings.Contains(err.Error(), "unknown memory file") {
		t.Fatalf("err = %v", err)
	}
	if len(r.events(t, evMemoryError)) != 1 {
		t.Fatal("no error event")
	}
}

func TestProfileIncluded(t *testing.T) {
	r := newMemRig(t, 24<<10)
	if err := os.MkdirAll(r.d.botCfg.ProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.d.botCfg.ProfileDir, "PROFILE.md"), []byte("Lives in Lisbon."), 0o644); err != nil {
		t.Fatal(err)
	}
	def, _ := bot.LoadDefinition(r.a.Dir)
	set := r.d.botSettings(r.a)
	in, err := bot.GatherInput(r.a.Dir, def, set, r.d.botCfg.ProfileDir, set.LogEntries, set.CoreBudget, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bot.RenderInstructions(in), "Lives in Lisbon.") {
		t.Fatal("profile missing from instructions")
	}
}

func TestImportGoesToReview(t *testing.T) {
	r := newMemRig(t, 24<<10)
	ex := &stubExtractor{reply: `{"ops":[{"action":"add","file":"MEMORY.md","text":"Prefers dark mode"}],"log":""}`}
	r.d.extractorFor = func(bot.Settings) memory.Extractor { return ex }
	before := r.read(t, "memory/MEMORY.md")

	res, err := callBot[api.BotMemoryImportResult](t, r.d.handleBotMemoryImport, api.BotMemoryImportParams{BotID: r.a.ID, Content: "- likes dark mode"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Staged != 1 {
		t.Fatalf("staged = %d", res.Staged)
	}
	if len(ex.prompts) != 1 || !strings.Contains(ex.prompts[0], "extract durable facts from this exported memory") || !strings.Contains(ex.prompts[0], "likes dark mode") {
		t.Fatalf("prompt = %v", ex.prompts)
	}
	if got := r.read(t, "memory/MEMORY.md"); got != before {
		t.Fatalf("memory applied directly: %q", got)
	}
	props, err := callBot[api.BotMemoryProposalsResult](t, r.d.handleBotMemoryProposals, api.BotRef{BotID: r.a.ID})
	if err != nil || len(props.Proposals) != 1 || props.Proposals[0].Ops[0].Text != "Prefers dark mode" {
		t.Fatalf("proposals = %+v, %v", props, err)
	}

	r.setMode(t, "off")
	if _, err := callBot[api.BotMemoryImportResult](t, r.d.handleBotMemoryImport, api.BotMemoryImportParams{BotID: r.a.ID, Content: "x"}); err == nil {
		t.Fatal("import with memory off succeeded")
	}
}

func TestOuterUndoRefusesCommitBeyondBot(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	d, s, root := botNode(t)
	d.botCfg.Defaults.Git.Mode = botgit.ModeOuter
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root, "-c", "user.name=u", "-c", "user.email=u@x"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--quiet")
	info, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.BotByID(context.Background(), info.ID)
	if _, err := d.botApplyMemory(context.Background(), a, "", "capture", []memory.Op{{Action: "add", File: "MEMORY.md", Text: "fact one"}}, ""); err != nil {
		t.Fatal(err)
	}

	// The user commits code and memory together.
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mem := filepath.Join(a.Dir, "memory", "MEMORY.md")
	data, _ := os.ReadFile(mem)
	if err := os.WriteFile(mem, append(data, "- hand fact\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "wip")
	mixed := git("rev-parse", "HEAD")

	_, err = callBot[api.BotMemoryUndoResult](t, d.handleBotMemoryUndo, api.BotMemoryUndoParams{BotID: a.ID, Commit: mixed})
	if err == nil || !strings.Contains(err.Error(), "app.go") {
		t.Fatalf("mixed undo: %v", err)
	}
	if git("rev-parse", "HEAD") != mixed {
		t.Fatal("mixed undo committed")
	}
	if _, err := os.Stat(filepath.Join(root, "app.go")); err != nil {
		t.Fatalf("user file touched: %v", err)
	}
	// A commit within the bot directory still undoes.
	if _, err := d.botApplyMemory(context.Background(), a, "", "capture", []memory.Op{{Action: "add", File: "MEMORY.md", Text: "fact two"}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := callBot[api.BotMemoryUndoResult](t, d.handleBotMemoryUndo, api.BotMemoryUndoParams{BotID: a.ID, Commit: git("rev-parse", "HEAD")}); err != nil {
		t.Fatalf("bot undo: %v", err)
	}
}

func TestOuterFailedMemoryCommitLeavesIndexClean(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	d, s, root := botNode(t)
	d.botCfg.Defaults.Git.Mode = botgit.ModeOuter
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root, "-c", "user.name=u", "-c", "user.email=u@x"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--quiet")
	info, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.BotByID(context.Background(), info.ID)
	if _, err := d.botApplyMemory(context.Background(), a, "", "capture", []memory.Op{{Action: "add", File: "MEMORY.md", Text: "fact one"}}, ""); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(git("rev-parse", "--absolute-git-dir"), "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := d.botApplyMemory(context.Background(), a, "", "capture", []memory.Op{{Action: "add", File: "MEMORY.md", Text: "fact two"}}, "logged"); err == nil {
		t.Fatal("memory commit succeeded despite failing hook")
	}
	if got := git("diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("index after failed commit = %q, want clean", got)
	}
	if got := git("status", "--porcelain"); got != "" {
		t.Fatalf("worktree after failed commit = %q, want clean", got)
	}
}
