package push

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// runWatch feeds events through Watch and returns the targets the sender saw,
// one per dispatched notification.
func runWatch(t *testing.T, events []registry.Event) []Target {
	t.Helper()
	store := NewStore(t.TempDir())
	mustUpsert(t, store, "dev-1", Target{Endpoint: "https://up.example/x"})
	sender := &fakeSender{}
	d := NewDispatcher(store, sender, nil)

	ch := make(chan registry.Event)
	done := make(chan struct{})
	go func() { Watch(context.Background(), ch, Sinks{Immediate: []Sink{d}}, nil); close(done) }()
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	<-done

	sender.mu.Lock()
	defer sender.mu.Unlock()
	return sender.sent
}

func ev(typ registry.EventType, id string, st session.Status) registry.Event {
	return registry.Event{Type: typ, Session: session.Session{ID: id, Status: st}}
}

// replay builds a snapshot-replay event (the aggregator emits these when a
// source connects or reconnects).
func replay(id string, st session.Status) registry.Event {
	e := ev(registry.EventAdded, id, st)
	e.Replay = true
	return e
}

func TestWatchReplayOnFreshStartDoesNotNotify(t *testing.T) {
	// After a gateway restart, Watch starts with no prior state; reconnecting
	// nodes replay their snapshots. An already-awaiting session must not re-notify.
	sent := runWatch(t, []registry.Event{
		replay("s1", session.StatusAwaitingInput),
	})
	if len(sent) != 0 {
		t.Fatalf("fired %d times, want 0 (snapshot replay must not notify)", len(sent))
	}
}

func TestWatchReplayDoesNotReNotify(t *testing.T) {
	// A live transition notifies once; a later replay of the same awaiting
	// session (reconnect snapshot) must not re-notify.
	sent := runWatch(t, []registry.Event{
		ev(registry.EventUpdated, "s1", session.StatusAwaitingInput), // fire
		replay("s1", session.StatusAwaitingInput),                    // reconnect snapshot: no fire
	})
	if len(sent) != 1 {
		t.Fatalf("fired %d times, want 1 (replay must not re-notify)", len(sent))
	}
}

func TestWatchLiveNewAwaitingNotifies(t *testing.T) {
	// A genuinely new session first seen awaiting (e.g. a permission hook for a
	// session argus hadn't recorded) arrives as a live EventAdded and must notify.
	sent := runWatch(t, []registry.Event{
		ev(registry.EventAdded, "s1", session.StatusAwaitingInput),
	})
	if len(sent) != 1 {
		t.Fatalf("fired %d times, want 1 (live new awaiting must notify)", len(sent))
	}
}

func TestWatchFiresOnceOnAwaitingTransition(t *testing.T) {
	sent := runWatch(t, []registry.Event{
		ev(registry.EventAdded, "s1", session.StatusWorking),
		ev(registry.EventUpdated, "s1", session.StatusAwaitingInput), // fire
		ev(registry.EventUpdated, "s1", session.StatusAwaitingInput), // no refire
	})
	if len(sent) != 1 {
		t.Fatalf("fired %d times, want 1", len(sent))
	}
}

func TestWatchRefiresAfterLeavingAwaiting(t *testing.T) {
	sent := runWatch(t, []registry.Event{
		ev(registry.EventUpdated, "s1", session.StatusAwaitingInput), // fire
		ev(registry.EventUpdated, "s1", session.StatusWorking),
		ev(registry.EventUpdated, "s1", session.StatusAwaitingInput), // fire again
	})
	if len(sent) != 2 {
		t.Fatalf("fired %d times, want 2", len(sent))
	}
}

func TestWatchDeathClearsStateThenReNotifies(t *testing.T) {
	sent := runWatch(t, []registry.Event{
		ev(registry.EventUpdated, "s1", session.StatusAwaitingInput), // fire
		ev(registry.EventRemoved, "s1", session.StatusAwaitingInput), // genuine death (not offline): clears
		ev(registry.EventAdded, "s1", session.StatusAwaitingInput),   // genuinely new: fire
	})
	if len(sent) != 2 {
		t.Fatalf("fired %d times, want 2", len(sent))
	}
}

func TestWatchIgnoresNonAwaitingStatuses(t *testing.T) {
	sent := runWatch(t, []registry.Event{
		ev(registry.EventAdded, "s1", session.StatusWorking),
		ev(registry.EventUpdated, "s1", session.StatusIdle),
		ev(registry.EventUpdated, "s2", session.StatusDiscovered),
	})
	if len(sent) != 0 {
		t.Fatalf("fired %d times, want 0", len(sent))
	}
}

