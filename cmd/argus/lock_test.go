package main

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/keyfmt"
)

func TestLockCmdRegistersOnly6aSubcommands(t *testing.T) {
	cmd := newLockCmd()
	subs := cmd.Commands()

	if len(subs) != 5 {
		names := make([]string, len(subs))
		for i, c := range subs {
			names[i] = c.Name()
		}
		t.Fatalf("newLockCmd must register exactly 5 subcommands, got %d: %v", len(subs), names)
	}

	want := []string{"local-disable", "log", "pin", "status", "unpin"}
	for _, name := range want {
		found := false
		for _, c := range subs {
			if c.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected subcommand %q not registered", name)
		}
	}

	forbidden := []string{"init", "sign", "revoke-device", "add-signer", "remove-signer", "disable", "revoke-signer"}
	for _, name := range forbidden {
		for _, c := range subs {
			if c.Name() == name {
				t.Errorf("signing/ceremony command %q must not be registered in 6a", name)
			}
		}
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
