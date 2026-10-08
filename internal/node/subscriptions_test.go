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
	"github.com/MunifTanjim/argus/internal/transcript"
)

// TestSubscribeWorksWithoutRegisterConn proves the gateway case: handleTranscriptSubscribe
// must succeed when the connection Notifier was never pre-registered via OnConnect
// (the uplink and in-process gateway paths). It also confirms that cancelling the ctx
// tears the subscription's poller down.
func TestSubscribeWorksWithoutRegisterConn(t *testing.T) {
	d := newNode(nil)
	tmp := writeTempTranscript(t)

	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		AgentSessionID: "node1",
		TranscriptPath: tmp,
		Status:         session.StatusIdle,
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
	t.Logf("initial delta: SubID=%q FromIndex=%d Entries=%d", delta.SubID, delta.FromIndex, len(delta.Entries))

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
	case <-time.After(10 * time.Second):
		t.Fatal("no delta notification after append (waited 10s)")
	}

	// Cancel the ctx; the lazy goroutine should call dropConn and stop the poller.
	cancel()
	deadline := time.Now().Add(10 * time.Second)
	for {
		d.subsMu.Lock()
		_, stillRegistered := d.conns[fn]
		d.subsMu.Unlock()
		if !stillRegistered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ctx cancel should have removed the lazily-registered conn via dropConn")
		}
		time.Sleep(10 * time.Millisecond)
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

func TestDiffEntries(t *testing.T) {
	e := func(id string) transcript.Entry { return transcript.Entry{ID: id, Kind: transcript.EntryText} }
	a := []transcript.Entry{e("0"), e("1")}
	if from, changed := diffEntries(a, a); changed {
		t.Fatalf("equal slices changed=%v from=%d", changed, from)
	}
	b := []transcript.Entry{e("0"), e("1"), e("2")}
	if from, changed := diffEntries(a, b); !changed || from != 2 {
		t.Fatalf("append: from=%d changed=%v, want 2,true", from, changed)
	}
	g := e("1")
	g.Text = "grown"
	c := []transcript.Entry{e("0"), g}
	if from, changed := diffEntries(a, c); !changed || from != 1 {
		t.Fatalf("mutate: from=%d changed=%v, want 1,true", from, changed)
	}
}

func TestDiffEntriesAppendedFooter(t *testing.T) {
	old := []transcript.Entry{
		{ID: "0", Kind: transcript.EntryUser, Text: "go"},
		{ID: "1.0", Kind: transcript.EntryText, Text: "checking"},
	}
	cur := append(append([]transcript.Entry{}, old...),
		transcript.Entry{ID: "1.end", Kind: transcript.EntryTurnEnd})
	from, changed := diffEntries(old, cur)
	if !changed || from != 2 {
		t.Fatalf("diffEntries = (%d, %v), want (2, true)", from, changed)
	}
}

func TestDiffEntriesGrownTail(t *testing.T) {
	old := []transcript.Entry{{ID: "1.1", Kind: transcript.EntryTool, ToolID: "t"}}
	cur := []transcript.Entry{{ID: "1.1", Kind: transcript.EntryTool, ToolID: "t", Result: "ok"}}
	if from, changed := diffEntries(old, cur); !changed || from != 0 {
		t.Fatalf("diffEntries = (%d, %v), want (0, true)", from, changed)
	}
}

