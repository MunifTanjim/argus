package main

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/keyfmt"
)

func TestLockCmdRegistersExpectedSubcommands(t *testing.T) {
	cmd := newLockCmd()
	subs := cmd.Commands()

	want := []string{
		"local-disable", "log", "pin", "status", "unpin",
		"init", "sign", "revoke-device", "add-signer", "remove-signer", "disable",
	}
	if len(subs) != len(want) {
		names := make([]string, len(subs))
		for i, c := range subs {
			names[i] = c.Name()
		}
		t.Fatalf("newLockCmd must register exactly %d subcommands, got %d: %v", len(want), len(subs), names)
	}

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

	// revoke-signer (6c ceremony) must not be registered yet.
	for _, c := range subs {
		if c.Name() == "revoke-signer" {
			t.Errorf("ceremony command %q must not be registered until 6c", c.Name())
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

func TestLockInitFewSignersWarning(t *testing.T) {
	if w := lockInitFewSignersWarning(1); w == "" {
		t.Error("lockInitFewSignersWarning(1) should return a non-empty warning")
	}
	if w := lockInitFewSignersWarning(2); w == "" {
		t.Error("lockInitFewSignersWarning(2) should return a non-empty warning")
	}
	if w := lockInitFewSignersWarning(3); w != "" {
		t.Errorf("lockInitFewSignersWarning(3) should return empty, got %q", w)
	}
	if w := lockInitFewSignersWarning(5); w != "" {
		t.Errorf("lockInitFewSignersWarning(5) should return empty, got %q", w)
	}
	w2 := lockInitFewSignersWarning(2)
	if !strings.Contains(w2, "revoke-signer") {
		t.Errorf("warning should mention 'revoke-signer', got: %q", w2)
	}
	if !strings.Contains(w2, "disable") {
		t.Errorf("warning should mention 'disable', got: %q", w2)
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

// Every signer input is keys-only, post-init included: a name could only be turned
// into a key through the gateway's roster, so revoking or adding a signer by name
// would let the gateway decide which key you actually acted on.
func TestSignerCommandsTakeOnlyKeys(t *testing.T) {
	want := bytes.Repeat([]byte{0xD4}, 32)

	got, err := parseSignerKeys([]string{keyfmt.SignerKey.Encode(want)})
	if err != nil || !bytes.Equal(got[0], want) {
		t.Fatalf("key form: got %x err %v", got, err)
	}
	if _, err := parseSignerKeys([]string{"beta"}); err == nil {
		t.Fatal("a node label must not resolve to a signer")
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
