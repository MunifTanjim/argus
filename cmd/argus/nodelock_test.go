package main

import (
	"log/slog"
	"os"
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/node"
)

func nodeLockTempStateDir(t *testing.T) {
	t.Helper()
	old := config.StateDir
	config.StateDir = t.TempDir()
	t.Cleanup(func() { config.StateDir = old })
}

// A node configured for e2ee whose identity file is corrupt must refuse to start
// (fail closed) rather than run with the trust log on but encryption off, which
// would serve unauthenticated plaintext channels — a locked-mode bypass.
func TestConfigureNodeLockFailsClosedOnBadIdentity(t *testing.T) {
	nodeLockTempStateDir(t)
	if err := os.WriteFile(config.GetStatePath("node-identity.json"), []byte("not a valid identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := node.New()
	cfg := &config.Config{}
	cfg.E2EE.Enabled = true

	err := configureNodeLock(d, cfg, slog.New(slog.DiscardHandler))

	if err == nil {
		t.Fatal("want an error (refuse to start) on a corrupt identity, got nil")
	}
	if d.TrustStore() != nil {
		t.Error("trust log must not be enabled after an identity failure")
	}
}

// A fresh node (no pin, TOFU) still starts: the fail-closed policy must not turn
// a normal e2ee startup into an error.
func TestConfigureNodeLockStartsWithFreshIdentity(t *testing.T) {
	nodeLockTempStateDir(t)
	d := node.New()
	cfg := &config.Config{}
	cfg.E2EE.Enabled = true

	if err := configureNodeLock(d, cfg, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("fresh identity (TOFU) must start, got %v", err)
	}
}
