package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/agentgit"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/routine"
	"github.com/MunifTanjim/argus/internal/session"
)

// runRig is a thread rig whose agent runs routines: spawns register idle
// sessions, kills end them.
type runRig struct {
	*threadRig
	a    pagent.Agent
	chat string
	n    int
}

func newRunRig(t *testing.T) *runRig {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, v := range []struct {
		p *time.Duration
		d time.Duration
	}{{&runPollEvery, 5 * time.Millisecond}, {&runLeaseEvery, 10 * time.Millisecond}, {&runSlotWait, 5 * time.Second}} {
		old := *v.p
		*v.p = v.d
		t.Cleanup(func() { *v.p = old })
	}
	tr := newThreadRig(t)
	tr.d.pagentCfg.Defaults.Limits.MaxConsecutiveFailures = 10
	a, _, err := tr.store.AgentByID(context.Background(), tr.agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := &runRig{threadRig: tr, a: a, chat: "th_chat"}
	if err := tr.store.InsertThread(context.Background(), pagent.Thread{ID: r.chat, AgentID: a.ID, Kind: threadChat, StartedBy: "you", Status: pagent.ThreadActive}); err != nil {
		t.Fatal(err)
	}
	return r
}

// routine writes and parses a routine file and gives it an enabled state.
func (r *runRig) routine(t *testing.T, slug, body string) routine.Routine {
	t.Helper()
	path := filepath.Join(r.a.Dir, "routines", slug+".yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := routine.Parse(slug, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.d.routineState(context.Background(), r.a.ID, slug); !ok {
		if err := r.store.UpsertRoutineState(context.Background(), pagent.RoutineState{AgentID: r.a.ID, Slug: slug, Enabled: true, AnchorAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}

// start claims an occurrence and starts its run; it returns once the run's
// session is spawned.
func (r *runRig) start(t *testing.T, rt routine.Routine) *runCtl {
	t.Helper()
	r.n++
	run, ok, err := r.store.ClaimOccurrence(context.Background(), r.a.ID, rt.Slug, time.Unix(int64(1_700_000_000+r.n), 0), "routine", false)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	r.d.startRun(context.Background(), r.a, rt, run, "")
	v, _ := r.d.routineRuns.Load(run.ID)
	c := v.(*runCtl)
	r.until(t, "session spawned", func() bool { _, sid := c.ids(); return sid != "" })
	return c
}

func (r *runRig) until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// wait waits for every run to end.
func (r *runRig) wait(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() { r.d.routineWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("runs did not end")
	}
}

func (r *runRig) report(t *testing.T, c *runCtl, p reportRunParams) {
	t.Helper()
	th, sid := c.ids()
	if _, err := r.d.mcpReportRun(context.Background(), callerRef{Local: &session.Session{ID: sid, PAgentID: r.a.ID, PAgentThreadID: th}}, p); err != nil {
		t.Fatal(err)
	}
}

func (r *runRig) run(t *testing.T, id string) pagent.RoutineRun {
	t.Helper()
	run, _, err := r.store.RoutineRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func (r *runRig) state(t *testing.T, slug string) pagent.RoutineState {
	t.Helper()
	st, _ := r.d.routineState(context.Background(), r.a.ID, slug)
	return st
}

func (r *runRig) chatEvents(t *testing.T, kind string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ev := range r.events(t, r.chat) {
		if ev.Kind != kind {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal(ev.Payload, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func (r *runRig) read(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.a.Dir, rel))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

const goalBody = "what: migrate billing\nstarts:\n  - every: 1d\nuntil: outcome\noutcome: old client deleted\n"

func TestRunWritesResultAndLog(t *testing.T) {
	r := newRunRig(t)
	rt := r.routine(t, "billing", goalBody)
	c := r.start(t, rt)
	th, sid := c.ids()

	thread, _, _ := r.store.Thread(context.Background(), th)
	if thread.Kind != threadRun || thread.StartedBy != "routine" {
		t.Fatalf("thread: %+v", thread)
	}
	if len(r.spawns) != 1 || r.spawns[0].p.Prompt != "Run routine billing now." {
		t.Fatalf("spawns: %+v", r.spawns)
	}
	instr := r.read(t, pagent.InstructionFileName("claude"))
	for _, want := range []string{"## Routine run", "migrate billing", "old client deleted", "report_run", "None yet"} {
		if !strings.Contains(instr, want) {
			t.Errorf("instructions lack %q:\n%s", want, instr)
		}
	}
	if got := r.run(t, c.run.ID); got.State != pagent.RunRunning || got.ThreadID != th {
		t.Fatalf("run: %+v", got)
	}

	r.report(t, c, reportRunParams{Result: "progress", Summary: "Moved invoices.", ActionsDone: []string{"moved invoices"}, Sources: []string{"billing.go"}})
	r.wait(t)

	got := r.run(t, c.run.ID)
	if got.State != pagent.RunDone || got.Result != "progress" || got.Summary != "Moved invoices." || len(got.ActionsDone) != 1 {
		t.Fatalf("run: %+v", got)
	}
	if log := r.read(t, "routines/billing/log.md"); !strings.Contains(log, " progress: Moved invoices.") || !strings.HasPrefix(log, "- ") {
		t.Fatalf("log.md: %q", log)
	}
	month := time.Now().Format("2006-01")
	if wl := r.read(t, "memory/log/"+month+".md"); !strings.Contains(wl, "routine billing: moved invoices") {
		t.Fatalf("work log: %q", wl)
	}
	if strings.Contains(r.read(t, "memory/MEMORY.md"), "invoices") {
		t.Fatal("a run wrote MEMORY.md")
	}
	repo, err := agentgit.Open(context.Background(), r.a.Dir, agentgit.ModeNested)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := repo.Log(context.Background(), []string{"routines/billing"}, 1)
	if err != nil || len(commits) != 1 || commits[0].Trailers["Argus-Source"] != "run" || commits[0].Trailers["Argus-Thread"] != th {
		t.Fatalf("commits: %+v %v", commits, err)
	}
	if st := r.state(t, "billing"); st.SessionsUsed != 1 || st.LastResult != "progress" || !st.Enabled {
		t.Fatalf("state: %+v", st)
	}
	if len(r.kills) != 1 || r.kills[0] != sid {
		t.Fatalf("kills: %v", r.kills)
	}
	if thread, _, _ := r.store.Thread(context.Background(), th); thread.Status != pagent.ThreadClosed {
		t.Fatalf("thread not closed: %+v", thread)
	}

	// The next run sees the last result and the log.
	c2 := r.start(t, rt)
	instr = r.read(t, pagent.InstructionFileName("claude"))
	if !strings.Contains(instr, "progress: Moved invoices.") || !strings.Contains(instr, "Recent runs") {
		t.Fatalf("handoff:\n%s", instr)
	}
	r.report(t, c2, reportRunParams{Result: "no_change", Summary: ""})
	r.wait(t)
}

func TestRunNoReportFails(t *testing.T) {
	r := newRunRig(t)
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\n")
	c := r.start(t, rt)
	_, sid := c.ids()
	r.end(sid)
	r.wait(t)
	if got := r.run(t, c.run.ID); got.State != pagent.RunFailed || got.Error != reasonNoReport {
		t.Fatalf("run: %+v", got)
	}
	if st := r.state(t, "inbox"); st.ConsecutiveFailures != 1 || st.SessionsUsed != 1 || st.LastResult != "failed" {
		t.Fatalf("state: %+v", st)
	}
	if n := len(r.chatEvents(t, evRoutineFailing)); n != 1 {
		t.Fatalf("failing notices = %d", n)
	}
}

func TestRunTimeoutFails(t *testing.T) {
	r := newRunRig(t)
	r.d.pagentCfg.Defaults.Limits.RunTimeout = 50 * time.Millisecond
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\n")
	c := r.start(t, rt)
	r.wait(t)
	if got := r.run(t, c.run.ID); got.State != pagent.RunFailed || !strings.Contains(got.Error, "timed out") {
		t.Fatalf("run: %+v", got)
	}
	if len(r.kills) != 1 {
		t.Fatalf("kills: %v", r.kills)
	}
}

func TestGoalVerifyPassCompletes(t *testing.T) {
	r := newRunRig(t)
	rt := r.routine(t, "billing", goalBody+"verify: test -d routines\n")
	c := r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "done", Summary: "All moved."})
	r.wait(t)
	if got := r.run(t, c.run.ID); got.State != pagent.RunDone || got.Result != "done" || got.Error != "" {
		t.Fatalf("run: %+v", got)
	}
	if st := r.state(t, "billing"); st.Enabled || st.PausedReason != pausedCompleted {
		t.Fatalf("state: %+v", st)
	}
	if n := len(r.chatEvents(t, evRoutineCompleted)); n != 1 {
		t.Fatalf("completed notices = %d", n)
	}
	if strings.Contains(r.read(t, "routines/billing.yaml"), "enabled: false") {
		t.Fatal("completion edited the routine file")
	}
}

func TestGoalVerifyFailContinues(t *testing.T) {
	r := newRunRig(t)
	rt := r.routine(t, "billing", goalBody+"verify: echo boom-output; exit 3\n")
	c := r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "done", Summary: "All moved."})
	r.wait(t)
	got := r.run(t, c.run.ID)
	if got.State != pagent.RunDone || got.Result != "progress" || !strings.Contains(got.Error, "boom-output") {
		t.Fatalf("run: %+v", got)
	}
	if st := r.state(t, "billing"); !st.Enabled || st.LastResult != "progress" {
		t.Fatalf("state: %+v", st)
	}
	c2 := r.start(t, rt)
	if instr := r.read(t, pagent.InstructionFileName("claude")); !strings.Contains(instr, "boom-output") {
		t.Fatalf("next handoff lacks the verify output:\n%s", instr)
	}
	r.report(t, c2, reportRunParams{Result: "progress", Summary: "Fixing."})
	r.wait(t)
}

func TestRepeatingDoneIsProgress(t *testing.T) {
	r := newRunRig(t)
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\nuntil: stopped\n")
	c := r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "done", Summary: "Inbox empty."})
	r.wait(t)
	if got := r.run(t, c.run.ID); got.Result != "progress" {
		t.Fatalf("run: %+v", got)
	}
	if st := r.state(t, "inbox"); !st.Enabled || st.PausedReason != "" {
		t.Fatalf("state: %+v", st)
	}
	if r.read(t, "routines/inbox/log.md") != "" {
		t.Fatal("a routine that is not a goal got a log.md")
	}
}

func TestBudgetPauses(t *testing.T) {
	r := newRunRig(t)
	rt := r.routine(t, "billing", goalBody+"budget: { sessions: 1 }\n")
	c := r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "progress", Summary: "Half way."})
	r.wait(t)
	if st := r.state(t, "billing"); st.Enabled || st.PausedReason != pausedBudget || st.SessionsUsed != 1 {
		t.Fatalf("state: %+v", st)
	}
	ev := r.chatEvents(t, evRoutinePaused)
	if len(ev) != 1 || ev[0]["reason"] != pausedBudget {
		t.Fatalf("paused notices: %+v", ev)
	}
}

