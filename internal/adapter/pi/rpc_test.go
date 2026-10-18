package pi

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// fakePi speaks enough RPC to test the client: it answers get_state and bash,
// emits an event before one response (out-of-order interleave, like real pi),
// fails fail_me, and records extension_ui_response records.
type fakePi struct {
	t *testing.T

	state      map[string]any // extra get_state fields
	spawnModel string
	// promptDisposition answers prompt ("started" when empty); with
	// promptRelease set, the answer waits for it, as a command's does for
	// its dialogs.
	promptDisposition string
	promptRelease     chan struct{}

	mu      sync.Mutex
	gotUI   []map[string]any
	prompts []map[string]any
	calls   []map[string]any // other commands, answered with success
}

func (f *fakePi) run(stdin io.Reader, stdout io.WriteCloser) {
	defer stdout.Close()
	dec := json.NewDecoder(stdin)
	var encMu sync.Mutex
	raw := json.NewEncoder(stdout)
	enc := struct{ Encode func(any) error }{func(v any) error {
		encMu.Lock()
		defer encMu.Unlock()
		return raw.Encode(v)
	}}
	for {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			return
		}
		switch rec["type"] {
		case "get_state":
			// Interleave an event before the response, like real pi does.
			enc.Encode(map[string]any{"type": "session_info_changed", "name": "x"})
			data := map[string]any{"sessionId": "ses-1", "sessionFile": "/tmp/ses-1.jsonl"}
			f.mu.Lock()
			for k, v := range f.state {
				data[k] = v
			}
			f.mu.Unlock()
			enc.Encode(map[string]any{"id": rec["id"], "type": "response", "command": "get_state", "success": true,
				"data": data})
		case "bash":
			enc.Encode(map[string]any{"type": "bash_execution_update", "id": rec["id"], "delta": "hi\n"})
			enc.Encode(map[string]any{"id": rec["id"], "type": "response", "command": "bash", "success": true,
				"data": map[string]any{"output": "hi\n", "exitCode": 0}})
		case "fail_me":
			enc.Encode(map[string]any{"id": rec["id"], "type": "response", "command": "fail_me", "success": false, "error": "nope"})
		case "prompt":
			f.mu.Lock()
			f.prompts = append(f.prompts, rec)
			f.mu.Unlock()
			disposition := f.promptDisposition
			if disposition == "" {
				disposition = "started"
			}
			resp := map[string]any{"id": rec["id"], "type": "response", "command": "prompt", "success": true,
				"data": map[string]any{"disposition": disposition}}
			if f.promptRelease == nil {
				enc.Encode(resp)
				break
			}
			go func() {
				<-f.promptRelease
				enc.Encode(resp)
			}()
		case "extension_ui_response":
			f.mu.Lock()
			f.gotUI = append(f.gotUI, rec)
			f.mu.Unlock()
		default:
			f.mu.Lock()
			f.calls = append(f.calls, rec)
			f.mu.Unlock()
			enc.Encode(map[string]any{"id": rec["id"], "type": "response", "command": rec["type"], "success": true})
		}
	}
}

// call returns the last received command of type typ, or nil.
func (f *fakePi) call(typ string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i]["type"] == typ {
			return f.calls[i]
		}
	}
	return nil
}

func (f *fakePi) uiResponses() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.gotUI...)
}

// waitUIResponses waits until the fake has decoded n ui responses: send
// returns once the bytes are written, before the fake records them.
func (f *fakePi) waitUIResponses(n int) []map[string]any {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := f.uiResponses(); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return f.uiResponses()
}

func dialFake(t *testing.T, onEvent func(typ string, raw json.RawMessage)) (*rpcConn, *fakePi) {
	t.Helper()
	fakeStdin, clientStdin := io.Pipe()   // client writes, fake reads
	clientStdout, fakeStdout := io.Pipe() // fake writes, client reads
	f := &fakePi{t: t}
	go f.run(fakeStdin, fakeStdout)
	conn := newRPCConn(clientStdin, clientStdout)
	conn.start(onEvent)
	t.Cleanup(func() { conn.close() })
	return conn, f
}

func TestCallReturnsData(t *testing.T) {
	conn, _ := dialFake(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := conn.call(ctx, "1", "get_state", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var st struct {
		SessionID   string `json:"sessionId"`
		SessionFile string `json:"sessionFile"`
	}
	if json.Unmarshal(data, &st) != nil || st.SessionID != "ses-1" || st.SessionFile != "/tmp/ses-1.jsonl" {
		t.Fatalf("state = %s", data)
	}
}

func TestCallSurfacesError(t *testing.T) {
	conn, _ := dialFake(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := conn.call(ctx, "1", "fail_me", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestEventsDeliveredAroundResponses(t *testing.T) {
	var mu sync.Mutex
	var events []string
	conn, _ := dialFake(t, func(_ string, raw json.RawMessage) {
		var p struct {
			Type string `json:"type"`
		}
		json.Unmarshal(raw, &p)
		mu.Lock()
		events = append(events, p.Type)
		mu.Unlock()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// get_state emits session_info_changed before its response; bash emits a
	// bash_execution_update before its response. Neither may be swallowed or
	// mistaken for a command response.
	if _, err := conn.call(ctx, "1", "get_state", nil); err != nil {
		t.Fatalf("get_state: %v", err)
	}
	if _, err := conn.call(ctx, "b1", "bash", map[string]any{"command": "echo hi"}); err != nil {
		t.Fatalf("bash: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0] != "session_info_changed" || events[1] != "bash_execution_update" {
		t.Fatalf("events = %v", events)
	}
}

func TestSendUIResponse(t *testing.T) {
	conn, f := dialFake(t, nil)
	if err := conn.send(map[string]any{"type": "extension_ui_response", "id": "u1", "value": "Allow"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := f.waitUIResponses(1)
	if len(got) != 1 || got[0]["id"] != "u1" || got[0]["value"] != "Allow" {
		t.Fatalf("ui responses = %v", got)
	}
}

func TestConcurrentCalls(t *testing.T) {
	conn, _ := dialFake(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = conn.call(ctx, string(rune('a'+i)), "get_state", nil)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

func TestParseModelList(t *testing.T) {
	out := "provider    model                    context  max-out  thinking  images\n" +
		"anthropic   claude-sonnet-4-5        200K     64K      yes       yes\n" +
		"openrouter  liquid/lfm-2.5-2.6b:free 32K      4K       no        no\n"
	got := parseModelList([]byte(out))
	want := []api.ModelInfo{
		{ID: "anthropic/claude-sonnet-4-5", Name: "claude-sonnet-4-5 · anthropic"},
		{ID: "openrouter/liquid/lfm-2.5-2.6b:free", Name: "liquid/lfm-2.5-2.6b:free · openrouter"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %+v", got)
	}
	if got := parseModelList([]byte("No models available. Use /login ...\n")); got != nil {
		t.Fatalf("guidance parsed as models: %+v", got)
	}
}

func TestModelArgs(t *testing.T) {
	cases := map[string][]string{
		"":                                    nil,
		"sonnet":                              {"--model", "sonnet"},
		"anthropic/claude-sonnet-4-5":         {"--provider", "anthropic", "--model", "claude-sonnet-4-5"},
		"openrouter/liquid/lfm-2.5-2.6b:free": {"--provider", "openrouter", "--model", "liquid/lfm-2.5-2.6b:free"},
	}
	for in, want := range cases {
		if got := modelArgs(in); !reflect.DeepEqual(got, want) {
			t.Errorf("modelArgs(%q) = %v, want %v", in, got, want)
		}
	}
}
