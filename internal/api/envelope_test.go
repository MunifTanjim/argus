package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageWithoutRouteMarshalsUnchanged(t *testing.T) {
	// A local (non-relay) frame must not emit route/body keys — byte-identical to today.
	m := message{JSONRPC: jsonrpcVersion, Method: "ping"}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if strings.Contains(s, "route") || strings.Contains(s, "body") {
		t.Errorf("non-relay frame leaked route/body: %s", s)
	}
	if m.isRelay() {
		t.Error("message with nil Route must not be a relay frame")
	}
}

func TestRelayMessageRoundTrips(t *testing.T) {
	m := message{
		JSONRPC: jsonrpcVersion,
		Method:  "sessions.input",
		Route:   &RouteHeader{ChanID: "c7", NodeID: "home", SubID: "s-1"},
		Body:    json.RawMessage(`"c2VhbGVk"`),
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got message
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.isRelay() {
		t.Fatal("round-tripped relay frame lost its Route")
	}
	if got.Route.ChanID != "c7" || got.Route.NodeID != "home" || got.Route.SubID != "s-1" {
		t.Errorf("route = %+v", got.Route)
	}
	if string(got.Body) != `"c2VhbGVk"` {
		t.Errorf("body = %s", got.Body)
	}
}
