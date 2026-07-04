package main

import "testing"

func TestCaptureEnv(t *testing.T) {
	t.Setenv("ANTIGRAVITY_CONVERSATION_ID", "conv-123")

	got := captureEnv([]string{"ANTIGRAVITY_CONVERSATION_ID", "UNSET_HOOK_VAR"})
	if got["ANTIGRAVITY_CONVERSATION_ID"] != "conv-123" {
		t.Fatalf("captured wrong values: %#v", got)
	}
	if _, ok := got["UNSET_HOOK_VAR"]; ok {
		t.Fatalf("unset var must be absent, got %#v", got)
	}
}