func TestLastTurnInterrupted(t *testing.T) {
	cases := []struct {
		name string
		es   []transcript.Entry
		want bool
	}{
		{"empty", nil, false},
		{"interrupted footer", []transcript.Entry{{Kind: transcript.EntryTurnEnd, Interrupted: true}}, true},
		{"plain footer", []transcript.Entry{{Kind: transcript.EntryTurnEnd}}, false},
		{"prompt after", []transcript.Entry{{Kind: transcript.EntryTurnEnd, Interrupted: true}, {Kind: transcript.EntryUser}}, false},
	}
	for _, c := range cases {
		if got := lastTurnInterrupted(c.es); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
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

	// Confirm the initial fold yields ≥1 entry so the append produces a detectable delta.
	initial, err := claudecode.ReadStreamingView(tmp)
	if err != nil {
		t.Fatalf("ReadStreamingView on fixture: %v", err)
	}
	if len(initial) == 0 {
		t.Fatal("fixture produced 0 entries — cannot detect a delta; check JSONL format")
	}
	t.Logf("initial fold: %d entries", len(initial))

	// Insert a session pointing at the temp transcript.
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		AgentSessionID: "c1",
		TranscriptPath: tmp,
		Status:         session.StatusIdle,
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
	t.Logf("initial delta: SubID=%q FromIndex=%d Entries=%d", delta.SubID, delta.FromIndex, len(delta.Entries))

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
		t.Logf("delta push: SubID=%q FromIndex=%d Entries=%d", got.SubID, got.FromIndex, len(got.Entries))
		if got.SubID != "x" {
			t.Errorf("sub_id = %q, want x", got.SubID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no delta notification received after append (waited 10s)")
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

func TestSurfaceIdleAfterInterrupt(t *testing.T) {
	d := newNode(nil)
	reg := d.Registry()
	reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Status: session.StatusAwaitingInput,
		Interaction: &session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{{Question: "Pick"}}},
	})
	id := reg.Snapshot()[0].ID

	// An interrupted turn surfaces the idle composer.
	d.surfaceIdleAfterInterrupt(id)
	if got, _ := reg.Get(id); got.Interaction == nil || got.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("interrupt should surface the idle composer, got %+v", got.Interaction)
	}

	// Idempotent once idle: a further interrupt signal is a no-op.
	d.surfaceIdleAfterInterrupt(id)
	if got, _ := reg.Get(id); got.Interaction == nil || got.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("idle composer must remain, got %+v", got.Interaction)
	}
}

// A native terminal interrupt clears the parked prompt to working (interaction
// nil). The poll must still surface the idle composer once the transcript shows
// the interrupt.
func TestSurfaceIdleAfterInterruptClearedToWorking(t *testing.T) {
	d := newNode(nil)
	reg := d.Registry()
	reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Status: session.StatusWorking,
	})
	id := reg.Snapshot()[0].ID
	if got, _ := reg.Get(id); got.Interaction != nil {
		t.Fatalf("precondition: want nil interaction, got %+v", got.Interaction)
	}

	d.surfaceIdleAfterInterrupt(id)
	got, _ := reg.Get(id)
	if got.Interaction == nil || got.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("interrupt should surface the idle composer from working, got %+v", got.Interaction)
	}
	if got.Status != session.StatusIdle {
		t.Errorf("status: got %q want idle", got.Status)
	}
}

func TestClampFrom(t *testing.T) {
	k := func(kind transcript.EntryKind) transcript.Entry { return transcript.Entry{Kind: kind} }
	open := []transcript.Entry{
		k(transcript.EntryUser), k(transcript.EntryText), k(transcript.EntryTurnEnd),
		k(transcript.EntryUser), k(transcript.EntryThinking), k(transcript.EntryTool), k(transcript.EntryText),
	}
	finished := append(append([]transcript.Entry{}, open...), k(transcript.EntryTurnEnd))
	// A prompt queued while the agent was busy lands inside the open turn; the
	// spawn before it can still resolve.
	queued := []transcript.Entry{
		k(transcript.EntryUser), k(transcript.EntryText), k(transcript.EntryTurnEnd),
		k(transcript.EntryUser), k(transcript.EntrySubagent), k(transcript.EntryUser),
	}
	cases := []struct {
		name    string
		entries []transcript.Entry
		have    int
		want    int
	}{
		{"mid-turn resends from turn start", open, 7, 3},
		{"mid-turn partial cache", open, 6, 3},
		{"cache ends on user entry", open, 4, 3},
		{"queued prompt inside the turn is not a boundary", queued, 6, 3},
		{"finished resends last entry", finished, 8, 7},
		{"first turn open", open[:2], 2, 0},
		{"no boundary at all", []transcript.Entry{k(transcript.EntryText), k(transcript.EntryTool)}, 2, 0},
		{"have zero", open, 0, 0},
		{"have beyond len", open, 20, 3},
		{"empty", nil, 3, 0},
	}
	for _, c := range cases {
		if got := clampFrom(c.entries, c.have); got != c.want {
			t.Errorf("%s: clampFrom(have=%d) = %d, want %d", c.name, c.have, got, c.want)
		}
	}
}
