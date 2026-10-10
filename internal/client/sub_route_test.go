package client

import (
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

// The sub_id -> node route exists before the subscribe reaches the node, so an
// unsubscribe sent while the subscribe is in flight still routes; it is dropped
// on unsubscribe and when the subscribe fails.
func TestTranscriptSubRouteLifecycle(t *testing.T) {
	f, conn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	c, _ := NewE2EClient(conn)
	defer c.Close()
	routed := func(id string) bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.subNode[id] != ""
	}
	var routedDuringSubscribe bool
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodTranscriptSubscribe:
			id, _ := subIDFromParams(params)
			routedDuringSubscribe = routed(id)
			if id == "bad" {
				return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown session"}, nil
			}
			return json.RawMessage(`{"sub_id":"` + id + `","entries":[]}`), nil, nil
		}
		return json.RawMessage(`null`), nil, nil
	}
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := c.Call(api.MethodTranscriptSubscribe, api.TranscriptSubscribeParams{SubID: "s1", SessionID: "n1:sess"}, nil); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if !routedDuringSubscribe {
		t.Fatal("sub route must exist before the subscribe reaches the node")
	}
	if err := c.Call(api.MethodTranscriptUnsubscribe, api.TranscriptUnsubscribeParams{SubID: "s1"}, nil); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if routed("s1") {
		t.Fatal("unsubscribe must drop the sub route")
	}

	if err := c.Call(api.MethodTranscriptSubscribe, api.TranscriptSubscribeParams{SubID: "bad", SessionID: "n1:sess"}, nil); err == nil {
		t.Fatal("subscribe to an unknown session should fail")
	}
	if routed("bad") {
		t.Fatal("a failed subscribe must drop its sub route")
	}
}
