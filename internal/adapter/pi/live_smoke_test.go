//go:build pi_live

package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// Run with: go test ./internal/adapter/pi/ -tags pi_live -run TestLiveSmoke -v
// Requires the pi binary on PATH (no model auth needed: spawn with an empty
// prompt, then drive bash + dialogs only).
func TestLiveSmoke(t *testing.T) {
	if _, err := exec.LookPath(piBin); err != nil {
		t.Skipf("pi binary not available: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	useRealRelay(t)
	conn, err := spawnHosted(ctx, t.TempDir(), "smoke-"+newSessionID(), "", "")
	if err != nil {
		t.Fatalf("spawnHosted: %v", err)
	}
	conn.start(nil)
	defer func() {
		_ = conn.stop()
		<-conn.done
		_ = conn.close()
	}()

	data, err := conn.call(ctx, "s1", "get_state", nil)
	if err != nil {
		t.Fatalf("get_state: %v", err)
	}
	t.Logf("state: %s", data)

	bash, err := conn.call(ctx, "b1", "bash", map[string]any{"command": "echo smoke"})
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	t.Logf("bash: %s", bash)

	entries, err := conn.call(ctx, "e1", "get_entries", nil)
	if err != nil {
		t.Fatalf("get_entries: %v", err)
	}
	t.Logf("entries: %s", entries)
}

// TestLiveSupervisedSession drives the whole Tier 1 loop through the Go
// supervisor against the real binary: spawn (with staged gate extension) →
// prompt → settled idle → transcript
// shows the tool result → dismiss removes the card.
func TestLiveSupervisedSession(t *testing.T) {
	if _, err := exec.LookPath(piBin); err != nil {
		t.Skipf("pi binary not available: %v", err)
	}
	useRealRelay(t)
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cwd := t.TempDir()
	id, err := d.spawnSession(ctx, cwd, adapter.SpawnOptions{Prompt: "Run the shell command: echo live-supervised. Then reply OK."})
	if err != nil {
		t.Fatalf("spawnSession: %v", err)
	}

	var sess session.Session

	// Wait for idle.
	idle := false
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); {
		for _, s := range reg.Snapshot() {
			if s.AgentSessionID == id && s.Status == session.StatusAwaitingInput &&
				(s.Interaction == nil || s.Interaction.Kind == session.InteractionIdle) {
				sess, idle = s, true
				break
			}
		}
		if idle {
			break
		}
		time.Sleep(time.Second)
	}
	if !idle {
		t.Fatal("session never settled idle")
	}

	// Transcript shows the tool result. The file appears once conversation
	// exists; fall back to polling briefly if the flush races us.
	var toolResult string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if sess.TranscriptPath != "" {
			if view, err := readTranscriptView(sess.TranscriptPath, false); err == nil {
				for _, e := range view.Entries {
					if e.ToolName == "bash" && e.Result != "" {
						toolResult = e.Result
					}
				}
			}
		} else {
			for _, s := range reg.Snapshot() {
				if s.AgentSessionID == id && s.TranscriptPath != "" {
					sess = s
				}
			}
		}
		if toolResult != "" {
			break
		}
		time.Sleep(time.Second)
	}
	if toolResult != "live-supervised\n" {
		t.Fatalf("tool result = %q", toolResult)
	}

	if err := d.dismiss(ctx, id); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	for _, s := range reg.Snapshot() {
		if s.AgentSessionID == id {
			t.Fatalf("session still listed: %+v", s)
		}
	}
}

