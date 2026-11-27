package main

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
)

// A one-shot command never dials tcp; a long-running client may.
func TestOneShotOptionsSkipTCP(t *testing.T) {
	cfg := &config.Config{}
	cfg.P2P.Enabled = true
	if !p2pOptions(cfg, "tok", true).NoTCP {
		t.Fatal("one-shot options allow tcp")
	}
	if p2pOptions(cfg, "tok", false).NoTCP {
		t.Fatal("long-running options forbid tcp")
	}
}
