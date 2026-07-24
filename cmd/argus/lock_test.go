package main

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestLockInitFewSignersWarning(t *testing.T) {
	// Warning fires for 1 signer.
	if w := lockInitFewSignersWarning(1); w == "" {
		t.Error("lockInitFewSignersWarning(1) should return a non-empty warning")
	}
	// Warning fires for 2 signers.
	if w := lockInitFewSignersWarning(2); w == "" {
		t.Error("lockInitFewSignersWarning(2) should return a non-empty warning")
	}
	// No warning for 3 signers.
	if w := lockInitFewSignersWarning(3); w != "" {
		t.Errorf("lockInitFewSignersWarning(3) should return empty, got %q", w)
	}
	// No warning for >3 signers.
	if w := lockInitFewSignersWarning(5); w != "" {
		t.Errorf("lockInitFewSignersWarning(5) should return empty, got %q", w)
	}
	// Warning mentions revoke-signer and disable.
	w2 := lockInitFewSignersWarning(2)
	if !strings.Contains(w2, "revoke-signer") {
		t.Errorf("warning should mention 'revoke-signer', got: %q", w2)
	}
	if !strings.Contains(w2, "disable") {
		t.Errorf("warning should mention 'disable', got: %q", w2)
	}
}

func TestSoleRootGuardDetectsZeroSigners(t *testing.T) {
	s1 := bytes.Repeat([]byte{0x01}, 32)
	s2 := bytes.Repeat([]byte{0x02}, 32)
	s3 := bytes.Repeat([]byte{0x03}, 32)

	// Revoking the sole signer → 0 remaining.
	if n := signerCountAfterRevoke([][]byte{s1}, [][]byte{s1}); n != 0 {
		t.Fatalf("sole-root: got %d, want 0", n)
	}
	// Revoking one of two signers → 1 remaining.
	if n := signerCountAfterRevoke([][]byte{s1, s2}, [][]byte{s1}); n != 1 {
		t.Fatalf("one-of-two: got %d, want 1", n)
	}
	// Revoking both of two signers → 0 remaining.
	if n := signerCountAfterRevoke([][]byte{s1, s2}, [][]byte{s1, s2}); n != 0 {
		t.Fatalf("both-of-two: got %d, want 0", n)
	}
	// Revoking a non-member → all remain.
	if n := signerCountAfterRevoke([][]byte{s1, s2}, [][]byte{s3}); n != 2 {
		t.Fatalf("non-member: got %d, want 2", n)
	}
	// Empty current → 0.
	if n := signerCountAfterRevoke(nil, [][]byte{s1}); n != 0 {
		t.Fatalf("nil current: got %d, want 0", n)
	}
}

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
