package main

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/keyfmt"
)

func TestLockStatusLinesNamesTheThreeStates(t *testing.T) {
	cases := []struct {
		name string
		st   api.LockStatusResult
		want string
	}{
		{"never locked", api.LockStatusResult{}, "locked mode: not enabled"},
		{"enforcing", api.LockStatusResult{Enabled: true, Pinned: true}, "locked mode: enforcing"},
		{"break-glass", api.LockStatusResult{Enabled: true, Disabled: true, Pinned: true}, "locked mode: disabled network-wide"},
	}
	for _, tc := range cases {
		out, _ := lockStatusLines(tc.st)
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%s: output %q must contain %q", tc.name, out, tc.want)
		}
	}
}

// The break-glass state is terminal and the way back is a new genesis; say both.
func TestLockStatusLinesExplainsBreakGlass(t *testing.T) {
	out, _ := lockStatusLines(api.LockStatusResult{Enabled: true, Disabled: true, Pinned: true})
	for _, want := range []string{"permanent", "argus lock init"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q must contain %q", out, want)
		}
	}
}

// A superseded device is pinned AND quarantined; the line must name both roots and
// the one command that fixes it.
func TestLockStatusLinesReportsSupersession(t *testing.T) {
	out, _ := lockStatusLines(api.LockStatusResult{
		Enabled:     true,
		Disabled:    true,
		Pinned:      true,
		Quarantined: true,
		PinGenesis:  testGenesis(0xC1),
		SeenGenesis: testGenesis(0xC2),
	})
	for _, want := range []string{"SUPERSEDED", "argus lock pin"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q must contain %q", out, want)
		}
	}
	if strings.Contains(out, "locked mode: disabled network-wide") {
		t.Fatal("the break-glass headline is subsumed by the supersession one")
	}
}

func TestLockStatusLinesKeepsUnpinnedQuarantineWording(t *testing.T) {
	out, _ := lockStatusLines(api.LockStatusResult{Quarantined: true, SeenGenesis: testGenesis(0xC2)})
	for _, want := range []string{"QUARANTINED", "argus lock pin"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q must contain %q", out, want)
		}
	}
}

// A superseded device's own log being disabled is background: what matters is that
// THIS device is refusing everything. Leading with "nothing is enforced" reads as a
// contradiction two lines above "this device refuses all channels".
func TestLockStatusLinesLeadsWithQuarantineWhenSuperseded(t *testing.T) {
	out, _ := lockStatusLines(api.LockStatusResult{
		Enabled:     true,
		Disabled:    true,
		Pinned:      true,
		Quarantined: true,
		PinGenesis:  testGenesis(0xC1),
		SeenGenesis: testGenesis(0xC2),
	})
	head := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(head, "QUARANTINED") {
		t.Fatalf("headline %q must lead with the device's own state", head)
	}
	// The break-glass headline (lockStatusLines' st.Disabled branch) says "nothing is
	// enforced", which reads as the opposite of refusing every channel. A superseded
	// device must not fall through to it: it is the most restrictive state there is.
	if strings.Contains(out, "nothing is enforced") {
		t.Fatalf("output %q must not claim it enforces nothing while refusing every channel", out)
	}
	if n := strings.Count(out, "refuses all channels"); n != 1 {
		t.Fatalf("output %q states the refusal %d times, want exactly 1", out, n)
	}
}

// After two relocks the gateway offers several roots, and bare `lock pin` refuses to
// choose between them — so the instruction has to carry the genesis.
func TestLockStatusLinesGivesAnExplicitPinCommand(t *testing.T) {
	seen := testGenesis(0xC2)
	for _, st := range []api.LockStatusResult{
		{Enabled: true, Disabled: true, Pinned: true, Quarantined: true, PinGenesis: testGenesis(0xC1), SeenGenesis: seen},
		{Quarantined: true, SeenGenesis: seen},
	} {
		out, _ := lockStatusLines(st)
		if want := "argus lock pin " + keyfmt.Genesis.Encode(seen); !strings.Contains(out, want) {
			t.Fatalf("output %q must contain %q", out, want)
		}
	}
}

