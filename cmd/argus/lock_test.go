package main

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/keyfmt"
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

// A sigpub: key must be trusted as given, without consulting the roster at all.
// At lock init the roster comes from the untrusted gateway and no trust log yet
// constrains it, so a key collected out-of-band is the only gateway-independent
// way to name a co-signer.
func TestResolveSignersAcceptsAKeyWithoutTheRoster(t *testing.T) {
	want := bytes.Repeat([]byte{0xD4}, 32)

	got, err := resolveSigners(nil, []string{keyfmt.SignerKey.Encode(want)})
	if err != nil {
		t.Fatalf("resolveSigners: %v", err)
	}
	if len(got) != 1 || !bytes.Equal(got[0], want) {
		t.Fatalf("resolved = %x, want %x", got, want)
	}
}

// A roster that claims a different key for a named node cannot override an
// explicitly supplied one — the key path must not consult the roster.
func TestResolveSignersPrefersTheGivenKeyOverTheRoster(t *testing.T) {
	want := bytes.Repeat([]byte{0xD4}, 32)
	roster := []api.NodeDescriptor{{
		ID:           keyfmt.SignerKey.Encode(want),
		Label:        keyfmt.SignerKey.Encode(want),
		SignerPubKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xEE}, 32)),
	}}

	got, err := resolveSigners(roster, []string{keyfmt.SignerKey.Encode(want)})
	if err != nil {
		t.Fatalf("resolveSigners: %v", err)
	}
	if !bytes.Equal(got[0], want) {
		t.Fatalf("resolved = %x, want the supplied key %x", got[0], want)
	}
}

func TestResolveSignersRejectsAWrongKindOfKey(t *testing.T) {
	_, err := resolveSigners(nil, []string{keyfmt.DeviceKey.Encode(bytes.Repeat([]byte{0x01}, 32))})
	if err == nil {
		t.Fatal("a device key must not be accepted as a signer")
	}
	if !strings.Contains(err.Error(), "signer key") || !strings.Contains(err.Error(), "device key") {
		t.Fatalf("error must name both kinds, got: %v", err)
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
	// A devpub: key passes through.
	rawPub := keyfmt.DeviceKey.Encode(bytes.Repeat([]byte{0xB2}, 32))
	got, err = resolveDevice(roster, rawPub)
	if err != nil || keyfmt.DeviceKey.Encode(got) != rawPub {
		t.Fatalf("device key: got %x err %v", got, err)
	}
	// Short devpub: body → error.
	if _, err := resolveDevice(roster, "devpub:010203"); err == nil {
		t.Fatal("short pubkey should error")
	}
	// Unknown untagged string → error.
	if _, err := resolveDevice(roster, "not-a-node-or-key!!"); err == nil {
		t.Fatal("unresolvable device should error")
	}
	// A genesis hash is also 32 bytes: it must be refused by kind, not accepted.
	err = func() error {
		_, e := resolveDevice(roster, keyfmt.Genesis.Encode(bytes.Repeat([]byte{0xC3}, 32)))
		return e
	}()
	if err == nil {
		t.Fatal("a genesis hash must not resolve as a device")
	}
	if !strings.Contains(err.Error(), "genesis hash") || !strings.Contains(err.Error(), "device key") {
		t.Fatalf("error must name both kinds, got: %v", err)
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
	encoded := keyfmt.DeviceKey.Encode(pub)
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