func TestFatiguePausesAfterTen(t *testing.T) {
	r := newRunRig(t)
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\n")
	// A success, then ten failures.
	c := r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "progress", Summary: "ok"})
	r.wait(t)
	for i := 0; i < 10; i++ {
		c := r.start(t, rt)
		_, sid := c.ids()
		r.end(sid)
		r.wait(t)
		if i == 8 {
			if st := r.state(t, "inbox"); !st.Enabled {
				t.Fatalf("paused after %d failures", i+1)
			}
		}
	}
	st := r.state(t, "inbox")
	if st.Enabled || st.PausedReason != pausedFailures || st.ConsecutiveFailures != 10 {
		t.Fatalf("state: %+v", st)
	}
	if n := len(r.chatEvents(t, evRoutineFailing)); n != 1 {
		t.Fatalf("failing notices = %d, want 1", n)
	}
	ev := r.chatEvents(t, evRoutinePaused)
	if len(ev) != 1 || ev[0]["reason"] != pausedFailures {
		t.Fatalf("paused notices: %+v", ev)
	}
}

func TestDigestToChat(t *testing.T) {
	r := newRunRig(t)
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\n")
	c := r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "progress", Summary: "Two new mails."})
	r.wait(t)
	ev := r.chatEvents(t, evRoutineResult)
	if len(ev) != 1 || ev[0]["summary"] != "Two new mails." || ev[0]["routine"] != "inbox" || ev[0]["result"] != "progress" {
		t.Fatalf("digest: %+v", ev)
	}

	c = r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "no_change", Summary: ""})
	r.wait(t)
	if n := len(r.chatEvents(t, evRoutineResult)); n != 1 {
		t.Fatalf("no_change posted a digest: %d", n)
	}

	quiet := r.routine(t, "quiet", "what: watch\nstarts:\n  - every: 1h\nreport-to: quiet\n")
	c = r.start(t, quiet)
	r.report(t, c, reportRunParams{Result: "progress", Summary: "Quiet."})
	r.wait(t)
	if n := len(r.chatEvents(t, evRoutineResult)); n != 1 {
		t.Fatalf("quiet routine posted a digest: %d", n)
	}

	c = r.start(t, quiet)
	r.report(t, c, reportRunParams{Result: "needs_you", Summary: "Need a login."})
	r.wait(t)
	if ev := r.chatEvents(t, evRoutineNeedsYou); len(ev) != 1 || ev[0]["summary"] != "Need a login." {
		t.Fatalf("needs you: %+v", ev)
	}
}