// Every section renders in every state. Field presence must not depend on the
// headline branch — that coupling is what hid the device set and the node's own
// keys for as long as it did.
func TestLockStatusLinesAlwaysRendersEverySection(t *testing.T) {
	states := map[string]api.LockStatusResult{
		"not enabled": {IdentityPubKey: testGenesis(0xB0)},
		"enforcing": {
			Enabled: true, Pinned: true,
			PinGenesis: testGenesis(0xA1), Tip: testGenesis(0xB1),
			IdentityPubKey: testGenesis(0xB0), DeviceCount: 1,
		},
		"disabled": {
			Enabled: true, Disabled: true, Pinned: true,
			PinGenesis: testGenesis(0xA2), Tip: testGenesis(0xB2),
			IdentityPubKey: testGenesis(0xB0),
		},
		"quarantined": {
			Enabled: true, Quarantined: true,
			SeenGenesis: testGenesis(0xA3), Tip: testGenesis(0xB3),
			IdentityPubKey: testGenesis(0xB0),
		},
		"superseded": {
			Enabled: true, Disabled: true, Pinned: true, Quarantined: true,
			PinGenesis: testGenesis(0xA4), SeenGenesis: testGenesis(0xA5),
			Tip: testGenesis(0xB4), IdentityPubKey: testGenesis(0xB0),
		},
	}
	// "  pin: " and not "pin": the disabled headline says "every device repins",
	// which would satisfy a bare substring check with the pin section absent.
	labels := []string{"chain", "this node", "signers", "devices", "  pin: "}
	for name, st := range states {
		out, _ := lockStatusLines(st)
		for _, label := range labels {
			if !strings.Contains(out, label) {
				t.Errorf("%s: missing section %q in:\n%s", name, label, out)
			}
		}
	}
}

// The node's own keys must be visible while locked mode is on, not only when it
// is off.
func TestLockStatusLinesNamesThisNodesKeysWhileEnforcing(t *testing.T) {
	st := api.LockStatusResult{
		Enabled: true, Pinned: true,
		PinGenesis: testGenesis(0xA1), Tip: testGenesis(0xB1),
		IdentityPubKey: testGenesis(0xB0), SignerPubKey: testGenesis(0xB5),
		Authorized: true, SignerTrusted: true,
	}
	out, _ := lockStatusLines(st)
	for _, want := range []string{
		keyfmt.DeviceKey.Encode(testGenesis(0xB0)),
		keyfmt.SignerKey.Encode(testGenesis(0xB5)),
		"authorized: yes",
		"trusted: yes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output must contain %q:\n%s", want, out)
		}
	}
}

// Locked mode off means there is nothing to be authorized against; a bare "no"
// would read as a denial.
func TestLockStatusLinesOmitsAuthorizedWhenNotEnabled(t *testing.T) {
	out, _ := lockStatusLines(api.LockStatusResult{IdentityPubKey: testGenesis(0xB0)})
	for _, unwanted := range []string{"authorized:", "trusted:"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("output must not contain %q when locked mode is off:\n%s", unwanted, out)
		}
	}
}

// The genesis must be printed in a form that can be copied, not only fingerprinted.
func TestChainSectionPrintsGenesisTipAndLength(t *testing.T) {
	st := api.LockStatusResult{
		Enabled: true, Pinned: true,
		PinGenesis: testGenesis(0xA1), Tip: testGenesis(0xB1), Length: 12,
	}
	out := chainSection(st)
	for _, want := range []string{
		keyfmt.Genesis.Encode(testGenesis(0xA1)),
		fingerprintOf(testGenesis(0xA1)),
		keyfmt.Tip.Encode(testGenesis(0xB1)),
		fingerprintOf(testGenesis(0xB1)),
		"length:  12 entries",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chain section must contain %q:\n%s", want, out)
		}
	}
}

func TestChainSectionCollapsesWhenNotEnabled(t *testing.T) {
	out := chainSection(api.LockStatusResult{})
	if !strings.Contains(out, "chain: none") {
		t.Errorf("chain section = %q, want a none line", out)
	}
}

