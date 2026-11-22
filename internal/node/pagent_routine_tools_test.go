package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/agentgit"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/routine"
	"github.com/MunifTanjim/argus/internal/session"
)

func (r *runRig) callerIn(threadID string) callerRef {
	return callerRef{Local: &session.Session{ID: "claude:c-" + threadID, PAgentID: r.a.ID, PAgentThreadID: threadID}}
}

// runThread inserts a run thread started by startedBy.
func (r *runRig) runThread(t *testing.T, id, startedBy string) string {
	t.Helper()
	if err := r.store.InsertThread(context.Background(), pagent.Thread{ID: id, AgentID: r.a.ID, Kind: threadRun, StartedBy: startedBy, Status: pagent.ThreadActive}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *runRig) lastCommit(t *testing.T, path string) agentgit.Commit {
	t.Helper()
	repo, err := agentgit.Open(context.Background(), r.a.Dir, agentgit.ModeNested)
	if err != nil {
		t.Fatal(err)
	}
	c, err := repo.Log(context.Background(), []string{path}, 1)
	if err != nil || len(c) != 1 {
		t.Fatalf("log %s: %+v %v", path, c, err)
	}
	return c[0]
}

func checkBacks(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "routines", "check-back-*.yaml"))
	return m
}

func TestScheduleCheckBackChatOnly(t *testing.T) {
	r := newRunRig(t)
	ctx := context.Background()
	res, err := r.d.mcpScheduleCheckBack(ctx, r.callerIn(r.chat), scheduleCheckBackParams{In: "2h", Note: "see if the PR merged"})
	if err != nil {
		t.Fatal(err)
	}
	slug := res.(routineToolResult).Slug
	data, err := os.ReadFile(filepath.Join(r.a.Dir, "routines", slug+".yaml"))
	if err != nil || !strings.Contains(string(data), "see if the PR merged") || !strings.Contains(string(data), "at: ") {
		t.Fatalf("file: %q %v", data, err)
	}
	if c := r.lastCommit(t, "routines/"+slug+".yaml"); c.Trailers["Argus-Source"] != "agent" {
		t.Fatalf("commit: %+v", c)
	}

	routineRun := r.runThread(t, "th_r", "routine")
	if _, err := r.d.mcpScheduleCheckBack(ctx, r.callerIn(routineRun), scheduleCheckBackParams{In: "2h", Note: "x"}); err == nil || !strings.Contains(err.Error(), "only in chats and check-back runs") {
		t.Fatalf("routine run: err = %v", err)
	}
	cbRun := r.runThread(t, "th_cb", "check-back")
	if _, err := r.d.mcpScheduleCheckBack(ctx, r.callerIn(cbRun), scheduleCheckBackParams{In: "3h", Note: "again"}); err != nil {
		t.Fatalf("check-back run: %v", err)
	}

	// The per-agent cap.
	for i := len(checkBacks(t, r.a.Dir)); i < 10; i++ {
		if _, err := r.d.mcpScheduleCheckBack(ctx, r.callerIn(r.chat), scheduleCheckBackParams{In: (time.Duration(i+4) * time.Hour).String(), Note: "n"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.d.mcpScheduleCheckBack(ctx, r.callerIn(r.chat), scheduleCheckBackParams{In: "20h", Note: "one too many"}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("cap: err = %v", err)
	}
}

func TestCheckBackClamp(t *testing.T) {
	set := pagent.Settings{}
	for in, want := range map[time.Duration]time.Duration{
		0: 24 * time.Hour, time.Minute: 15 * time.Minute, 4 * time.Hour: 4 * time.Hour, 30 * 24 * time.Hour: 7 * 24 * time.Hour,
	} {
		if got := clampCheckBack(set, in); got != want {
			t.Errorf("clamp(%s) = %s, want %s", in, got, want)
		}
	}

	r := newRunRig(t)
	rt := r.routine(t, "pr", "what: watch the PR\nstarts:\n  - check-back: self-paced\n")
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{{"1m", 15 * time.Minute}, {"30d", 7 * 24 * time.Hour}, {"", 24 * time.Hour}, {"6h", 6 * time.Hour}} {
		c := r.start(t, rt)
		before := time.Now()
		r.report(t, c, reportRunParams{Result: "progress", Summary: "waiting", NextCheckIn: tc.in})
		r.wait(t)
		got := r.state(t, "pr").CheckBackAt.Sub(before)
		if got < tc.want-time.Second || got > tc.want+5*time.Second {
			t.Errorf("next_check_in %q: next in %s, want %s", tc.in, got, tc.want)
		}
	}

	// A one-shot check-back's in is clamped too.
	before := time.Now()
	res, err := r.d.mcpScheduleCheckBack(context.Background(), r.callerIn(r.chat), scheduleCheckBackParams{In: "1m", Note: "soon"})
	if err != nil {
		t.Fatal(err)
	}
	slug := res.(routineToolResult).Slug
	data, _ := os.ReadFile(filepath.Join(r.a.Dir, "routines", slug+".yaml"))
	one, err := routine.Parse(slug, data)
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.ParseInLocation("2006-01-02T15:04", one.Starts[0].At, routineLoc(one, r.d.pagentSettings(r.a)))
	if err != nil || at.Before(before.Add(15*time.Minute-time.Minute)) || at.After(before.Add(17*time.Minute)) {
		t.Fatalf("one-shot at %s (%v), want about 15m from now", at, err)
	}
}

func TestCheckBackScheduled(t *testing.T) {
	r := newSchedRig(t)
	r.write(t, "pr", "what: watch the PR\nstarts:\n  - check-back: self-paced\n")
	r.tick()
	if st, _ := r.state(t, "pr"); !st.CheckBackAt.Equal(schedT0.Add(24 * time.Hour)) {
		t.Fatalf("state: %+v", st)
	}
	r.at(23 * time.Hour)
	r.tick()
	if r.startCount() != 0 {
		t.Fatal("started early")
	}
	r.at(24 * time.Hour)
	r.tick()
	r.tick()
	if r.startCount() != 1 || r.starts[0].StartedBy != "check-back" {
		t.Fatalf("starts: %+v", r.starts)
	}
	// Without a report, the fallback is a day after the dispatch.
	if st, _ := r.state(t, "pr"); !st.CheckBackAt.Equal(schedT0.Add(48 * time.Hour)) {
		t.Fatalf("state: %+v", st)
	}
}

func TestOneShotCheckBackRemovedAfterRun(t *testing.T) {
	r := newRunRig(t)
	res, err := r.d.mcpScheduleCheckBack(context.Background(), r.callerIn(r.chat), scheduleCheckBackParams{In: "2h", Note: "check the deploy"})
	if err != nil {
		t.Fatal(err)
	}
	slug := res.(routineToolResult).Slug
	data, _ := os.ReadFile(filepath.Join(r.a.Dir, "routines", slug+".yaml"))
	rt := r.routine(t, slug, string(data))
	c := r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "progress", Summary: "deployed"})
	r.wait(t)
	if _, err := os.Stat(filepath.Join(r.a.Dir, "routines", slug+".yaml")); !os.IsNotExist(err) {
		t.Fatalf("one-shot check-back kept: %v", err)
	}
	if _, ok := r.d.routineState(context.Background(), r.a.ID, slug); ok {
		t.Fatal("state kept")
	}
}

func TestRoutineCreateCommitsAndEvent(t *testing.T) {
	r := newRunRig(t)
	r.d.pagentCfg.Defaults.Limits.MinInterval = 15 * time.Minute
	ctx := context.Background()
	body := "what: check inbox\nstarts:\n  - every: 1h\n"
	res, err := r.d.mcpRoutineCreate(ctx, r.callerIn(r.chat), routineWriteParams{Slug: "inbox", Routine: body})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(routineToolResult)
	if out.Status != "created" || out.Commit == "" {
		t.Fatalf("result: %+v", out)
	}
	if got := r.read(t, "routines/inbox.yaml"); got != body {
		t.Fatalf("file: %q", got)
	}
	if c := r.lastCommit(t, "routines/inbox.yaml"); c.Trailers["Argus-Source"] != "agent" || c.Trailers["Argus-Thread"] != r.chat {
		t.Fatalf("commit: %+v", c)
	}
	ev := r.chatEvents(t, evRoutineChanged)
	if len(ev) != 1 || ev[0]["action"] != "create" || ev[0]["commit"] != out.Commit {
		t.Fatalf("events: %+v", ev)
	}
	if _, err := r.d.mcpRoutineCreate(ctx, r.callerIn(r.chat), routineWriteParams{Slug: "inbox", Routine: body}); err == nil {
		t.Fatal("create over an existing routine")
	}
	if _, err := r.d.mcpRoutineCreate(ctx, r.callerIn(r.chat), routineWriteParams{Slug: "fast", Routine: "what: x\nstarts:\n  - every: 1m\n"}); err == nil {
		t.Fatal("a routine under min-interval was accepted")
	}

	// Pause and resume edit the file.
	if _, err := r.d.mcpRoutineSetEnabled(ctx, r.callerIn(r.chat), routineRefParams{Slug: "inbox"}, false); err != nil {
		t.Fatal(err)
	}
	if got := r.read(t, "routines/inbox.yaml"); !strings.Contains(got, "enabled: false") || !strings.Contains(got, "what: check inbox") {
		t.Fatalf("paused file: %q", got)
	}
	if _, err := r.d.mcpRoutineSetEnabled(ctx, r.callerIn(r.chat), routineRefParams{Slug: "inbox"}, true); err != nil {
		t.Fatal(err)
	}
	if got := r.read(t, "routines/inbox.yaml"); !strings.Contains(got, "enabled: true") {
		t.Fatalf("resumed file: %q", got)
	}

	// Undo reverts the routine commit.
	if _, err := callPAgent[api.PAgentMemoryUndoResult](t, r.d.handlePAgentMemoryUndo, api.PAgentMemoryUndoParams{AgentID: r.a.ID, Commit: out.Commit}); err == nil {
		t.Fatal("undo of create should conflict with the later pause and resume")
	}
	del, err := r.d.mcpRoutineDelete(ctx, r.callerIn(r.chat), routineRefParams{Slug: "inbox"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := callPAgent[api.PAgentMemoryUndoResult](t, r.d.handlePAgentMemoryUndo, api.PAgentMemoryUndoParams{AgentID: r.a.ID, Commit: del.(routineToolResult).Commit}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.read(t, "routines/inbox.yaml"), "check inbox") {
		t.Fatal("undo did not restore the routine")
	}
}

func TestGoalOutcomeChangeNeedsReview(t *testing.T) {
	r := newRunRig(t)
	ctx := context.Background()
	goal := "what: migrate billing\nstarts:\n  - every: 1d\nuntil: outcome\noutcome: old client deleted\n"
	res, err := r.d.mcpRoutineCreate(ctx, r.callerIn(r.chat), routineWriteParams{Slug: "billing", Routine: goal, Reason: "you asked"})
	if err != nil || res.(routineToolResult).Status != "proposed" {
		t.Fatalf("create goal: %+v %v", res, err)
	}
	if r.read(t, "routines/billing.yaml") != "" {
		t.Fatal("a goal was written without review")
	}
	props, _ := r.store.PendingProposals(ctx, r.a.ID)
	if len(props) != 1 {
		t.Fatalf("proposals: %+v", props)
	}
	if _, err := callPAgent[api.PAgentMemoryDecideResult](t, r.d.handlePAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: props[0].ID, Accept: true}); err != nil {
		t.Fatal(err)
	}
	if r.read(t, "routines/billing.yaml") != goal {
		t.Fatalf("accepted goal: %q", r.read(t, "routines/billing.yaml"))
	}

	// Changing what goes through; changing the outcome or verify is staged.
	edited := strings.Replace(goal, "migrate billing", "migrate the billing service", 1)
	if res, err := r.d.mcpRoutineUpdate(ctx, r.callerIn(r.chat), routineWriteParams{Slug: "billing", Routine: edited}); err != nil || res.(routineToolResult).Status != "updated" {
		t.Fatalf("update what: %+v %v", res, err)
	}
	for _, body := range []string{
		strings.Replace(edited, "old client deleted", "done enough", 1),
		edited + "verify: true\n",
		strings.Replace(edited, "until: outcome\noutcome: old client deleted\n", "", 1),
	} {
		res, err := r.d.mcpRoutineUpdate(ctx, r.callerIn(r.chat), routineWriteParams{Slug: "billing", Routine: body})
		if err != nil || res.(routineToolResult).Status != "proposed" {
			t.Fatalf("update %q: %+v %v", body, res, err)
		}
	}
	if r.read(t, "routines/billing.yaml") != edited {
		t.Fatal("a staged change was written")
	}
	if n := len(r.chatEvents(t, evRoutineProposed)); n != 4 {
		t.Fatalf("proposed events = %d", n)
	}
}

func TestRoutineToolsRefusedInRun(t *testing.T) {
	r := newRunRig(t)
	ctx := context.Background()
	for _, startedBy := range []string{"routine", "check-back"} {
		th := r.runThread(t, "th_"+startedBy, startedBy)
		caller := r.callerIn(th)
		calls := map[string]func() (any, error){
			"list": func() (any, error) { return r.d.mcpRoutineList(ctx, caller) },
			"create": func() (any, error) {
				return r.d.mcpRoutineCreate(ctx, caller, routineWriteParams{Slug: "x", Routine: "what: x\nstarts:\n  - every: 1h\n"})
			},
			"update": func() (any, error) {
				return r.d.mcpRoutineUpdate(ctx, caller, routineWriteParams{Slug: "x", Routine: "what: x\nstarts:\n  - every: 1h\n"})
			},
			"pause":  func() (any, error) { return r.d.mcpRoutineSetEnabled(ctx, caller, routineRefParams{Slug: "x"}, false) },
			"resume": func() (any, error) { return r.d.mcpRoutineSetEnabled(ctx, caller, routineRefParams{Slug: "x"}, true) },
			"delete": func() (any, error) { return r.d.mcpRoutineDelete(ctx, caller, routineRefParams{Slug: "x"}) },
		}
		for name, call := range calls {
			if _, err := call(); err == nil || !strings.Contains(err.Error(), "only in chats") {
				t.Errorf("%s in a %s run: err = %v", name, startedBy, err)
			}
		}
	}
	if r.read(t, "routines/x.yaml") != "" {
		t.Fatal("a run wrote a routine")
	}
}
