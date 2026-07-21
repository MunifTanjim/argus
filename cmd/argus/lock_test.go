package main

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestResolveSigners(t *testing.T) {
	sigB := base64.StdEncoding.EncodeToString([]byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	roster := []api.NodeDescriptor{
		{ID: "node-a", Label: "alpha", SignerPubKey: base64.StdEncoding.EncodeToString([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))},
		{ID: "node-b", Label: "beta", SignerPubKey: sigB},
	}
	// Resolve by label.
	got, err := resolveSigners(roster, []string{"beta"})
	if err != nil {
		t.Fatalf("resolveSigners: %v", err)
	}
	if len(got) != 1 || base64.StdEncoding.EncodeToString(got[0]) != sigB {
		t.Fatalf("resolved = %v", got)
	}
	// Resolve by id.
	if _, err := resolveSigners(roster, []string{"node-a"}); err != nil {
		t.Fatalf("by id: %v", err)
	}
	// Unknown name errors.
	if _, err := resolveSigners(roster, []string{"nope"}); err == nil {
		t.Fatal("unknown signer name should error")
	}
	// A node without a signer pubkey errors.
	noSigner := []api.NodeDescriptor{{ID: "n", Label: "n"}}
	if _, err := resolveSigners(noSigner, []string{"n"}); err == nil {
		t.Fatal("node without signer pubkey should error")
	}
}

func TestResolveDevice(t *testing.T) {
	idA := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xA1}, 32))
	roster := []api.NodeDescriptor{{ID: "node-a", Label: "alpha", IdentityPubKey: idA}}

	// Roster label → identity pubkey.
	got, err := resolveDevice(roster, "alpha")
	if err != nil {
		t.Fatalf("by label: %v", err)
	}
	if base64.StdEncoding.EncodeToString(got) != idA {
		t.Fatalf("resolved = %x", got)
	}
	// Raw base64 pubkey (32 bytes) passes through.
	rawPub := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xB2}, 32))
	got, err = resolveDevice(roster, rawPub)
	if err != nil || base64.StdEncoding.EncodeToString(got) != rawPub {
		t.Fatalf("raw pubkey: got %x err %v", got, err)
	}
	// Non-32-byte base64 → error.
	if _, err := resolveDevice(roster, base64.StdEncoding.EncodeToString([]byte{1, 2, 3})); err == nil {
		t.Fatal("short pubkey should error")
	}
	// Unknown non-base64 string → error.
	if _, err := resolveDevice(roster, "not-a-node-or-key!!"); err == nil {
		t.Fatal("unresolvable device should error")
	}
	// Resolve by node ID → identity pubkey.
	got, err = resolveDevice(roster, "node-a")
	if err != nil {
		t.Fatalf("by id: %v", err)
	}
	if base64.StdEncoding.EncodeToString(got) != idA {
		t.Fatalf("by id resolved = %x, want %s", got, idA)
	}
	// Roster node with empty IdentityPubKey → error.
	noIdentity := []api.NodeDescriptor{{ID: "node-x", Label: "x"}}
	if _, err := resolveDevice(noIdentity, "node-x"); err == nil {
		t.Fatal("node without identity pubkey should error")
	}
}

func TestLockSignHint(t *testing.T) {
	pub := bytes.Repeat([]byte{0xAB}, 32)
	hint := lockSignHint(pub)
	if !strings.HasPrefix(hint, "argus lock sign ") {
		t.Fatalf("hint %q does not start with 'argus lock sign '", hint)
	}
	encoded := base64.StdEncoding.EncodeToString(pub)
	if !strings.HasSuffix(hint, encoded) {
		t.Fatalf("hint %q does not end with pubkey %s", hint, encoded)
	}
}

func TestGatherDevices(t *testing.T) {
	idA := base64.StdEncoding.EncodeToString([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	roster := []api.NodeDescriptor{
		{ID: "a", IdentityPubKey: idA},
		{ID: "b"}, // no identity pubkey: skipped
	}
	devs := gatherDevices(roster)
	if len(devs) != 1 || base64.StdEncoding.EncodeToString(devs[0]) != idA {
		t.Fatalf("gatherDevices = %v", devs)
	}
}
