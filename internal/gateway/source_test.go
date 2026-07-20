package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
)

func TestInProcessSourceCallMarshalsDispatchResult(t *testing.T) {
	dispatch := func(_ context.Context, method string, params json.RawMessage) (any, error) {
		return map[string]string{"method": method, "params": string(params)}, nil
	}
	src := NewInProcessSource("home", "home-box", "", api.NodeCapabilities{SpawnSession: true}, registry.New(), dispatch)

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

type recordingNotifier struct {
	method string
	params any
	calls  int
}

func (r *recordingNotifier) Notify(method string, params any) error {
	r.method, r.params = method, params
	r.calls++
	return nil
}

func TestCompositingNotifierRewritesTasksChangedSessionID(t *testing.T) {
	rec := &recordingNotifier{}
	c := compositingNotifier{inner: rec, nodeID: "home"}

	if err := c.Notify(api.MethodTasksChanged, api.TasksChanged{SubID: "s1", SessionID: "abcd"}); err != nil {
		t.Fatal(err)
	}
	tc, ok := rec.params.(api.TasksChanged)
	if !ok {
		t.Fatalf("params type: %T", rec.params)
	}
	if tc.SessionID != "home:abcd" {
		t.Fatalf("session id not composited: %q", tc.SessionID)
	}
	if tc.SubID != "s1" {
		t.Fatalf("sub id mangled: %q", tc.SubID)
	}
}

func TestCompositingNotifierPassesOtherMethodsThrough(t *testing.T) {
	rec := &recordingNotifier{}
	c := compositingNotifier{inner: rec, nodeID: "home"}
	d := api.TranscriptDelta{SubID: "s1", FromIndex: 3}
	if err := c.Notify(api.MethodTranscriptDelta, d); err != nil {
		t.Fatal(err)
	}
	if got, ok := rec.params.(api.TranscriptDelta); !ok || got.SubID != "s1" || got.FromIndex != 3 {
		t.Fatalf("transcript delta altered: %#v", rec.params)
	}
}

// The in-process source wraps the ctx notifier so a handler that pushes
// tasks.changed with a node-local id emits a composite id to the client.
func TestInProcessSourceCompositesPushedTasksChanged(t *testing.T) {
	dispatch := func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		n, ok := api.NotifierFrom(ctx)
		if !ok {
			t.Fatal("no notifier in dispatch ctx")
		}
		_ = n.Notify(api.MethodTasksChanged, api.TasksChanged{SubID: "s1", SessionID: "abcd"})
		return map[string]bool{"ok": true}, nil
	}
	src := NewInProcessSource("home", "home-box", "", api.NodeCapabilities{}, registry.New(), dispatch)
	rec := &recordingNotifier{}
	ctx := api.WithNotifier(context.Background(), rec)

	if _, err := src.Call(ctx, "transcript.subscribe", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	tc, ok := rec.params.(api.TasksChanged)
	if !ok || tc.SessionID != "home:abcd" {
		t.Fatalf("pushed tasks.changed not composited: %#v", rec.params)
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
	a.AddSource(NewInProcessSource("home", "home-box", "", api.NodeCapabilities{SpawnSession: true}, registry.New(), dispatch))

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
