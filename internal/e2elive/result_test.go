package e2elive

import (
	"slices"
	"testing"
)

func TestBuildLockArgsDefaultsWhenCallerSuppliesNothing(t *testing.T) {
	got := buildLockArgs([]string{"status"}, "/sock", "ws://gw", "tok")
	want := []string{"lock", "status", "--socket=/sock", "--gateway=ws://gw", "--token=tok"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBuildLockArgsDoesNotAppendWhenCallerSuppliesEqualForm(t *testing.T) {
	args := []string{"status", "--token=wrong-token"}
	got := buildLockArgs(args, "/sock", "ws://gw", "tok")
	for _, a := range got {
		if a == "--token=tok" {
			t.Fatalf("harness token was appended even though caller supplied --token: %v", got)
		}
	}
}

func TestBuildLockArgsDoesNotAppendWhenCallerSuppliesBareFlag(t *testing.T) {
	args := []string{"status", "--gateway", "ws://127.0.0.1:1"}
	got := buildLockArgs(args, "/sock", "ws://gw", "tok")
	for _, a := range got {
		if a == "--gateway=ws://gw" {
			t.Fatalf("harness gateway was appended even though caller supplied --gateway: %v", got)
		}
	}
}

func TestBuildLockArgsDoesNotAppendWhenCallerSuppliesSocket(t *testing.T) {
	args := []string{"status", "--socket=/nowhere.sock"}
	got := buildLockArgs(args, "/sock", "ws://gw", "tok")
	for _, a := range got {
		if a == "--socket=/sock" {
			t.Fatalf("harness socket was appended even though caller supplied --socket: %v", got)
		}
	}
}