func TestDevicesSectionListsEachDeviceAndMarksThisNode(t *testing.T) {
	self, other := testGenesis(0xD0), testGenesis(0xD1)
	out := devicesSection(api.LockStatusResult{
		Enabled: true, DeviceCount: 2,
		Devices:        [][]byte{self, other},
		IdentityPubKey: self,
	})
	if !strings.Contains(out, "devices (2)") {
		t.Errorf("want a count header:\n%s", out)
	}
	for _, want := range []string{keyfmt.DeviceKey.Encode(self), keyfmt.DeviceKey.Encode(other)} {
		if !strings.Contains(out, want) {
			t.Errorf("must list %q:\n%s", want, out)
		}
	}
	selfLine := keyfmt.DeviceKey.Encode(self) + "  ← this node"
	if !strings.Contains(out, selfLine) {
		t.Errorf("must mark this node:\n%s", out)
	}
	if strings.Contains(out, keyfmt.DeviceKey.Encode(other)+"  ← this node") {
		t.Errorf("must not mark another device:\n%s", out)
	}
}

// An upgraded binary talking to a daemon that has not restarted returns a count with
// no list. Printing an empty roster for a populated chain would be a lie.
func TestDevicesSectionReportsAStaleDaemon(t *testing.T) {
	out := devicesSection(api.LockStatusResult{Enabled: true, DeviceCount: 7})
	if !strings.Contains(out, "devices (7)") {
		t.Errorf("must keep the count:\n%s", out)
	}
	if !strings.Contains(out, "restart it: argus start") {
		t.Errorf("must advise a restart:\n%s", out)
	}
}

func TestDevicesSectionCollapsesOnAnEmptyRoster(t *testing.T) {
	out := devicesSection(api.LockStatusResult{Enabled: true})
	if !strings.Contains(out, "devices: none") {
		t.Errorf("devices section = %q, want a none line", out)
	}
	if strings.Contains(out, "restart") {
		t.Errorf("an empty roster is not a stale daemon:\n%s", out)
	}
}

func TestSignersSectionListsSignersAndFingerprint(t *testing.T) {
	sigA, sigB := testGenesis(0xA1), testGenesis(0xA2)
	out := signersSection(api.LockStatusResult{
		Enabled: true, Signers: [][]byte{sigA, sigB},
	})
	if !strings.Contains(out, "signers (2)") {
		t.Errorf("want count header:\n%s", out)
	}
	for _, want := range []string{
		keyfmt.SignerKey.Encode(sigA),
		keyfmt.SignerKey.Encode(sigB),
		"fingerprint:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("must contain %q:\n%s", want, out)
		}
	}
}

// Break-glass disables enforcement but the signer list records who held authority;
// that list must still render so an operator can audit who had signing power.
func TestSignersSectionRendersWhenLogDisabledByBreakGlass(t *testing.T) {
	sigA := testGenesis(0xA1)
	out := signersSection(api.LockStatusResult{
		Enabled: true, Disabled: true,
		Signers: [][]byte{sigA},
	})
	if !strings.Contains(out, "signers (1)") {
		t.Errorf("want count header:\n%s", out)
	}
	if !strings.Contains(out, keyfmt.SignerKey.Encode(sigA)) {
		t.Errorf("must list signer:\n%s", out)
	}
}

func TestSignersSectionCollapsesWhenNoSigners(t *testing.T) {
	out := signersSection(api.LockStatusResult{})
	if !strings.Contains(out, "signers: none") {
		t.Errorf("signers section = %q, want a none line", out)
	}
}

func TestThisNodeSectionShowsKeysAndFlags(t *testing.T) {
	st := api.LockStatusResult{
		Enabled:        true,
		IdentityPubKey: testGenesis(0xB0),
		SignerPubKey:   testGenesis(0xB5),
		Authorized:     true,
		SignerTrusted:  true,
	}
	out := thisNodeSection(st)
	for _, want := range []string{
		"this node",
		keyfmt.DeviceKey.Encode(testGenesis(0xB0)),
		keyfmt.SignerKey.Encode(testGenesis(0xB5)),
		"authorized: yes",
		"trusted: yes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("must contain %q:\n%s", want, out)
		}
	}
}

func TestThisNodeSectionShowsNoneWhenKeysAbsent(t *testing.T) {
	out := thisNodeSection(api.LockStatusResult{})
	if !strings.Contains(out, "this node") {
		t.Errorf("section header missing:\n%s", out)
	}
	// Keys absent: expect (none) placeholders, not empty fields.
	if strings.Count(out, "(none)") < 2 {
		t.Errorf("expect (none) for both absent keys:\n%s", out)
	}
	// No locked mode: authorized/trusted flags must not appear.
	for _, unwanted := range []string{"authorized:", "trusted:"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("must not contain %q when not enabled:\n%s", unwanted, out)
		}
	}
}
