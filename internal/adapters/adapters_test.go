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

func TestPiRegistered(t *testing.T) {
	a := ByAgent("pi")
	if a == nil {
		t.Fatal("pi adapter not registered")
	}
	if a.Agent() != "pi" {
		t.Fatalf("Agent() = %q", a.Agent())
	}
}
