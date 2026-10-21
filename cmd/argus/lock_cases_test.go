package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustpin"
	"github.com/MunifTanjim/argus/internal/tui"
)

var lockCaseFixtures = []struct {
	name     string
	st       api.LockStatusResult
	state    tui.LockState
	headline string
}{
	{"replaced", api.LockStatusResult{Enabled: true, Quarantined: true, Pinned: true}, tui.LockOutReplaced,
		"locked mode: locked out — this node opens no channels\n" +
			"  the trust root it is pinned to was disabled by break-glass (permanently), and\n" +
			"  the network has since locked again under a new root\n"},
	{"unpinned", api.LockStatusResult{Enabled: true, Quarantined: true}, tui.LockOutUnpinned,
		"locked mode: locked out — the network is locked, but this node is not pinned to it\n" +
			"  this node opens no channels until you pin it (see pin: below)\n"},
	{"disabled", api.LockStatusResult{Enabled: true, Disabled: true}, tui.LockDisabled,
		"locked mode: disabled network-wide — break-glass used, nothing is enforced\n" +
			"  this is permanent: the log can never be re-enabled\n" +
			"  to lock again: argus lock init (new genesis; every device repins)\n"},
	{"not enabled", api.LockStatusResult{}, tui.LockNotEnabled, "locked mode: not enabled\n"},
	{"enforcing", api.LockStatusResult{Enabled: true}, tui.LockEnforcing, "locked mode: enforcing\n"},
}

func TestLockStateOfFollowsTheHeadline(t *testing.T) {
	for _, c := range lockCaseFixtures {
		t.Run(c.name, func(t *testing.T) {
			if got := lockStateOf(c.st); got != c.state {
				t.Fatalf("lockStateOf = %v, want %v", got, c.state)
			}
			if got := lockHeadline(c.st); got != c.headline {
				t.Fatalf("lockHeadline = %q, want %q", got, c.headline)
			}
		})
	}
}

func TestClientPinInfoStateFollowsTheText(t *testing.T) {
	cur, next := testGenesis(0xA1), testGenesis(0xA2)
	pinned := trustpin.Pin{Genesis: cur, Source: trustpin.SourceFile}
	cases := []struct {
		name   string
		cp     clientPinInfo
		state  tui.TUIPinState
		prefix string
	}{
		{"unreadable", clientPinInfo{perr: errors.New("bad")}, tui.TUIPinUnreadable, "  pin: unreadable"},
		{"replaced", clientPinInfo{pin: pinned, netGenesis: next, superseded: true}, tui.TUIRootReplaced,
			"  pin: " + fingerprintOf(cur) + " — the network now uses a new root"},
		{"replaced unchecked", clientPinInfo{pin: pinned, neterr: errors.New("down"), superseded: true}, tui.TUIRootReplacedUnchecked,
			"  pin: " + fingerprintOf(cur) + " — the pinned root was replaced, but the gateway could not be reached"},
		{"disabled", clientPinInfo{pin: pinned, superseded: true}, tui.TUIRootDisabled,
			"  pin: " + fingerprintOf(cur) + " — the pinned root was disabled network-wide"},
		{"pinned", clientPinInfo{pin: pinned}, tui.TUIPinned, "  pin: " + fingerprintOf(cur) + " (source:"},
		{"locked out", clientPinInfo{netGenesis: next}, tui.TUILockedOut, "  pin: none — locked out"},
		{"check failed", clientPinInfo{neterr: errors.New("down")}, tui.TUICheckFailed, "  pin: none (could not check"},
		{"unpinned", clientPinInfo{}, tui.TUIUnpinned, "  pin: none\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cp.state(); got != c.state {
				t.Fatalf("state = %v, want %v", got, c.state)
			}
			if got := c.cp.text(); !strings.HasPrefix(got, c.prefix) {
				t.Fatalf("text = %q, want prefix %q", got, c.prefix)
			}
		})
	}
}

func TestNodeStateOfMatchesAuthorizeHint(t *testing.T) {
	id := []byte{1, 2, 3}
	sts := []api.LockStatusResult{
		{},
		{Enabled: true, IdentityPubKey: id},
		{Enabled: true, IdentityPubKey: id, Authorized: true},
		{Enabled: true, IdentityPubKey: id, Disabled: true},
		{Enabled: true, IdentityPubKey: id, Quarantined: true},
		{Enabled: true},
	}
	for _, st := range append(sts, lockCaseFixtures[0].st) {
		state := nodeStateOf(st)
		if got, want := state == tui.NodeUnsigned && len(st.IdentityPubKey) > 0, authorizeHint(st) != ""; got != want {
			t.Fatalf("%+v: unsigned with identity=%v, hint printed=%v", st, got, want)
		}
		if (state == tui.NodeSigned || state == tui.NodeSigner) && !st.Authorized {
			t.Fatalf("%+v: row says signed but the node is not authorized", st)
		}
	}
	if nodeStateOf(sts[3]) != tui.NodeNotApplicable {
		t.Fatal("disabled chain must be not applicable")
	}
}
