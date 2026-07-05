package adapters

import "testing"

func TestAntigravityRegistered(t *testing.T) {
	a := ByAgent("antigravity")
	if a == nil {
		t.Fatal("antigravity adapter not registered")
	}
	if a.Agent() != "antigravity" {
		t.Fatalf("Agent() = %q", a.Agent())
	}
}
