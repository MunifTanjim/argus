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

func TestParseSignerKeysAcceptsOnlyKeys(t *testing.T) {
	want := bytes.Repeat([]byte{0xD4}, 32)

	got, err := parseSignerKeys([]string{keyfmt.SignerKey.Encode(want)})
	if err != nil {
		t.Fatalf("parseSignerKeys: %v", err)
	}
	if len(got) != 1 || !bytes.Equal(got[0], want) {
		t.Fatalf("parsed = %x, want %x", got, want)
	}
}

// A node name must be refused outright. Resolving one would go through the roster
// the gateway serves, which nothing constrains at init time — the whole reason this
// input is keys-only.
func TestParseSignerKeysRejectsANodeName(t *testing.T) {
	err := func() error { _, e := parseSignerKeys([]string{"node-b"}); return e }()
	if err == nil {
		t.Fatal("a node name must not be accepted as a signer")
	}
	if !strings.Contains(err.Error(), keyfmt.SignerKey.Prefix()) {
		t.Fatalf("error must show the expected form, got: %v", err)
	}
	if !strings.Contains(err.Error(), "argus lock status") {
		t.Fatalf("error must say where to read the key, got: %v", err)
	}
}

func TestParseSignerKeysRejectsAWrongKindOfKey(t *testing.T) {
	_, err := parseSignerKeys([]string{keyfmt.DeviceKey.Encode(bytes.Repeat([]byte{0x01}, 32))})
	if err == nil {
		t.Fatal("a device key must not be accepted as a signer")
	}
	if !strings.Contains(err.Error(), "signer key") || !strings.Contains(err.Error(), "device key") {
		t.Fatalf("error must name both kinds, got: %v", err)
	}
}

// The post-init signer commands still accept a name; keys must win there too.
func TestResolveSignerArgsAcceptsBothFormsAndPrefersTheKey(t *testing.T) {
	want := bytes.Repeat([]byte{0xD4}, 32)
	rosterKey := bytes.Repeat([]byte{0xEE}, 32)
	roster := []api.NodeDescriptor{{
		ID:           "node-b",
		Label:        "beta",
		SignerPubKey: base64.StdEncoding.EncodeToString(rosterKey),
	}}

	byName, err := resolveSignerArgs(roster, []string{"beta"})
	if err != nil || !bytes.Equal(byName[0], rosterKey) {
		t.Fatalf("by name: got %x err %v", byName, err)
	}
	byKey, err := resolveSignerArgs(roster, []string{keyfmt.SignerKey.Encode(want)})
	if err != nil || !bytes.Equal(byKey[0], want) {
		t.Fatalf("by key: got %x err %v", byKey, err)
	}
	if _, err := resolveSignerArgs(roster, []string{"nope"}); err == nil {
		t.Fatal("unknown name should error")
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

func TestGatherRosterDevices(t *testing.T) {
	idA := base64.StdEncoding.EncodeToString([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	roster := []api.NodeDescriptor{
		{ID: "a", Label: "alpha", IdentityPubKey: idA},
		{ID: "b"}, // no identity pubkey: skipped
	}
	devs := gatherRosterDevices(roster)
	if len(devs) != 1 || base64.StdEncoding.EncodeToString(devs[0].pub) != idA {
		t.Fatalf("gatherRosterDevices = %v", devs)
	}
	if devs[0].label != "alpha" {
		t.Fatalf("label = %q, want alpha", devs[0].label)
	}
}

func TestRequireOwnSignerKey(t *testing.T) {
	own := bytes.Repeat([]byte{0x01}, 32)
	other := bytes.Repeat([]byte{0x02}, 32)

	if err := requireOwnSignerKey(own, [][]byte{other, own}, nil); err != nil {
		t.Fatalf("own key listed: %v", err)
	}
	err := requireOwnSignerKey(own, [][]byte{other}, []string{keyfmt.SignerKey.Encode(other)})
	if err == nil {
		t.Fatal("omitting this node's own key must be refused")
	}
	// The error has to be copy-pasteable: the full corrected command, own key first.
	want := "argus lock init " + keyfmt.SignerKey.Encode(own) + " " + keyfmt.SignerKey.Encode(other)
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error must contain the corrected command %q, got: %v", want, err)
	}
}

// The preview is what stands between an operator and a permanent, once-only
// disclosure of disablement secrets, so it has to show every part of what would be
// created — including the roster-supplied device list nothing has verified.
func TestInitPreviewShowsWhatWouldBeCreated(t *testing.T) {
	own := bytes.Repeat([]byte{0x01}, 32)
	other := bytes.Repeat([]byte{0x02}, 32)
	dev := bytes.Repeat([]byte{0x03}, 32)
	args := []string{keyfmt.SignerKey.Encode(own), keyfmt.SignerKey.Encode(other)}

	got := initPreview(own, [][]byte{own, other}, []rosterDevice{{pub: dev, label: "node-b"}}, 2, args)

	for _, want := range []string{
		keyfmt.SignerKey.Encode(own),
		keyfmt.SignerKey.Encode(other),
		"(this node)",
		"disablement secrets: 2",
		keyfmt.DeviceKey.Encode(dev),
		"node-b",
		"come from the gateway",
		"nothing has been created",
		"argus lock init --confirm " + strings.Join(args, " "),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("preview must contain %q, got:\n%s", want, got)
		}
	}
}
