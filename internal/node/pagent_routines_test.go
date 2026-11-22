package node

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/routine"
)

type schedRig struct {
	d      *Node
	store  *pagent.Store
	agent  pagent.Agent
	thread string
	root   string

	mu     sync.Mutex
	clock  time.Time
	starts []pagent.RoutineRun
	// hold leaves started runs running (lease far ahead) instead of done.
	hold bool
}

var schedT0 = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func newSchedRig(t *testing.T) *schedRig {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "argus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	r := &schedRig{clock: schedT0, root: t.TempDir()}
	r.store = pagent.NewStore(sqlDB, r.now)
	r.d = r.node(t)
	ctx := context.Background()
	r.agent = pagent.Agent{ID: pagent.NewID(), Name: "scout", Dir: filepath.Join(r.root, "scout"), CreatedAt: schedT0}
	if err := r.store.InsertAgent(ctx, r.agent); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(r.agent.Dir, "routines"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.thread = pagent.NewID()
	if err := r.store.InsertThread(ctx, pagent.Thread{ID: r.thread, AgentID: r.agent.ID, Kind: threadChat, StartedBy: "you", Status: pagent.ThreadActive, CreatedAt: schedT0, LastActiveAt: schedT0}); err != nil {
		t.Fatal(err)
	}
	return r
}

// node builds a node on the rig's store and clock.
func (r *schedRig) node(t *testing.T) *Node {
	d := newTestNode(t)
	cfg := config.AgentsConfig{}
	cfg.Defaults.Timezone = "UTC"
	l := &cfg.Defaults.Limits
	l.MinInterval, l.MaxEnabledRoutines, l.MaxRunsPerHour = 15*time.Minute, 20, 12
	l.CatchUpWindow, l.PauseAfterAway = 12*time.Hour, 7*24*time.Hour
	d.SetAgentStore(r.store, cfg)
	d.pagentPrimaryFile = filepath.Join(r.root, "pagent-primary")
	d.lastClientFile = filepath.Join(r.root, "pagent-last-client")
	d.pagentNow = r.now
	d.routineStartFn = func(ctx context.Context, _ pagent.Agent, _ routine.Routine, run pagent.RoutineRun) error {
		r.mu.Lock()
		r.starts = append(r.starts, run)
		hold := r.hold
		r.mu.Unlock()
		if hold {
			return r.store.SetRunThread(ctx, run.ID, "th_run", r.now().Add(1000*time.Hour))
		}
		return r.store.SetRunState(ctx, run.ID, pagent.RunDone, "")
	}
	return d
}

func (r *schedRig) now() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clock
}

func (r *schedRig) at(d time.Duration) {
	r.mu.Lock()
	r.clock = schedT0.Add(d)
	r.mu.Unlock()
}

func (r *schedRig) tick() { r.d.routineTick(context.Background()) }

func (r *schedRig) write(t *testing.T, slug, body string) {
	t.Helper()
	path := filepath.Join(r.agent.Dir, "routines", slug+".yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// A rewrite within the same mtime tick must still be seen as a change.
	mt := r.now().Add(time.Duration(len(body)) * time.Second)
	_ = os.Chtimes(path, mt, mt)
}

func (r *schedRig) startCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.starts)
}

