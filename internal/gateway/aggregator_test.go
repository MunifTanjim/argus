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
	return session.Session{ID: id, Tool: "claude-code", Status: session.StatusWorking}
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

func recvEvent(t *testing.T, ch <-chan registry.Event) registry.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no event received")
		return registry.Event{}
	}
}

func TestAggregatorMergesWithCompositeIDsAndOrigin(t *testing.T) {
	a := New(time.Second)
	a.AddSource(newFakeSource("home", "home-box", sess("default:%1")))
	a.AddSource(newFakeSource("dev", "dev-box", sess("default:%2")))

	eventually(t, func() bool { return len(a.Snapshot()) == 2 })

	byID := map[string]session.Session{}
	for _, s := range a.Snapshot() {
		byID[s.ID] = s
	}
	h, ok := byID["home:default:%1"]
	if !ok {
		t.Fatalf("missing composite id home:default:%%1; got %v", byID)
	}
	if h.NodeID != "home" || h.NodeLabel != "home-box" {
		t.Fatalf("origin not stamped: %+v", h)
	}
	if _, ok := byID["dev:default:%2"]; !ok {
		t.Fatalf("missing composite id dev:default:%%2; got %v", byID)
	}
}

func TestAggregatorStreamsEvents(t *testing.T) {
	a := New(time.Second)
	events, cancel := a.Subscribe()
	defer cancel()

	src := newFakeSource("home", "home-box")
	a.AddSource(src)

	src.events <- registry.Event{Type: registry.EventAdded, Session: sess("default:%3")}
	ev := recvEvent(t, events)
	if ev.Type != registry.EventAdded || ev.Session.ID != "home:default:%3" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if ev.Session.NodeID != "home" {
		t.Fatalf("origin not stamped on streamed event: %+v", ev.Session)
	}
}

func TestAggregatorMarksSnapshotReplay(t *testing.T) {
	a := New(time.Second)
	events, cancel := a.Subscribe()
	defer cancel()

	// A connecting source's snapshot is a replay of existing state, not a live
	// change, so the push watcher can record it without re-notifying.
	a.AddSource(newFakeSource("home", "home-box", sess("default:%1")))

	ev := recvEvent(t, events)
	if ev.Type != registry.EventAdded {
		t.Fatalf("want EventAdded, got %v", ev.Type)
	}
	if !ev.Replay {
		t.Fatal("snapshot event must be marked Replay")
	}
}

func TestRouteForwardsToOwningSourceWithLocalID(t *testing.T) {
	a := New(time.Second)
	home := newFakeSource("home", "home-box", sess("default:%1"))
	dev := newFakeSource("dev", "dev-box", sess("default:%2"))
	home.callResp = json.RawMessage(`{"screen":"home"}`)
	a.AddSource(home)
	a.AddSource(dev)
	eventually(t, func() bool { return len(a.Snapshot()) == 2 })

	params := json.RawMessage(`{"session_id":"home:default:%1"}`)
	res, err := a.Route(context.Background(), "sessions.capture", params)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if string(res) != `{"screen":"home"}` {
		t.Fatalf("unexpected result: %s", res)
	}

	// Only "home" should have been called, with the node-local session id.
	if _, ok := dev.lastCall(); ok {
		t.Fatal("dev source should not have been called")
	}
	call, ok := home.lastCall()
	if !ok || call.method != "sessions.capture" {
		t.Fatalf("home not called correctly: %+v", call)
	}
	local, _ := sessionIDFromParams(call.params)
	if local != "default:%1" {
		t.Fatalf("want local id default:%%1, got %q", local)
	}
}

func TestRouteUnknownNode(t *testing.T) {
	a := New(time.Second)
	_, err := a.Route(context.Background(), "sessions.capture", json.RawMessage(`{"session_id":"ghost:default:%1"}`))
	if err == nil {
		t.Fatal("want error routing to unknown node")
	}
}

func TestSourceOfflineThenRemoved(t *testing.T) {
	a := New(60 * time.Millisecond)
	events, cancel := a.Subscribe()
	defer cancel()

	src := newFakeSource("home", "home-box", sess("default:%1"))
	a.AddSource(src)
	// Drain the initial added event.
	if ev := recvEvent(t, events); ev.Type != registry.EventAdded {
		t.Fatalf("want added, got %+v", ev)
	}

	close(src.done) // node disconnects

	off := recvEvent(t, events)
	if off.Type != registry.EventUpdated || !off.Session.Offline {
		t.Fatalf("want offline update, got %+v", off)
	}
	gone := recvEvent(t, events)
	if gone.Type != registry.EventRemoved {
		t.Fatalf("want removed after grace, got %+v", gone)
	}
	eventually(t, func() bool { return len(a.Snapshot()) == 0 })
}

func TestReconnectBeforeGraceKeepsSessions(t *testing.T) {
	a := New(400 * time.Millisecond)
	src := newFakeSource("home", "home-box", sess("default:%1"))
	a.AddSource(src)
	eventually(t, func() bool { return len(a.Snapshot()) == 1 })

	close(src.done) // disconnect; removal scheduled in 400ms
	eventually(t, func() bool {
		for _, s := range a.Snapshot() {
			if s.Offline {
				return true
			}
		}
		return false
	})

	// Reconnect well before grace elapses.
	time.Sleep(50 * time.Millisecond)
	a.AddSource(newFakeSource("home", "home-box", sess("default:%1")))

	// Past the original grace window, the session must still be present and online.
	time.Sleep(500 * time.Millisecond)
	snap := a.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 session after reconnect, got %d", len(snap))
	}
	if snap[0].Offline {
		t.Fatal("session should be back online after reconnect")
	}
}

func TestRouteToNodeForwardsAndCompositesResult(t *testing.T) {
	a := New(time.Second)
	home := newFakeSource("home", "home-box", sess("default:%1"))
	dev := newFakeSource("dev", "dev-box", sess("default:%2"))
	home.callResp = json.RawMessage(`{"session_id":"default:%9","pane_id":"%9"}`)
	a.AddSource(home)
	a.AddSource(dev)
	eventually(t, func() bool { return len(a.Snapshot()) == 2 })

	params := json.RawMessage(`{"node_id":"home","name":"x"}`)
	res, err := a.RouteToNode(context.Background(), "home", "sessions.spawn", params)
	if err != nil {
		t.Fatalf("route to node: %v", err)
	}
	got, _ := sessionIDFromParams(res)
	if got != "home:default:%9" {
		t.Fatalf("want composite session id home:default:%%9, got %q", got)
	}
	if _, ok := dev.lastCall(); ok {
		t.Fatal("dev source should not have been called")
	}
	if call, ok := home.lastCall(); !ok || call.method != "sessions.spawn" {
		t.Fatalf("home not called correctly: %+v", call)
	}
}

func TestRouteToNodeUnknown(t *testing.T) {
	a := New(time.Second)
	_, err := a.RouteToNode(context.Background(), "ghost", "sessions.spawn", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("want error routing to unknown node")
	}
}

func TestNodeIDFromParams(t *testing.T) {
	id, err := nodeIDFromParams(json.RawMessage(`{"node_id":"home","name":"x"}`))
	if err != nil || id != "home" {
		t.Fatalf("want home, got %q err %v", id, err)
	}
}
