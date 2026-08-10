package main

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
)

func TestDesktopClickCmd(t *testing.T) {
	cfg := &config.Config{Socket: "/tmp/argus.sock"}
	argv := desktopClickCmd(cfg)("nodeA:abc")
	// Must invoke `focus`, target the local socket, and carry the session id.
	joined := strings.Join(argv, " ")
	for _, want := range []string{"focus", "--socket", "/tmp/argus.sock", "nodeA:abc"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("click argv %v missing %q", argv, want)
		}
	}
}