func (r *schedRig) runs(t *testing.T, slug string) []pagent.RoutineRun {
	t.Helper()
	runs, err := r.store.RunsSince(context.Background(), r.agent.ID, slug, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func (r *schedRig) state(t *testing.T, slug string) (pagent.RoutineState, bool) {
	t.Helper()
	sts, err := r.store.RoutineStates(context.Background(), r.agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sts {
		if s.Slug == slug {
			return s, true
		}
	}
	return pagent.RoutineState{}, false
}

func (r *schedRig) notices(t *testing.T, kind string) []pagent.Event {
	t.Helper()
	evs, err := r.store.Events(context.Background(), r.thread, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []pagent.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

const hourly = "what: check inbox\nstarts:\n  - every: 1h\n"

func TestSchedulerRunsDueOnce(t *testing.T) {
	r := newSchedRig(t)
	r.write(t, "inbox", hourly)
	r.tick()
	st, ok := r.state(t, "inbox")
	if !ok || !st.NextDueAt.Equal(schedT0.Add(time.Hour)) || !st.AnchorAt.Equal(schedT0) {
		t.Fatalf("state: %+v %v", st, ok)
	}
	if r.startCount() != 0 {
		t.Fatal("a new routine waits for its next due time")
	}

	r.at(time.Hour)
	r.tick()
	r.tick()
	other := r.node(t) // a second node process on the same database
	other.routineTick(context.Background())
	if r.startCount() != 1 {
		t.Fatalf("starts = %d, want 1", r.startCount())
	}
	if run := r.starts[0]; !run.Occurrence.Equal(schedT0.Add(time.Hour)) || run.StartedBy != "routine" {
		t.Fatalf("run: %+v", run)
	}
	if st, _ := r.state(t, "inbox"); !st.NextDueAt.Equal(schedT0.Add(2*time.Hour)) || !st.AnchorAt.Equal(schedT0) {
		t.Fatalf("next due: %+v", st)
	}

	// Removed file: the state stays but the routine is no longer scheduled.
	_ = os.Remove(filepath.Join(r.agent.Dir, "routines", "inbox.yaml"))
	r.at(2 * time.Hour)
	r.tick()
	if _, ok := r.state(t, "inbox"); !ok || r.startCount() != 1 {
		t.Fatalf("removed routine: state %v starts %d", ok, r.startCount())
	}

	// Archived agent: ignored.
	r.write(t, "inbox", hourly)
	_ = r.store.ArchiveAgent(context.Background(), r.agent.ID)
	r.at(3 * time.Hour)
	r.tick()
	if r.startCount() != 1 {
		t.Fatalf("archived agent ran: %d", r.startCount())
	}
}

func TestSchedulerMissedCollapses(t *testing.T) {
	r := newSchedRig(t)
	r.write(t, "inbox", hourly)
	r.tick()

	// Node down for 3 days: one missed row, then only the newest runs.
	r.at(72*time.Hour + 30*time.Minute)
	r.tick()
	r.tick()
	if r.startCount() != 1 || !r.starts[0].Occurrence.Equal(schedT0.Add(72*time.Hour)) {
		t.Fatalf("starts: %+v", r.starts)
	}
	var missed []pagent.RoutineRun
	for _, run := range r.runs(t, "inbox") {
		if run.State == pagent.RunMissed {
			missed = append(missed, run)
		}
	}
	if len(missed) != 1 || !missed[0].Occurrence.Equal(schedT0.Add(time.Hour)) || !strings.Contains(missed[0].Error, "71") {
		t.Fatalf("missed: %+v", missed)
	}
	if n := len(r.notices(t, evRoutineMissed)); n != 1 {
		t.Fatalf("missed notices = %d", n)
	}
	if st, _ := r.state(t, "inbox"); !st.NextDueAt.Equal(schedT0.Add(73 * time.Hour)) {
		t.Fatalf("next due: %v", st.NextDueAt)
	}

	// Newest occurrence older than the catch-up window: all recorded missed, nothing runs.
	r.write(t, "daily", "what: digest\nstarts:\n  - every: 1d\n")
	r.at(73 * time.Hour)
	r.tick() // anchor at 73h
	r.at(73*time.Hour + 3*24*time.Hour + 13*time.Hour)
	r.tick()
	runs := r.runs(t, "daily")
	if len(runs) != 1 || runs[0].State != pagent.RunMissed || !strings.Contains(runs[0].Error, "3 occurrences") {
		t.Fatalf("daily runs: %+v", runs)
	}
	if st, _ := r.state(t, "daily"); !st.NextDueAt.Equal(schedT0.Add(73*time.Hour + 4*24*time.Hour)) {
		t.Fatalf("daily next due: %v", st.NextDueAt)
	}
}

func TestSchedulerOverlapSkip(t *testing.T) {
	r := newSchedRig(t)
	r.hold = true
	r.write(t, "inbox", hourly)
	r.tick()
	r.at(time.Hour)
	r.tick()
	r.at(2 * time.Hour)
	r.tick()
	r.at(3 * time.Hour)
	r.tick()
	if r.startCount() != 1 {
		t.Fatalf("starts = %d", r.startCount())
	}
	runs := r.runs(t, "inbox")
	if len(runs) != 3 || runs[1].State != pagent.RunSkipped || runs[1].Error != "previous run still active" || runs[2].State != pagent.RunSkipped {
		t.Fatalf("runs: %+v", runs)
	}
	if n := len(r.notices(t, evRoutineSkipped)); n != 1 {
		t.Fatalf("skipped notices = %d, want one per streak", n)
	}
}

func TestSchedulerOverlapQueue(t *testing.T) {
	r := newSchedRig(t)
	r.hold = true
	r.write(t, "inbox", "what: check inbox\noverlap: queue\nstarts:\n  - every: 1h\n")
	r.tick()
	r.at(time.Hour)
	r.tick()
	r.at(2 * time.Hour)
	r.tick()
	r.at(3 * time.Hour)
	r.tick()
	runs := r.runs(t, "inbox")
	if len(runs) != 3 || runs[1].State != pagent.RunQueued || runs[2].State != pagent.RunSkipped {
		t.Fatalf("runs: %+v", runs)
	}
	_ = r.store.SetRunState(context.Background(), runs[0].ID, pagent.RunDone, "")
	r.tick()
	if r.startCount() != 2 || r.starts[1].ID != runs[1].ID {
		t.Fatalf("queued run not started: %+v", r.starts)
	}
}

func TestSchedulerRunsPerHourCap(t *testing.T) {
	r := newSchedRig(t)
	r.d.pagentCfg.Defaults.Limits.MaxRunsPerHour = 2
	for _, s := range []string{"a", "b", "c"} {
		r.write(t, s, "what: x\nstarts:\n  - every: 15m\n")
	}
	r.tick()
	r.at(15 * time.Minute)
	r.tick()
	if r.startCount() != 2 {
		t.Fatalf("starts = %d, want 2", r.startCount())
	}
	runs := r.runs(t, "c")
	if len(runs) != 1 || runs[0].State != pagent.RunSkipped || !strings.Contains(runs[0].Error, "max-runs-per-hour") {
		t.Fatalf("c runs: %+v", runs)
	}
	if st, _ := r.state(t, "c"); !st.NextDueAt.Equal(schedT0.Add(30 * time.Minute)) {
		t.Fatalf("c next due: %v", st.NextDueAt)
	}
}

func TestSchedulerInvalidFileIsolated(t *testing.T) {
	r := newSchedRig(t)
	r.write(t, "good", hourly)
	r.write(t, "bad", "what: x\nstarts:\n  - schedule: \"61 * * * *\"\n")
	r.tick()
	r.tick()
	if n := len(r.notices(t, evRoutineError)); n != 1 {
		t.Fatalf("error notices = %d, want 1", n)
	}
	if _, ok := r.state(t, "bad"); ok {
		t.Fatal("invalid routine got state")
	}
	r.at(time.Hour)
	r.tick()
	if r.startCount() != 1 || r.starts[0].Slug != "good" {
		t.Fatalf("starts: %+v", r.starts)
	}
	// A different invalid definition is reported again.
	r.write(t, "bad", "what: x\nstarts:\n  - every: 1m\n")
	r.tick()
	if n := len(r.notices(t, evRoutineError)); n != 2 {
		t.Fatalf("error notices = %d, want 2", n)
	}
}

func TestSchedulerEnabledCap(t *testing.T) {
	r := newSchedRig(t)
	r.d.pagentCfg.Defaults.Limits.MaxEnabledRoutines = 1
	r.write(t, "a", hourly)
	r.write(t, "b", hourly)
	r.write(t, "c", "what: x\nenabled: false\nstarts:\n  - every: 1h\n")
	r.tick()
	r.tick()
	if _, ok := r.state(t, "a"); !ok {
		t.Fatal("a not scheduled")
	}
	if _, ok := r.state(t, "b"); ok {
		t.Fatal("b beyond the cap was scheduled")
	}
	if n := len(r.notices(t, evRoutineError)); n != 1 {
		t.Fatalf("error notices = %d, want 1", n)
	}
}

func TestInterruptedRetriedOnce(t *testing.T) {
	r := newSchedRig(t)
	ctx := context.Background()
	r.write(t, "digest", "what: x\nstarts:\n  - every: 1d\n")
	r.tick()
	orig, _, err := r.store.ClaimOccurrence(ctx, r.agent.ID, "digest", schedT0.Add(-time.Hour), "routine", false)
	if err != nil {
		t.Fatal(err)
	}
	// The retry is started but never renews its lease.
	r.d.routineStartFn = func(_ context.Context, _ pagent.Agent, _ routine.Routine, run pagent.RoutineRun) error {
		r.starts = append(r.starts, run)
		return nil
	}
	r.at(3 * time.Minute)
	r.tick()
	if got, _, _ := r.store.RoutineRun(ctx, orig.ID); got.State != pagent.RunInterrupted {
		t.Fatalf("original: %+v", got)
	}
	if r.startCount() != 1 || r.starts[0].RetryOf != orig.ID {
		t.Fatalf("retry: %+v", r.starts)
	}
	r.at(6 * time.Minute)
	r.tick()
	r.at(9 * time.Minute)
	r.tick()
	if got, _, _ := r.store.RoutineRun(ctx, r.starts[0].ID); got.State != pagent.RunInterrupted {
		t.Fatalf("retry: %+v", got)
	}
	if r.startCount() != 1 {
		t.Fatalf("retried more than once: %+v", r.starts)
	}
}

func TestSchedulerStaleNotice(t *testing.T) {
	r := newSchedRig(t)
	r.hold = true
	r.write(t, "inbox", hourly)
	r.tick()
	r.at(time.Hour)
	r.tick() // starts and stays running; later occurrences are skipped
	for h := 2; h <= 30; h++ {
		r.at(time.Duration(h) * time.Hour)
		r.tick()
	}
	if n := len(r.notices(t, evRoutineStale)); n != 1 {
		t.Fatalf("stale notices = %d, want 1", n)
	}
}

func TestSchedulerPausesAfterAway(t *testing.T) {
	r := newSchedRig(t)
	r.write(t, "inbox", hourly)
	r.tick()
	r.at(8 * 24 * time.Hour)
	r.tick()
	r.tick()
	st, _ := r.state(t, "inbox")
	if st.Enabled || st.PausedReason != pausedAway || r.startCount() != 0 {
		t.Fatalf("state: %+v starts %d", st, r.startCount())
	}
	if n := len(r.notices(t, evRoutinePaused)); n != 1 {
		t.Fatalf("paused notices = %d", n)
	}
	// The last-attached time survives a restart.
	data, err := os.ReadFile(r.d.lastClientFile)
	if n, _ := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err != nil || n != schedT0.Unix() {
		t.Fatalf("last client file: %q %v", data, err)
	}
}

func TestSchedulerAttachedClientKeepsRunning(t *testing.T) {
	r := newSchedRig(t)
	r.write(t, "inbox", hourly)
	r.tick()
	r.at(8*24*time.Hour - time.Minute)
	n := &fakeNotifier{ch: make(chan api.Notification, 1)}
	r.d.registerConn(n)
	if _, err := r.d.handleSessionsList(api.WithNotifier(context.Background(), n), nil); err != nil {
		t.Fatal(err)
	}
	r.at(8 * 24 * time.Hour)
	r.tick()
	if st, _ := r.state(t, "inbox"); !st.Enabled {
		t.Fatalf("paused with a client attached: %+v", st)
	}
	if r.startCount() != 1 {
		t.Fatalf("starts = %d", r.startCount())
	}
}

func TestSchedulerHookConnectionIsNotAClient(t *testing.T) {
	r := newSchedRig(t)
	r.write(t, "inbox", hourly)
	r.tick()
	r.at(8*24*time.Hour - time.Minute)
	// A blocking permission hook holds its connection open but never lists sessions.
	r.d.registerConn(&fakeNotifier{ch: make(chan api.Notification, 1)})
	r.at(8 * 24 * time.Hour)
	r.tick()
	if st, _ := r.state(t, "inbox"); st.Enabled || st.PausedReason != pausedAway {
		t.Fatalf("a hook connection kept routines running: %+v", st)
	}
}

func TestExpiredLeaseOfOwnedRunNotInterrupted(t *testing.T) {
	r := newSchedRig(t)
	ctx := context.Background()
	r.write(t, "digest", "what: x\nstarts:\n  - every: 1d\n")
	r.tick()
	run, _, err := r.store.ClaimOccurrence(ctx, r.agent.ID, "digest", schedT0.Add(-time.Hour), "routine", false)
	if err != nil {
		t.Fatal(err)
	}
	r.d.routineRuns.Store(run.ID, struct{}{})
	r.at(10 * time.Minute)
	r.tick()
	if got, _, _ := r.store.RoutineRun(ctx, run.ID); got.State != pagent.RunClaimed {
		t.Fatalf("owned run: %+v", got)
	}
	if r.startCount() != 0 {
		t.Fatalf("owned run retried: %+v", r.starts)
	}
	// Renewed after the tick listed it: still not interrupted.
	r.d.routineRuns.Delete(run.ID)
	if err := r.store.RenewLease(ctx, run.ID, r.now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if r.d.interruptRun(ctx, run) {
		t.Fatal("renewed run interrupted")
	}
}
