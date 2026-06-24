package push

import (
	"context"
	"testing"

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
	go func() { Watch(context.Background(), ch, d, nil); close(done) }()
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

func TestNotificationForCarriesSessionID(t *testing.T) {
	n := notificationFor(session.Session{ID: "node1:abc", NodeID: "node1"})
	if n.Data["session_id"] != "node1:abc" {
		t.Errorf("Data[session_id] = %q, want node1:abc", n.Data["session_id"])
	}
	if n.Data["node_id"] != "node1" {
		t.Errorf("Data[node_id] = %q, want node1", n.Data["node_id"])
	}
}