// Run with: go test ./internal/adapter/pi/ -tags pi_live -run TestLiveCompletions -v
func TestLiveCompletions(t *testing.T) {
	if _, err := exec.LookPath(piBin); err != nil {
		t.Skipf("pi binary not available: %v", err)
	}
	agentDir := t.TempDir()
	writeFile := func(rel, body string) {
		path := filepath.Join(agentDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("prompts/zz-live.md", "---\ndescription: Live template\n---\nSay hi.\n")
	writeFile("skills/zz-skill/SKILL.md", "---\nname: zz-skill\ndescription: Live skill\n---\nDo nothing.\n")
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)

	useRealRelay(t)
	d := newDiscoverer(registry.New(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	id, err := d.spawnSession(ctx, t.TempDir(), adapter.SpawnOptions{})
	if err != nil {
		t.Fatalf("spawnSession: %v", err)
	}
	got, err := d.completions(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("completions: %+v", got)
	want := map[string]string{"compact": adapter.CompactCompletion.Description, "new": newCompletion.Description, "zz-live": "Live template", "skill:zz-skill": "Live skill"}
	for _, it := range got {
		if desc, ok := want[it.Name]; ok && desc == it.Description {
			delete(want, it.Name)
		}
	}
	if len(want) > 0 {
		t.Fatalf("missing or wrong: %v", want)
	}
}

// Run with: go test ./internal/adapter/pi/ -tags pi_live -run TestLiveUsage -v
func TestLiveUsage(t *testing.T) {
	if _, err := exec.LookPath(piBin); err != nil {
		t.Skipf("pi binary not available: %v", err)
	}
	useRealRelay(t)
	d := newDiscoverer(registry.New(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	id, err := d.spawnSession(ctx, t.TempDir(), adapter.SpawnOptions{})
	if err != nil {
		t.Fatalf("spawnSession: %v", err)
	}
	p := d.get(id)
	if p == nil {
		t.Fatal("no session")
	}
	if _, err := d.loadState(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	u := p.usage
	p.mu.Unlock()
	if u == nil || !u.hasCost {
		t.Fatalf("usage = %+v, want pi's report with a cost", u)
	}
	t.Logf("usage: %+v", *u)
}

// Run with: go test ./internal/adapter/pi/ -tags pi_live -run TestLiveAgentTools -v
// Real pi with a stamped gate calls a fake Argus MCP server through a fake
// model; each tools/call must name the live session, also after /new.
func TestLiveAgentTools(t *testing.T) {
	if _, err := exec.LookPath(piBin); err != nil {
		t.Skipf("pi binary not available: %v", err)
	}
	token := strings.Repeat("b", 64)
	var mu sync.Mutex
	var callers []string
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": req.Params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "argus", "version": "test"}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "whoami", "description": "Report the caller", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			mu.Lock()
			callers = append(callers, r.Header.Get("X-Argus-Session"))
			mu.Unlock()
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer mcp.Close()

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		delta := map[string]any{"role": "assistant", "content": "done"}
		finish := "stop"
		if n := len(req.Messages); n > 0 && req.Messages[n-1].Role != "tool" {
			delta = map[string]any{"role": "assistant", "tool_calls": []map[string]any{{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "mcp__argus__whoami", "arguments": "{}"}}}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ch := range []map[string]any{
			{"choices": []map[string]any{{"index": 0, "delta": delta}}},
			{"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}},
		} {
			ch["id"], ch["object"], ch["model"] = "x", "chat.completion.chunk", "fake-1"
			b, _ := json.Marshal(ch)
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()

	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	models := fmt.Sprintf(`{"providers":{"fake":{"baseUrl":%q,"api":"openai-completions","apiKey":"x","models":[{"id":"fake-1","contextWindow":100000}]}}}`, model.URL+"/v1")
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(dir, "gate.ts")
	if err := os.WriteFile(gate, []byte(stampGate(gateStamp{mcpURL: mcp.URL + "/mcp/pi", mcpToken: token})), 0o600); err != nil {
		t.Fatal(err)
	}

	// run drives one owned pi process whose node serves ownerURL, and returns
	// the session ids before and after /new with the callers the server saw.
	run := func(t *testing.T, ownerURL string) (string, string, []string) {
		mu.Lock()
		callers = nil
		mu.Unlock()
		cmd := exec.Command(piBin, "--mode", "rpc", "--model", "fake/fake-1", "-e", gate)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "PI_CODING_AGENT_DIR="+agentDir, "ARGUS_MANAGED=1", "ARGUS_MCP_URL="+ownerURL)
		stdin, _ := cmd.StdinPipe()
		stdout, _ := cmd.StdoutPipe()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
		lines := make(chan map[string]any, 256)
		go func() {
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 1<<20), 16<<20)
			for sc.Scan() {
				var m map[string]any
				if json.Unmarshal(sc.Bytes(), &m) == nil {
					lines <- m
				}
			}
			close(lines)
		}()
		next := func(match func(map[string]any) bool) map[string]any {
			t.Helper()
			deadline := time.After(60 * time.Second)
			for {
				select {
				case m, ok := <-lines:
					if !ok {
						t.Fatal("pi exited")
					}
					if match(m) {
						return m
					}
				case <-deadline:
					t.Fatal("timed out")
				}
			}
		}
		n := 0
		call := func(typ string) map[string]any {
			t.Helper()
			n++
			id := fmt.Sprintf("r%d", n)
			rec := map[string]any{"id": id, "type": typ}
			if typ == "prompt" {
				rec["message"] = "go"
			}
			b, _ := json.Marshal(rec)
			if _, err := stdin.Write(append(b, '\n')); err != nil {
				t.Fatal(err)
			}
			return next(func(m map[string]any) bool { return m["type"] == "response" && m["id"] == id })
		}
		sessionID := func() string {
			data, _ := call("get_state")["data"].(map[string]any)
			id, _ := data["sessionId"].(string)
			return id
		}
		turn := func() {
			call("prompt")
			next(func(m map[string]any) bool { return m["type"] == "agent_end" })
		}

		first := sessionID()
		turn()
		call("new_session")
		second := sessionID()
		turn()
		mu.Lock()
		defer mu.Unlock()
		return first, second, append([]string(nil), callers...)
	}
	t.Run("owner", func(t *testing.T) {
		first, second, got := run(t, mcp.URL+"/mcp/pi")
		if first == second || len(got) != 2 || got[0] != first || got[1] != second {
			t.Fatalf("callers = %v, want [%s %s]", got, first, second)
		}
	})
	t.Run("another node", func(t *testing.T) {
		if _, _, got := run(t, "http://127.0.0.1:1/mcp/pi"); len(got) != 0 {
			t.Fatalf("callers = %v, want none: the gate carries another node's endpoint", got)
		}
	})
}
