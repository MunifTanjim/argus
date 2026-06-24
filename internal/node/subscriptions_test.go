package node

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// TestSubscribeWorksWithoutRegisterConn proves the gateway case: handleTranscriptSubscribe
// must succeed when the connection Notifier was never pre-registered via OnConnect
// (the uplink and in-process gateway paths). It also confirms that cancelling the ctx
// tears the subscription's poller down.
func TestSubscribeWorksWithoutRegisterConn(t *testing.T) {
	d := newNode(nil)
	tmp := writeTempTranscript(t)

	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		ClaudeSessionID: "node1",
		TranscriptPath:  tmp,
		Status:          session.StatusIdle,
	})

	fn := &fakeNotifier{ch: make(chan api.Notification, 8)}
	// Intentionally do NOT call d.registerConn(fn) — this is the gateway path.

	ctx, cancel := context.WithCancel(context.Background())
	ctx = api.WithNotifier(ctx, fn)

	res, err := d.handleTranscriptSubscribe(ctx, mustJSON(api.TranscriptSubscribeParams{
		SubID:     "gateway-sub",
		SessionID: s.ID,
	}))
	if err != nil {
		t.Fatalf("handleTranscriptSubscribe without registerConn: %v", err)
	}
	delta, ok := res.(api.TranscriptDelta)
	if !ok {
		t.Fatalf("result is %T, want TranscriptDelta", res)
	}
	t.Logf("initial delta: SubID=%q FromIndex=%d Chunks=%d", delta.SubID, delta.FromIndex, len(delta.Chunks))

	// Verify the conn was lazily registered.
	d.subsMu.Lock()
	_, registered := d.conns[fn]
	d.subsMu.Unlock()
	if !registered {
		t.Fatal("getOrCreateConnSubs should have lazily registered the notifier")
	}

	// Append to trigger a delta push so the poller is proven running.
	appendTranscriptLine(t, tmp)
	select {
	case n := <-fn.ch:
		if n.Method != api.MethodTranscriptDelta {
			t.Fatalf("method = %q, want %q", n.Method, api.MethodTranscriptDelta)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no delta notification after append (waited 3s)")
	}

	// Cancel the ctx; the lazy goroutine should call dropConn and stop the poller.
	cancel()
	time.Sleep(150 * time.Millisecond) // let the goroutine run

	d.subsMu.Lock()
	_, stillRegistered := d.conns[fn]
	d.subsMu.Unlock()
	if stillRegistered {
		t.Fatal("ctx cancel should have removed the lazily-registered conn via dropConn")
	}

	// Drain any in-flight notification.
	for {
		select {
		case <-fn.ch:
		default:
			goto drained2
		}
	}
drained2:
	// After cancel the poller must be gone; a new append must not produce a notification.
	appendTranscriptLine(t, tmp)
	select {
	case n := <-fn.ch:
		t.Errorf("received notification after ctx cancel: method=%q", n.Method)
	case <-time.After(2 * time.Second):
		// expected: poller torn down
	}
}

func TestDiffChunks(t *testing.T) {
	a := []claudecode.Chunk{{ID: "0"}, {ID: "1"}}
	// no change
	if from, changed := diffChunks(a, a); changed {
		t.Fatalf("equal slices changed=%v from=%d", changed, from)
	}
	// appended chunk -> from = len(old)
	b := []claudecode.Chunk{{ID: "0"}, {ID: "1"}, {ID: "2"}}
	if from, changed := diffChunks(a, b); !changed || from != 2 {
		t.Fatalf("append: from=%d changed=%v, want 2,true", from, changed)
	}
	// last chunk mutated -> from = index of last
	c := []claudecode.Chunk{{ID: "0"}, {ID: "1", Text: "grown"}}
	if from, changed := diffChunks(a, c); !changed || from != 1 {
		t.Fatalf("mutate: from=%d changed=%v, want 1,true", from, changed)
	}
}

// fakeNotifier is a test double for api.Notifier that records Notify calls.
type fakeNotifier struct{ ch chan api.Notification }

func (f *fakeNotifier) Notify(method string, params any) error {
	b, _ := json.Marshal(params)
	f.ch <- api.Notification{Method: method, Params: b}
	return nil
}

