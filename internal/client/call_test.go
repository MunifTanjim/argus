package client

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestCallSessionsListStampsComposite(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method == api.MethodSessionsList {
			b, _ := json.Marshal([]session.Session{{ID: "s1"}, {ID: "s2"}})
			return b, nil, nil
		}
		return json.RawMessage(`null`), nil, nil
	}
	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	var sessions []session.Session
	if err := c.Call(api.MethodSessionsList, nil, &sessions); err != nil {
		t.Fatalf("Call sessions.list: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
	for _, s := range sessions {
		if s.NodeID != "n1" {
			t.Errorf("session %s not stamped with node", s.ID)
		}
		if _, _, ok := session.SplitCompositeID(s.ID); !ok {
			t.Errorf("session id %q not composite", s.ID)
		}
	}
}

func TestCallSessionAddressedSplitsCompositeToLocal(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var gotSessionID string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method == api.MethodSessionInput {
			gotSessionID, _ = sessionIDFromParams(params)
		}
		return json.RawMessage(`null`), nil, nil
	}
	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	composite := session.CompositeID("n1", "local-s")
	err := c.Call(api.MethodSessionInput, map[string]any{"session_id": composite, "text": "x"}, nil)
	if err != nil {
		t.Fatalf("Call input: %v", err)
	}
	if gotSessionID != "local-s" {
		t.Errorf("node saw session_id %q, want local-s (composite must be stripped)", gotSessionID)
	}
}

func TestCallNodeAddressedCompositesSpawnResult(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method == api.MethodSessionSpawn {
			return json.RawMessage(`{"session_id":"newlocal"}`), nil, nil
		}
		return json.RawMessage(`null`), nil, nil
	}
	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	var res struct {
		SessionID string `json:"session_id"`
	}
	if err := c.Call(api.MethodSessionSpawn, map[string]any{"node_id": "n1"}, &res); err != nil {
		t.Fatalf("Call spawn: %v", err)
	}
	if res.SessionID != session.CompositeID("n1", "newlocal") {
		t.Errorf("spawn result session_id = %q, want composite", res.SessionID)
	}
}

func TestCallTerminalHandleRoutesByOpenNode(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	sawInput := false
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method == api.MethodTerminalInput {
			sawInput = true
		}
		return json.RawMessage(`null`), nil, nil
	}
	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// open records term_id -> node
	if err := c.Call(api.MethodTerminalOpen, map[string]any{
		"session_id": session.CompositeID("n1", "s"), "term_id": "t1",
	}, nil); err != nil {
		t.Fatalf("terminal.open: %v", err)
	}
	// input routes by remembered term_id (no session_id present)
	if err := c.Call(api.MethodTerminalInput, map[string]any{"term_id": "t1", "data": "aGk="}, nil); err != nil {
		t.Fatalf("terminal.input: %v", err)
	}
	if !sawInput {
		t.Error("node did not receive terminal.input routed by term_id")
	}
}

func TestCallUnknownTerminalHandleErrors(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return json.RawMessage(`null`), nil, nil
	}
	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.Call(api.MethodTerminalInput, map[string]any{"term_id": "nope"}, nil); err == nil {
		t.Fatal("terminal.input for an unknown term_id must error")
	}
}

func TestSessionEventNotificationStampedComposite(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	// On this request the node emits a session.event notification with a NODE-LOCAL id.
	f.handle = func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		ev, _ := json.Marshal(registryEvent("added", "slocal"))
		return json.RawMessage(`null`), nil, &fakeNote{method: api.MethodSessionEvent, params: ev}
	}
	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.Call(api.MethodSessionsRefresh, nil, nil); err != nil {
		// sessions.refresh fans out; the handler above also fires the notification
	}
	select {
	case ev := <-c.Events():
		if ev.Method != api.MethodSessionEvent {
			t.Fatalf("method = %q", ev.Method)
		}
		var got map[string]any
		if err := json.Unmarshal(ev.Params, &got); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		sess := got["session"].(map[string]any)
		if sess["id"] != session.CompositeID("n1", "slocal") {
			t.Errorf("event session id = %v, want composite", sess["id"])
		}
		if sess["node_id"] != "n1" {
			t.Errorf("event session node_id = %v, want n1", sess["node_id"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no session.event delivered")
	}
}

func TestCallSessionsListMergesMultipleNodes(t *testing.T) {
	k1, _ := e2e.GenerateKeyPair()
	k2, _ := e2e.GenerateKeyPair()
	listHandler := func(id string) func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
			if method == api.MethodSessionsList {
				b, _ := json.Marshal([]session.Session{{ID: "s-" + id}})
				return b, nil, nil
			}
			return json.RawMessage(`null`), nil, nil
		}
	}
	n1 := &fakeNode{id: "n1", key: k1, handle: listHandler("n1")}
	n2 := &fakeNode{id: "n2", key: k2, handle: listHandler("n2")}
	g, clientConn := newFakeMultiGateway(t, n1, n2)
	defer g.peer.Close()

	c, _ := NewE2EClient(clientConn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	var sessions []session.Session
	if err := c.Call(api.MethodSessionsList, nil, &sessions); err != nil {
		t.Fatalf("sessions.list: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2 (one per node)", len(sessions))
	}
	byNode := map[string]string{} // nodeID -> composite id
	for _, s := range sessions {
		byNode[s.NodeID] = s.ID
	}
	if byNode["n1"] != session.CompositeID("n1", "s-n1") {
		t.Errorf("n1 session = %q, want composite of s-n1", byNode["n1"])
	}
	if byNode["n2"] != session.CompositeID("n2", "s-n2") {
		t.Errorf("n2 session = %q, want composite of s-n2", byNode["n2"])
	}
}

// registryEvent builds a registry.Event JSON with a node-local session id.
func registryEvent(typ, id string) map[string]any {
	return map[string]any{"type": typ, "session": map[string]any{"id": id}}
}
