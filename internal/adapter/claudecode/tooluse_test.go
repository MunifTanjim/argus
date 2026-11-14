package claudecode

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
)

func TestToolUseMapPutTake(t *testing.T) {
	m := &toolUseMap{}
	now := time.Now()
	m.put("tu1", "c1", now)
	if id, ok := m.take("tu1", now); !ok || id != "c1" {
		t.Fatalf("take = %q, %v", id, ok)
	}
	if _, ok := m.take("tu1", now); ok {
		t.Fatal("second take found the entry")
	}
}

func TestToolUseMapExpires(t *testing.T) {
	m := &toolUseMap{}
	t0 := time.Now()
	m.put("tu1", "c1", t0)
	if _, ok := m.take("tu1", t0.Add(toolUseTTL+time.Minute)); ok {
		t.Fatal("expired entry was returned")
	}
}

func TestToolUseMapOutlivesSlowApproval(t *testing.T) {
	m := &toolUseMap{}
	t0 := time.Now()
	m.put("tu1", "c1", t0)
	if id, ok := m.take("tu1", t0.Add(30*time.Minute)); !ok || id != "c1" {
		t.Fatalf("take after a 30 minute prompt = %q, %v", id, ok)
	}
}

func TestToolUseMapSweepIsThrottled(t *testing.T) {
	m := &toolUseMap{}
	t0 := time.Now()
	m.put("old", "c1", t0)
	late := t0.Add(toolUseTTL + time.Minute)
	m.put("a", "c1", late)
	if _, ok := m.entries["old"]; ok {
		t.Fatal("first put after the gap did not sweep")
	}
	m.put("old2", "c1", t0)
	m.put("b", "c1", late.Add(time.Second))
	if _, ok := m.entries["old2"]; !ok {
		t.Fatal("put within the gap swept")
	}
	m.put("c", "c1", late.Add(toolUseSweepGap+time.Second))
	if _, ok := m.entries["old2"]; ok {
		t.Fatal("put after the next gap did not sweep")
	}
}

func preToolUse(a *ccAdapter, tool, toolUseID, sessionID string) {
	raw, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "cwd": "/w", "tool_name": tool, "tool_use_id": toolUseID,
		"tool_input": map[string]any{},
	})
	a.ProcessHook(registry.New(), HookEvent{Event: "PreToolUse", Payload: raw})
}

func toolUseMetaFor(toolUseID string) map[string]json.RawMessage {
	b, _ := json.Marshal(toolUseID)
	return map[string]json.RawMessage{toolUseMeta: b}
}

func TestPreToolUseRecordsArgusTools(t *testing.T) {
	a := New().(*ccAdapter)
	preToolUse(a, mcpToolPrefix+"send", "tu1", "c1")
	if id, err := a.MCPSessionID(toolUseMetaFor("tu1")); err != nil || id != "c1" {
		t.Fatalf("MCPSessionID = %q, %v", id, err)
	}
	_, err := a.MCPSessionID(toolUseMetaFor("tu1"))
	if !errors.Is(err, api.ErrNoCaller) || !strings.Contains(err.Error(), "no PreToolUse hook announced tool use tu1") {
		t.Fatalf("second resolve err = %v", err)
	}
}

func TestPreToolUseIgnoresOtherTools(t *testing.T) {
	a := New().(*ccAdapter)
	preToolUse(a, "Bash", "tu1", "c1")
	if _, err := a.MCPSessionID(toolUseMetaFor("tu1")); err == nil {
		t.Fatal("non-argus tool was recorded")
	}
}

func TestPreToolUseIgnoresEmptyToolUseID(t *testing.T) {
	a := New().(*ccAdapter)
	preToolUse(a, mcpToolPrefix+"send", "", "c1")
	if n := len(a.toolUses.entries); n != 0 {
		t.Fatalf("entries = %d", n)
	}
	_, err := a.MCPSessionID(toolUseMetaFor(""))
	if !errors.Is(err, api.ErrNoCaller) || !strings.Contains(err.Error(), "carries no "+toolUseMeta) {
		t.Fatalf("err = %v", err)
	}
}