// writeTempTranscript writes a minimal valid JSONL transcript (1 user + 1 AI
// line) to a temp file and returns its path. The format matches what
// claudecode.ReadStreamingView / parser.ReadSession expects.
func writeTempTranscript(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "transcript-*.jsonl")
	if err != nil {
		t.Fatalf("create temp transcript: %v", err)
	}
	content := `{"type":"user","uuid":"u1","timestamp":"2026-06-12T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-06-12T10:00:01Z","message":{"role":"assistant","model":"claude-opus-4-5","stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":10},"content":[{"type":"text","text":"hi there"}]}}
`
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close transcript: %v", err)
	}
	return f.Name()
}

// appendTranscriptLine appends another user message to the transcript file.
func appendTranscriptLine(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open transcript for append: %v", err)
	}
	defer f.Close()
	line := `{"type":"user","uuid":"u2","timestamp":"2026-06-12T10:01:00Z","message":{"role":"user","content":[{"type":"text","text":"another message"}]}}` + "\n"
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("append transcript line: %v", err)
	}
}

// mustJSON marshals v to JSON for use as RPC params.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestSubscribePushesDeltaOnAppend(t *testing.T) {
	d := newNode(nil)
	// d.conns is already initialized by newNode (after our node.go change);
	// confirm it was registered in OnConnect by also pre-registering via registerConn.

	tmp := writeTempTranscript(t)

	// Confirm the initial fold yields ≥1 chunk so the append produces a detectable delta.
	initial, err := claudecode.ReadStreamingView(tmp)
	if err != nil {
		t.Fatalf("ReadStreamingView on fixture: %v", err)
	}
	if len(initial) == 0 {
		t.Fatal("fixture produced 0 chunks — cannot detect a delta; check JSONL format")
	}
	t.Logf("initial fold: %d chunks", len(initial))

	// Insert a session pointing at the temp transcript.
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		ClaudeSessionID: "c1",
		TranscriptPath:  tmp,
		Status:          session.StatusIdle,
	})

	fn := &fakeNotifier{ch: make(chan api.Notification, 8)}
	d.registerConn(fn)
	ctx := api.WithNotifier(context.Background(), fn)

	res, err := d.handleTranscriptSubscribe(ctx, mustJSON(api.TranscriptSubscribeParams{
		SubID:     "x",
		SessionID: s.ID,
	}))
	if err != nil {
		t.Fatalf("handleTranscriptSubscribe: %v", err)
	}
	delta, ok := res.(api.TranscriptDelta)
	if !ok {
		t.Fatalf("result is %T, want TranscriptDelta", res)
	}
	t.Logf("initial delta: SubID=%q FromIndex=%d Chunks=%d", delta.SubID, delta.FromIndex, len(delta.Chunks))

	// Append a new line to trigger a delta push from the poller.
	appendTranscriptLine(t, tmp)

	select {
	case n := <-fn.ch:
		if n.Method != api.MethodTranscriptDelta {
			t.Fatalf("method = %q, want %q", n.Method, api.MethodTranscriptDelta)
		}
		var got api.TranscriptDelta
		if err := json.Unmarshal(n.Params, &got); err != nil {
			t.Fatalf("unmarshal delta: %v", err)
		}
		t.Logf("delta push: SubID=%q FromIndex=%d Chunks=%d", got.SubID, got.FromIndex, len(got.Chunks))
		if got.SubID != "x" {
			t.Errorf("sub_id = %q, want x", got.SubID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no delta notification received after append (waited 3s)")
	}

	// dropConn must stop the poller; drain the channel and confirm no more arrive.
	d.dropConn(fn)
	time.Sleep(100 * time.Millisecond) // let any in-flight tick complete
	// drain
	for {
		select {
		case <-fn.ch:
		default:
			goto drained
		}
	}
drained:
	// Append again; after dropConn the poller must be gone so no notification.
	appendTranscriptLine(t, tmp)
	select {
	case n := <-fn.ch:
		t.Errorf("received notification after dropConn: method=%q", n.Method)
	case <-time.After(2 * time.Second):
		// expected: poller is gone
	}
}