func TestNotificationForRendersInteraction(t *testing.T) {
	cases := []struct {
		name     string
		sess     session.Session
		wantBody string
	}{
		{
			name: "permission with tool",
			sess: session.Session{
				Interaction: &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"},
			},
			wantBody: "Permission: Bash",
		},
		{
			name: "question with header",
			sess: session.Session{
				Interaction: &session.Interaction{
					Kind:      session.InteractionQuestion,
					Questions: []session.QuestionSpec{{Header: "Auth method"}},
				},
			},
			wantBody: "Question: Auth method",
		},
		{
			name:     "plan",
			sess:     session.Session{Interaction: &session.Interaction{Kind: session.InteractionPlan}},
			wantBody: "Plan ready to review",
		},
		{
			name:     "no interaction",
			sess:     session.Session{},
			wantBody: "Needs your attention",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := notificationFor(tc.sess)
			if n.Body != tc.wantBody {
				t.Errorf("Body = %q, want %q", n.Body, tc.wantBody)
			}
		})
	}
}

func TestNotificationForSurfacesSessionName(t *testing.T) {
	cases := []struct {
		name      string
		sess      session.Session
		wantTitle string
		wantBody  string
	}{
		{
			name: "named session in repo appends name to title",
			sess: session.Session{
				Repo: "argus", Name: "auth-refactor",
				Interaction: &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"},
			},
			wantTitle: "argus · auth-refactor",
			wantBody:  "Permission: Bash",
		},
		{
			name:      "unnamed session leaves body unchanged",
			sess:      session.Session{Repo: "argus", Interaction: &session.Interaction{Kind: session.InteractionPlan}},
			wantTitle: "argus",
			wantBody:  "Plan ready to review",
		},
		{
			name:      "name equal to repo is not duplicated",
			sess:      session.Session{Repo: "argus", Name: "argus"},
			wantTitle: "argus",
			wantBody:  "Needs your attention",
		},
		{
			name:      "no repo: name is the title, not the body",
			sess:      session.Session{Name: "auth-refactor"},
			wantTitle: "auth-refactor",
			wantBody:  "Needs your attention",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := notificationFor(tc.sess)
			if n.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", n.Title, tc.wantTitle)
			}
			if n.Body != tc.wantBody {
				t.Errorf("Body = %q, want %q", n.Body, tc.wantBody)
			}
		})
	}
}

func TestNotificationForNodeLabelPrefixWithName(t *testing.T) {
	n := notificationFor(session.Session{
		Repo: "argus", Name: "auth-refactor", NodeLabel: "MacBook",
		Interaction: &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"},
	})
	if want := "MacBook · argus · auth-refactor"; n.Title != want {
		t.Errorf("Title = %q, want %q", n.Title, want)
	}
	if want := "Permission: Bash"; n.Body != want {
		t.Errorf("Body = %q, want %q", n.Body, want)
	}
}

func TestNotificationForCarriesSessionID(t *testing.T) {
	n := notificationFor(session.Session{ID: "node1:abc", NodeID: "node1"})
	if n.Data["session_id"] != "node1:abc" {
		t.Errorf("Data[session_id] = %q, want node1:abc", n.Data["session_id"])
	}
	if n.Data["node_id"] != "node1" {
		t.Errorf("Data[node_id] = %q, want node1", n.Data["node_id"])
	}
}

// recordSink counts notifications it receives.
type recordSink struct {
	mu sync.Mutex
	n  []Notification
}

func (r *recordSink) Notify(_ context.Context, n Notification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n = append(r.n, n)
}

func TestWatchFansToAllSinks(t *testing.T) {
	a, b := &recordSink{}, &recordSink{}
	ch := make(chan registry.Event)
	done := make(chan struct{})
	go func() { Watch(context.Background(), ch, Sinks{Immediate: []Sink{a, b}}, nil); close(done) }()
	ch <- ev(registry.EventUpdated, "s1", session.StatusAwaitingInput) // one transition
	close(ch)
	<-done

	if len(a.n) != 1 || len(b.n) != 1 {
		t.Fatalf("fan-out = a:%d b:%d, want 1 each", len(a.n), len(b.n))
	}
}

func (r *recordSink) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.n)
}

func waitCount(t *testing.T, r *recordSink, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.count() >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout: count = %d, want >= %d", r.count(), want)
}

