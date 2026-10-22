package adapters

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
)

func TestAntigravityRegistered(t *testing.T) {
	a := ByAgent("antigravity")
	if a == nil {
		t.Fatal("antigravity adapter not registered")
	}
	if a.Agent() != "antigravity" {
		t.Fatalf("Agent() = %q", a.Agent())
	}
}

func TestOrchestratedAgentsImplementIdentity(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "opencode"} {
		a := ByAgent(agent)
		if a == nil {
			t.Fatalf("%s not registered", agent)
		}
		if _, ok := a.(adapter.CallerIdentity); !ok {
			t.Errorf("%s does not implement CallerIdentity", agent)
		}
		if _, ok := a.(adapter.MCPIdentity); !ok {
			t.Errorf("%s does not implement MCPIdentity", agent)
		}
	}
	a := ByAgent("antigravity")
	if _, ok := a.(adapter.CallerIdentity); ok {
		t.Error("antigravity implements CallerIdentity")
	}
	if _, ok := a.(adapter.MCPIdentity); ok {
		t.Error("antigravity implements MCPIdentity")
	}
}
