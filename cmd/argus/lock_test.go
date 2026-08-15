package main

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/keyfmt"
)

func TestLockLogCmdWiredInLockCmd(t *testing.T) {
	cmd := newLockCmd()
	found := false
	for _, c := range cmd.Commands() {
		if c.Name() == "log" {
			found = true
			break
		}
	}
	if !found {
		t.Error("'argus lock log' subcommand not registered in newLockCmd")
	}
}

func TestLockSignHint(t *testing.T) {
	pub := testGenesis(0xAB)
	hint := lockSignHint(pub)
	if !strings.HasPrefix(hint, "argus lock sign ") {
		t.Fatalf("hint %q does not start with 'argus lock sign '", hint)
	}
	encoded := keyfmt.DeviceKey.Encode(pub)
	if !strings.HasSuffix(hint, encoded) {
		t.Fatalf("hint %q does not end with pubkey %s", hint, encoded)
	}
}