func startDelayed(t *testing.T, delay time.Duration) (im, dl *recordSink, ch chan registry.Event) {
	t.Helper()
	im, dl = &recordSink{}, &recordSink{}
	ch = make(chan registry.Event)
	done := make(chan struct{})
	go func() {
		Watch(context.Background(), ch, Sinks{Immediate: []Sink{im}, Delayed: []Sink{dl}, Delay: delay}, nil)
		close(done)
	}()
	t.Cleanup(func() { close(ch); <-done })
	return
}

func TestWatchDelayedFiresAfterGrace(t *testing.T) {
	im, dl, ch := startDelayed(t, 30*time.Millisecond)
	ch <- ev(registry.EventUpdated, "s1", session.StatusAwaitingInput)
	waitCount(t, im, 1) // immediate fires now
	if got := dl.count(); got != 0 {
		t.Fatalf("delayed fired before grace: %d", got)
	}
	waitCount(t, dl, 1) // delayed fires after grace
}

func TestWatchDelayedSuppressedWhenAnswered(t *testing.T) {
	im, dl, ch := startDelayed(t, 50*time.Millisecond)
	ch <- ev(registry.EventUpdated, "s1", session.StatusAwaitingInput)
	waitCount(t, im, 1)
	ch <- ev(registry.EventUpdated, "s1", session.StatusWorking) // answered within window
	time.Sleep(120 * time.Millisecond)                           // past the delay
	if got := dl.count(); got != 0 {
		t.Fatalf("delayed fired despite answer: %d", got)
	}
}

func TestWatchDelayedFiresWhenIdleAtFireTime(t *testing.T) {
	im, dl, ch := startDelayed(t, 30*time.Millisecond)
	ch <- ev(registry.EventUpdated, "s1", session.StatusAwaitingInput)
	waitCount(t, im, 1)
	ch <- ev(registry.EventUpdated, "s1", session.StatusIdle) // still needs attention
	waitCount(t, dl, 1)
}

func TestWatchDelayedZeroFiresImmediately(t *testing.T) {
	_, dl, ch := startDelayed(t, 0)
	ch <- ev(registry.EventUpdated, "s1", session.StatusAwaitingInput)
	waitCount(t, dl, 1) // no delay: fires on the transition
}

func TestWatchDelayedReArmsAfterLeavingAwaiting(t *testing.T) {
	_, dl, ch := startDelayed(t, 40*time.Millisecond)
	ch <- ev(registry.EventUpdated, "s1", session.StatusAwaitingInput) // arm T1
	ch <- ev(registry.EventUpdated, "s1", session.StatusWorking)       // cancel T1 (answered)
	ch <- ev(registry.EventUpdated, "s1", session.StatusAwaitingInput) // re-arm T2
	waitCount(t, dl, 1)                                                // T2 fires after its grace
	time.Sleep(60 * time.Millisecond)                                  // let any stale T1 signal surface
	if got := dl.count(); got != 1 {
		t.Fatalf("delayed fired %d times, want exactly 1 (T1 cancelled, T2 fires once)", got)
	}
}

func TestWatchDelayedCancelledOnRemoval(t *testing.T) {
	im, dl, ch := startDelayed(t, 50*time.Millisecond)
	ch <- ev(registry.EventUpdated, "s1", session.StatusAwaitingInput)
	waitCount(t, im, 1)
	ch <- ev(registry.EventRemoved, "s1", session.StatusAwaitingInput) // gone within window
	time.Sleep(120 * time.Millisecond)
	if got := dl.count(); got != 0 {
		t.Fatalf("delayed fired after removal: %d", got)
	}
}

func TestWatchDelayedRendersLatestSnapshot(t *testing.T) {
	_, dl, ch := startDelayed(t, 40*time.Millisecond)
	// Arm with a permission interaction...
	arm := ev(registry.EventUpdated, "s1", session.StatusAwaitingInput)
	arm.Session.Interaction = &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"}
	ch <- arm
	// ...then update to a different interaction before the grace elapses (same
	// awaiting status, so it refreshes the armed snapshot without re-arming).
	upd := ev(registry.EventUpdated, "s1", session.StatusAwaitingInput)
	upd.Session.Interaction = &session.Interaction{Kind: session.InteractionPlan}
	ch <- upd

	waitCount(t, dl, 1)
	dl.mu.Lock()
	body := dl.n[0].Body
	dl.mu.Unlock()
	if body != "Plan ready to review" {
		t.Fatalf("delayed body = %q, want latest snapshot %q", body, "Plan ready to review")
	}
}