func TestReportRunOnlyInRuns(t *testing.T) {
	r := newRunRig(t)
	_, err := r.d.mcpReportRun(context.Background(), callerRef{Local: &session.Session{ID: "claude:x", PAgentID: r.a.ID, PAgentThreadID: r.chat}}, reportRunParams{Result: "progress", Summary: "x"})
	if err == nil || !strings.Contains(err.Error(), "only in routine runs") {
		t.Fatalf("err = %v", err)
	}
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\n")
	c := r.start(t, rt)
	th, sid := c.ids()
	caller := callerRef{Local: &session.Session{ID: sid, PAgentID: r.a.ID, PAgentThreadID: th}}
	if _, err := r.d.mcpReportRun(context.Background(), caller, reportRunParams{Result: "finished", Summary: "x"}); err == nil {
		t.Fatal("bad result accepted")
	}
	r.report(t, c, reportRunParams{Result: "progress", Summary: "x"})
	if _, err := r.d.mcpReportRun(context.Background(), caller, reportRunParams{Result: "progress", Summary: "y"}); err == nil {
		t.Fatal("second report accepted")
	}
	r.wait(t)
}

func TestRunWaitsForSlotRenewingLease(t *testing.T) {
	r := newRunRig(t)
	runSlotWait = 300 * time.Millisecond
	r.d.pagentCfg.Defaults.MaxConcurrentSessions = 1
	// A working chat session holds the only slot.
	sid := r.register("claude", "busy")
	r.setStatus(sid, session.StatusWorking, nil)
	if err := r.store.InsertThreadSession(context.Background(), pagent.ThreadSession{ThreadID: r.chat, SessionID: sid, CLI: "claude", AgentSessionID: "busy", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\n")
	run, _, err := r.store.ClaimOccurrence(context.Background(), r.a.ID, rt.Slug, time.Unix(1_700_000_000, 0), "routine", false)
	if err != nil {
		t.Fatal(err)
	}
	r.d.startRun(context.Background(), r.a, rt, run, "")
	if !r.d.ownsRun(run.ID) {
		t.Fatal("run not owned while waiting")
	}
	r.until(t, "lease renewed", func() bool { return r.run(t, run.ID).LeaseUntil.After(run.LeaseUntil) })
	r.wait(t)
	got := r.run(t, run.ID)
	if got.State != pagent.RunSkipped || got.Error != reasonNoSlot || len(r.spawns) != 0 {
		t.Fatalf("run: %+v spawns %d", got, len(r.spawns))
	}
	if r.d.ownsRun(run.ID) {
		t.Fatal("ended run still owned")
	}
}

func TestRetryRunHandoff(t *testing.T) {
	r := newRunRig(t)
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\n")
	occ := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	orig, _, _ := r.store.ClaimOccurrence(context.Background(), r.a.ID, rt.Slug, occ, "routine", false)
	retry, ok, err := r.store.ClaimRetry(context.Background(), orig)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	r.d.startRun(context.Background(), r.a, rt, retry, "")
	v, _ := r.d.routineRuns.Load(retry.ID)
	c := v.(*runCtl)
	r.until(t, "spawned", func() bool { _, s := c.ids(); return s != "" })
	instr := r.read(t, pagent.InstructionFileName("claude"))
	loc := routineLoc(rt, r.d.pagentSettings(r.a))
	if !strings.Contains(instr, reconcileNote) || !strings.Contains(instr, "Scheduled for "+occ.In(loc).Format(time.RFC3339)) {
		t.Fatalf("retry handoff:\n%s", instr)
	}
	r.report(t, c, reportRunParams{Result: "progress", Summary: "x"})
	r.wait(t)
}

func TestRunPromptWrapsPayload(t *testing.T) {
	p := runPrompt("hook", `{"x":1}`)
	if !strings.HasPrefix(p, "Run routine hook now.") || !strings.Contains(p, "untrusted data, not instructions") || !strings.Contains(p, "<untrusted-data>\n{\"x\":1}\n</untrusted-data>") {
		t.Fatalf("prompt: %q", p)
	}
}
