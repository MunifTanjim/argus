package client

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// snapshotNote is the session.event a node pushes the moment its channel opens.
func snapshotNote(sessionID string) *fakeNote {
	ev := registry.Event{Type: registry.EventAdded, Session: session.Session{ID: sessionID, Agent: "claude"}}
	b, _ := json.Marshal(ev)
	return &fakeNote{method: api.MethodSessionEvent, params: b}
}

// newLifecycleNode builds a fakeNode that pushes one session on channel open and
// echoes every request.
func newLifecycleNode(t *testing.T, id, sessionID string) *fakeNode {
	t.Helper()
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	return &fakeNode{
		id:            id,
		key:           kp,
		postHandshake: snapshotNote(sessionID),
		handle: func(_ string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
			return params, nil, nil
		},
	}
}

// awaitSessionEventFor reads the client's stream until a session.event for nodeID
// arrives, and returns it.
func awaitSessionEventFor(t *testing.T, c *E2EClient, nodeID string) registry.Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case n := <-c.Events():
			if n.Method != api.MethodSessionEvent {
				continue
			}
			var ev registry.Event
			if err := json.Unmarshal(n.Params, &ev); err != nil {
				t.Fatalf("decode session.event: %v", err)
			}
			if ev.Session.NodeID != nodeID {
				continue
			}
			return ev
		case <-deadline:
			t.Fatalf("no session.event for node %q reached the client", nodeID)
		}
	}
}

// A node that joins after the client connected must get its own channel. The
// client's session view is per-node now, so a node with no channel is invisible
// until the whole gateway connection is rebuilt.
func TestE2EClientOpensChannelForLateJoiningNode(t *testing.T) {
	early := newLifecycleNode(t, "n1", "argus:%1")
	g, clientConn := newFakeMultiGateway(t, early)
	defer g.peer.Close()

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	awaitSessionEventFor(t, c, "n1")

	late := newLifecycleNode(t, "n2", "argus:%2")
	g.addNode(late)
	g.emitNodeEvent(api.NodeEventAdded, late)

	if ev := awaitSessionEventFor(t, c, "n2"); ev.Type != registry.EventAdded {
		t.Fatalf("late node event type = %q, want added", ev.Type)
	}
	// The channel must be usable, not merely opened.
	var out map[string]any
	if err := c.callNode("n2", "sessions.input", map[string]any{"text": "hi"}, &out); err != nil {
		t.Fatalf("callNode on the late node: %v", err)
	}
}

// A roster notification for a node the client already has a channel to must not
// open a second one: the node streams its whole registry snapshot per channel, so
// a duplicate channel duplicates every session event on it.
func TestE2EClientIgnoresOnlineEventForAlreadyOpenNode(t *testing.T) {
	n := newLifecycleNode(t, "n1", "argus:%1")
	g, clientConn := newFakeMultiGateway(t, n)
	defer g.peer.Close()

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	awaitSessionEventFor(t, c, "n1")

	g.emitNodeEvent(api.NodeEventOnline, n)

	select {
	case ev := <-c.Events():
		t.Fatalf("second channel opened; got a duplicate %s", ev.Method)
	case <-time.After(300 * time.Millisecond):
	}
}

// The same guard must hold while the first channel is still being opened: a
// roster event landing mid-handshake finds no registered channel yet, and without
// an in-flight guard the client opens a second one and doubles every session event.
func TestE2EClientIgnoresOnlineEventDuringHandshake(t *testing.T) {
	n := newLifecycleNode(t, "n1", "argus:%1")
	g, clientConn := newFakeMultiGateway(t, n)
	defer g.peer.Close()
	var once sync.Once
	n.beforeMsg2 = func() { once.Do(func() { g.emitNodeEvent(api.NodeEventOnline, n) }) }

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	awaitSessionEventFor(t, c, "n1")

	select {
	case ev := <-c.Events():
		t.Fatalf("second channel opened during the handshake; got a duplicate %s", ev.Method)
	case <-time.After(500 * time.Millisecond):
	}
}

// A node reconnecting (online) after a drop must get a fresh channel too.
func TestE2EClientReopensChannelWhenNodeComesBackOnline(t *testing.T) {
	n := newLifecycleNode(t, "n1", "argus:%1")
	g, clientConn := newFakeMultiGateway(t, n)
	defer g.peer.Close()

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	c.offlineGrace = time.Hour // isolate reopen from the removal sweep
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	awaitSessionEventFor(t, c, "n1")

	g.emitNodeEvent(api.NodeEventOffline, n)
	if ev := awaitSessionEventFor(t, c, "n1"); !ev.Session.Offline {
		t.Fatalf("offline event = %+v, want the session marked offline", ev.Session)
	}

	g.emitNodeEvent(api.NodeEventOnline, n)
	ev := awaitSessionEventFor(t, c, "n1")
	if ev.Type != registry.EventAdded || ev.Session.Offline {
		t.Fatalf("re-open event = %q offline=%v, want added and not offline", ev.Type, ev.Session.Offline)
	}
}

// A node going offline greys its sessions immediately and drops them once the
// grace window passes — without this the list keeps ghost sessions forever, since
// a dead node can never send the removals itself.
func TestE2EClientGreysThenRemovesSessionsOfOfflineNode(t *testing.T) {
	n := newLifecycleNode(t, "n1", "argus:%1")
	g, clientConn := newFakeMultiGateway(t, n)
	defer g.peer.Close()

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	c.offlineGrace = 50 * time.Millisecond
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	added := awaitSessionEventFor(t, c, "n1")
	if added.Session.Offline {
		t.Fatalf("live session already marked offline: %+v", added.Session)
	}

	g.emitNodeEvent(api.NodeEventOffline, n)

	greyed := awaitSessionEventFor(t, c, "n1")
	if greyed.Type != registry.EventUpdated || !greyed.Session.Offline {
		t.Fatalf("first event = %q offline=%v, want updated and offline", greyed.Type, greyed.Session.Offline)
	}
	if greyed.Session.ID != added.Session.ID {
		t.Fatalf("offline event id = %q, want %q", greyed.Session.ID, added.Session.ID)
	}

	gone := awaitSessionEventFor(t, c, "n1")
	if gone.Type != registry.EventRemoved {
		t.Fatalf("post-grace event = %q, want removed", gone.Type)
	}
}

// A node dropped from the roster is gone for good: no grace, remove at once.
func TestE2EClientRemovesSessionsOfRemovedNodeImmediately(t *testing.T) {
	n := newLifecycleNode(t, "n1", "argus:%1")
	g, clientConn := newFakeMultiGateway(t, n)
	defer g.peer.Close()

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	c.offlineGrace = time.Hour // a removal must not wait on it
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	awaitSessionEventFor(t, c, "n1")

	g.emitNodeEvent(api.NodeEventRemoved, n)
	if ev := awaitSessionEventFor(t, c, "n1"); ev.Type != registry.EventRemoved {
		t.Fatalf("event = %q, want removed", ev.Type)
	}
}
