package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
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

func TestStartEmbeddedNodeMalformedGenesis(t *testing.T) {
	// A non-empty but malformed lock.genesis must not start an open node: the
	// function must return an error immediately (fail-closed invariant).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := &config.Config{Lock: config.LockConfig{Genesis: "not!base64!!"}}
	socket := filepath.Join(t.TempDir(), "node.sock")

	_, _, err := startEmbeddedNode(ctx, cfg, socket)
	if err == nil {
		t.Fatal("startEmbeddedNode with malformed genesis should return an error")
	}
}
