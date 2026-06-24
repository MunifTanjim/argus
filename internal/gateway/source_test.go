package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/registry"
)

func TestInProcessSourceCallMarshalsDispatchResult(t *testing.T) {
	dispatch := func(_ context.Context, method string, params json.RawMessage) (any, error) {
		return map[string]string{"method": method, "params": string(params)}, nil
	}
	src := NewInProcessSource("home", "home-box", registry.New(), dispatch)

	if src.ID() != "home" || src.Label() != "home-box" {
		t.Fatalf("identity: %q/%q", src.ID(), src.Label())
	}
	if got := src.Snapshot(); len(got) != 0 {
		t.Fatalf("empty registry should snapshot empty, got %d", len(got))
	}

	res, err := src.Call(context.Background(), "sessions.capture", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var out struct{ Method, Params string }
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Method != "sessions.capture" || out.Params != `{"x":1}` {
		t.Fatalf("dispatch not forwarded faithfully: %+v", out)
	}
}

// An in-process source plugged into the aggregator surfaces no sessions from an
// empty registry but routes control calls through to the local dispatcher.
func TestInProcessSourceRoutesThroughAggregator(t *testing.T) {
	called := make(chan string, 1)
	dispatch := func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		called <- method
		return map[string]bool{"ok": true}, nil
	}
	a := New(0)
	a.AddSource(NewInProcessSource("home", "home-box", registry.New(), dispatch))

	if _, err := a.Route(context.Background(), "sessions.kill", json.RawMessage(`{"session_id":"home:default:%1"}`)); err != nil {
		t.Fatalf("route: %v", err)
	}
	select {
	case m := <-called:
		if m != "sessions.kill" {
			t.Fatalf("want sessions.kill, got %q", m)
		}
	default:
		t.Fatal("dispatch was not invoked")
	}
}
