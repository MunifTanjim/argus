package main

import (
	"path/filepath"
	"testing"
)

func TestLocalNodeRunningAbsent(t *testing.T) {
	// A socket path that does not exist → not running, no error.
	socket := filepath.Join(t.TempDir(), "missing.sock")
	running, err := localNodeRunning(socket)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if running {
		t.Fatal("running = true, want false for a missing socket")
	}
}
