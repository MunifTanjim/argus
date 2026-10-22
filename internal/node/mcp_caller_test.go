package node

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func mcpNode() *Node {
	list := adapters.All()
	byAgent := make(map[string]adapter.Adapter, len(list))
	for _, a := range list {
		byAgent[a.Agent()] = a
	}
	return &Node{reg: registry.New(), adapterList: list, adapters: byAgent}
}

func rawMeta(kv map[string]string) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	for k, v := range kv {
		b, _ := json.Marshal(v)
		m[k] = b
	}
	return m
}

func TestResolveMCPCallerCodex(t *testing.T) {
	d := mcpNode()
	d.addAgentSession("codex", "t1")
	s, err := d.resolveMCPCaller(t.Context(), "codex", rawMeta(map[string]string{"threadId": "sub", "sessionId": "t1"}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Agent != "codex" || s.AgentSessionID != "t1" {
		t.Fatalf("got %s/%s", s.Agent, s.AgentSessionID)
	}
}

func TestResolveMCPCallerMissingKey(t *testing.T) {
	d := mcpNode()
	d.addAgentSession("codex", "t1")
	_, err := d.resolveMCPCaller(t.Context(), "codex", map[string]json.RawMessage{})
	if !errors.Is(err, api.ErrNoCaller) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveMCPCallerRejectsNonStringAndEmpty(t *testing.T) {
	d := mcpNode()
	d.addAgentSession("codex", "t1")
	for _, raw := range []string{`1`, `null`, `""`, `{}`} {
		_, err := d.resolveMCPCaller(t.Context(), "codex", map[string]json.RawMessage{"sessionId": json.RawMessage(raw)})
		if !errors.Is(err, api.ErrNoCaller) {
			t.Errorf("%s: err = %v", raw, err)
		}
	}
}

func TestResolveMCPCallerUnknownSession(t *testing.T) {
	d := mcpNode()
	_, err := d.resolveMCPCaller(t.Context(), "codex", rawMeta(map[string]string{"sessionId": "nope"}))
	if !errors.Is(err, api.ErrNoCaller) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveMCPCallerUnknownAgent(t *testing.T) {
	d := mcpNode()
	if _, err := d.resolveMCPCaller(t.Context(), "nope", nil); err == nil {
		t.Fatal("expected error")
	}
}

type rootingAdapter struct {
	adapter.Adapter
	roots map[string]string
}

func (rootingAdapter) MCPSessionID(meta map[string]json.RawMessage) (string, error) {
	return adapter.MetaSessionID(meta, "sessionId")
}

func (r rootingAdapter) RootSession(_ context.Context, id string) (string, error) {
	if root, ok := r.roots[id]; ok {
		return root, nil
	}
	return "", errors.New("no such session")
}

func TestResolveMCPCallerRootFallback(t *testing.T) {
	d := mcpNode()
	d.adapters["codex"] = rootingAdapter{Adapter: d.adapters["codex"], roots: map[string]string{"child": "root"}}
	d.addAgentSession("codex", "root")
	s, err := d.resolveMCPCaller(t.Context(), "codex", rawMeta(map[string]string{"sessionId": "child"}))
	if err != nil {
		t.Fatal(err)
	}
	if s.AgentSessionID != "root" {
		t.Fatalf("got %s", s.AgentSessionID)
	}
	_, err = d.resolveMCPCaller(t.Context(), "codex", rawMeta(map[string]string{"sessionId": "orphan"}))
	if !errors.Is(err, api.ErrNoCaller) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveMCPCallerWrongAgentPath(t *testing.T) {
	d := mcpNode()
	d.addAgentSession("opencode", "s1")
	_, err := d.resolveMCPCaller(t.Context(), "codex", rawMeta(map[string]string{"sessionId": "s1"}))
	if !errors.Is(err, api.ErrNoCaller) {
		t.Fatalf("err = %v", err)
	}
}

func claudeMeta(toolUseID string) map[string]json.RawMessage {
	return rawMeta(map[string]string{"claudecode/toolUseId": toolUseID})
}

// announceToolUse runs the PreToolUse hook Claude sends before an argus MCP call.
func announceToolUse(t *testing.T, d *Node, toolUseID, sessionID string) map[string]json.RawMessage {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	raw, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "tool_name": "mcp__plugin_argus_argus__send", "tool_use_id": toolUseID,
	})
	d.adapters["claude"].ProcessHook(registry.New(), adapter.HookEvent{Event: "PreToolUse", Payload: raw})
	return claudeMeta(toolUseID)
}

func TestClaudeCallResolvesThroughToolUse(t *testing.T) {
	d := mcpNode()
	d.addAgentSession("claude", "c1")
	s, err := d.resolveMCPCaller(t.Context(), "claude", announceToolUse(t, d, "tu1", "c1"))
	if err != nil {
		t.Fatal(err)
	}
	if s.AgentSessionID != "c1" {
		t.Fatalf("got %s", s.AgentSessionID)
	}
	if _, err := d.resolveMCPCaller(t.Context(), "claude", claudeMeta("tu1")); !errors.Is(err, api.ErrNoCaller) {
		t.Fatalf("second resolve: err = %v", err)
	}
}

func TestClaudeCallWithoutToolUseFails(t *testing.T) {
	d := mcpNode()
	d.addAgentSession("claude", "c1")
	for _, meta := range []map[string]json.RawMessage{claudeMeta("tu9"), claudeMeta(""), nil} {
		if _, err := d.resolveMCPCaller(t.Context(), "claude", meta); !errors.Is(err, api.ErrNoCaller) {
			t.Fatalf("meta %v: err = %v", meta, err)
		}
	}
}

func TestClaudeCallAfterClearUsesNewID(t *testing.T) {
	d := mcpNode()
	d.addAgentSession("claude", "c1")
	before, _ := d.reg.FindByAgentSession("claude", "c1")
	d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%c1",
		AgentSessionID: "c2", Status: session.StatusIdle,
	})
	old := announceToolUse(t, d, "tu1", "c1")
	s, err := d.resolveMCPCaller(t.Context(), "claude", announceToolUse(t, d, "tu2", "c2"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != before.ID || s.AgentSessionID != "c2" {
		t.Fatalf("got %s/%s, want %s/c2", s.ID, s.AgentSessionID, before.ID)
	}
	if _, err := d.resolveMCPCaller(t.Context(), "claude", old); !errors.Is(err, api.ErrNoCaller) {
		t.Fatalf("pre-clear id resolved: err = %v", err)
	}
}

func TestNonClaudeCallIgnoresToolUse(t *testing.T) {
	d := mcpNode()
	d.addAgentSession("claude", "c1")
	meta := announceToolUse(t, d, "tu1", "c1")
	if _, err := d.resolveMCPCaller(t.Context(), "codex", meta); !errors.Is(err, api.ErrNoCaller) {
		t.Fatalf("err = %v", err)
	}
	if _, err := d.resolveMCPCaller(t.Context(), "claude", meta); err != nil {
		t.Fatalf("codex call consumed the claude entry: %v", err)
	}
}

func TestClaudeCallForUnregisteredSessionFails(t *testing.T) {
	d := mcpNode()
	_, err := d.resolveMCPCaller(t.Context(), "claude", announceToolUse(t, d, "tu1", "ghost"))
	if !errors.Is(err, api.ErrNoCaller) || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("err = %v", err)
	}
}

func TestModPreToolUseAnnouncesCaller(t *testing.T) {
	d, srv := modTestNode(t)
	d.addAgentSession("claude", "c1")
	modPost(t, context.Background(), srv.URL+"/claude/event", map[string]any{
		"event": "PreToolUse",
		"payload": map[string]any{
			"session_id": "c1", "cwd": "/w", "tool_name": "mcp__plugin_argus_argus__send", "tool_use_id": "tu1",
			"tool_input": map[string]any{},
		},
	}, nil)
	if _, err := d.resolveMCPCaller(t.Context(), "claude", claudeMeta("tu1")); err != nil {
		t.Fatal(err)
	}
}
