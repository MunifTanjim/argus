package main

import "testing"

func TestSpawnDetachFlag(t *testing.T) {
	f := newSpawnCmd().Flags().Lookup("detach")
	if f == nil {
		t.Fatal("spawn must register a --detach flag")
	}
	if f.Shorthand != "d" {
		t.Errorf("--detach shorthand = %q, want %q", f.Shorthand, "d")
	}
}

func TestSpawnPassesAgentFlagsThrough(t *testing.T) {
	cmd := newSpawnCmd()
	if err := cmd.ParseFlags([]string{"-d", "claude", "--resume"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if detach, _ := cmd.Flags().GetBool("detach"); !detach {
		t.Error("-d before the agent must set detach")
	}
	if got := cmd.Flags().Args(); len(got) != 2 || got[0] != "claude" || got[1] != "--resume" {
		t.Errorf("passthrough args = %v, want [claude --resume]", got)
	}
}
