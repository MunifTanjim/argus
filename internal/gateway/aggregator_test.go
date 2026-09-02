package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

type callRecord struct {
	method string
	params json.RawMessage
}

type fakeSource struct {
	id, label string
	snap      []session.Session
	events    chan registry.Event
	done      chan struct{}

	mu       sync.Mutex
	calls    []callRecord
	callResp json.RawMessage
}

func newFakeSource(id, label string, snap ...session.Session) *fakeSource {
	return &fakeSource{
		id: id, label: label, snap: snap,
		events: make(chan registry.Event, 16),
		done:   make(chan struct{}),
	}
}

func (f *fakeSource) ID() string      { return f.id }
func (f *fakeSource) Label() string   { return f.label }
func (f *fakeSource) Version() string { return "" }
func (f *fakeSource) Capabilities() api.NodeCapabilities {
	return api.NodeCapabilities{SpawnSession: true}
}
func (f *fakeSource) Snapshot() []session.Session                { return f.snap }
func (f *fakeSource) Subscribe() (<-chan registry.Event, func()) { return f.events, func() {} }
func (f *fakeSource) Done() <-chan struct{}                      { return f.done }
func (f *fakeSource) IdentityPubKey() string                     { return "" }
func (f *fakeSource) SignerPubKey() string                       { return "" }

func (f *fakeSource) Call(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, callRecord{method, params})
	return f.callResp, nil
}

func (f *fakeSource) lastCall() (callRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return callRecord{}, false
	}
	return f.calls[len(f.calls)-1], true
}

func sess(id string) session.Session {
	return session.Session{ID: id, Agent: "claude", Status: session.StatusWorking}
}

func eventually(t *testing.T, want func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if want() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func recvRosterEvent(t *testing.T, ch <-chan api.NodeEvent) api.NodeEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no roster event received")
		return api.NodeEvent{}
	}
}

func TestNodeIDFromParams(t *testing.T) {
	id, err := nodeIDFromParams(json.RawMessage(`{"node_id":"home","name":"x"}`))
	if err != nil || id != "home" {
		t.Fatalf("want home, got %q err %v", id, err)
	}
}

func TestRosterAndSubscribeRoster(t *testing.T) {
	a := New(0)
	events, cancel := a.SubscribeRoster()
	defer cancel()

	src := newFakeSource("home", "home-box")
	a.AddSource(src)

	ev := recvRosterEvent(t, events)
	if ev.Type != api.NodeEventAdded {
		t.Fatalf("want added event, got %q", ev.Type)
	}
	if ev.Node.ID != "home" || ev.Node.Label != "home-box" {
		t.Fatalf("unexpected node in event: %+v", ev.Node)
	}
	if !ev.Node.Online {
		t.Fatal("node should be online in added event")
	}

	roster := a.Roster()
	if len(roster) != 1 || roster[0].ID != "home" {
		t.Fatalf("want 1 roster entry for home, got %v", roster)
	}
	if !roster[0].Online {
		t.Fatal("roster entry should be online")
	}
}
