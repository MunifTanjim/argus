package opencode_test

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/opencode"
)

func TestAdapterSatisfiesInterface(t *testing.T) {
	a := opencode.New()
	if a.Agent() != "opencode" {
		t.Fatalf("agent = %q", a.Agent())
	}
	if a.AgentName() != "OpenCode" {
		t.Fatalf("name = %q", a.AgentName())
	}
	name, args, ok := a.ResumeCommand("ses_1")
	if !ok || name != "opencode" || len(args) != 2 || args[0] != "--session" || args[1] != "ses_1" {
		t.Fatalf("resume = %q %v %v", name, args, ok)
	}
	if _, ok := a.(adapter.Responder); !ok {
		t.Fatal("adapter must implement Responder")
	}
}
