package node

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// fakeTrustPeer serves a fixed set of chains to trustlog.sync and records calls.
type fakeTrustPeer struct {
	chains  [][]byte
	pulls   int
	offers  int
	callErr error
}

func (f *fakeTrustPeer) Call(method string, params, out any) error {
	if f.callErr != nil {
		return f.callErr
	}
	switch method {
	case api.MethodTrustLogSync:
		f.pulls++
		if res, ok := out.(*api.TrustLogSyncResult); ok {
			var entries [][]byte
			for _, c := range f.chains {
				if raw, err := trustlog.ChainEntries(c); err == nil {
					entries = append(entries, raw...)
				}
			}
			res.Entries = entries
		}
	case api.MethodTrustLogPush:
		f.offers++
	}
	return nil
}

// lockedChainForTest builds a real single-entry genesis chain and returns its
// marshaled bytes plus the genesis hash. A one-entry log's Tip is its genesis.
func lockedChainForTest(t *testing.T) (chain []byte, genesis []byte) {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("signer key: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("new genesis: %v", err)
	}
	return trustlog.MarshalChain(log.Entries()), log.Tip()
}

func TestUnpinnedNodeTripsGateOnChain(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	peer := &fakeTrustPeer{chains: [][]byte{chain}}

	d.syncTrustOnce(peer)

	if !d.Quarantined() {
		t.Fatal("an unpinned node that saw a chain must quarantine")
	}
	if got := d.trustGate.Genesis(); string(got) != string(genesis) {
		t.Fatalf("gate genesis = %x, want %x", got, genesis)
	}
	if peer.offers != 0 {
		t.Fatal("an unpinned node has nothing to offer; it must only pull")
	}
}

func TestUnpinnedNodeStaysOpenWithNoChain(t *testing.T) {
	d := New()
	peer := &fakeTrustPeer{}

	d.syncTrustOnce(peer)

	if d.Quarantined() {
		t.Fatal("no chain on the network must not quarantine anyone")
	}
}

func TestUnpinnedNodeIgnoresUndecodableChain(t *testing.T) {
	d := New()
	peer := &fakeTrustPeer{chains: [][]byte{[]byte("not a chain")}}

	d.syncTrustOnce(peer)

	if d.Quarantined() {
		t.Fatal("garbage that does not decode must not trip the gate")
	}
}

func TestGateTripsOnlyOncePerNode(t *testing.T) {
	chain, _ := lockedChainForTest(t)
	d := New()
	peer := &fakeTrustPeer{chains: [][]byte{chain}}

	d.syncTrustOnce(peer)
	d.syncTrustOnce(peer)

	if peer.pulls != 1 {
		t.Fatalf("pulls = %d, want 1: a tripped gate must stop re-pulling", peer.pulls)
	}
}

// TestQuarantineRejectsHandshake drives a real Noise handshake into a quarantined
// responder and asserts no channel is established. Deleting the rejectsChannels()
// guard in handshake() makes this test fail.
func TestQuarantineRejectsHandshake(t *testing.T) {
	clientKP, _ := e2e.GenerateKeyPair()
	chain, _ := lockedChainForTest(t)
	d := newE2ETestNode(t)
	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{chain}})
	if !d.Quarantined() {
		t.Fatal("precondition: gate must be tripped")
	}
	r := d.newRelayResponder()
	if runClientHandshake(t, r, clientKP) {
		t.Fatal("quarantined node must reject inbound handshake; no channel must be established")
	}
}

// TestReevaluateClosesChannelsWhenQuarantined establishes a live channel on an
// open node, trips the gate, then calls reevaluate and asserts the channel is
// closed. Deleting the rejectsChannels() guard in reevaluate() makes this test fail.
func TestReevaluateClosesChannelsWhenQuarantined(t *testing.T) {
	clientKP, _ := e2e.GenerateKeyPair()
	_, genesis := lockedChainForTest(t)
	d := newE2ETestNode(t)
	r := d.newRelayResponder()

	if !runClientHandshake(t, r, clientKP) {
		t.Fatal("precondition: open node must establish channel")
	}

	d.trustGate.Trip(genesis)
	r.reevaluate()

	const chanID = "enforce-test-chan"
	if r.lookup(chanID) != nil {
		t.Fatal("reevaluate must close all channels when node is quarantined")
	}
}

// TestLocalDisableOverridesQuarantine drives a real handshake through a node
// whose gate is tripped and local-disable is set, and asserts the channel is
// accepted. local-disable is the universal escape hatch.
func TestLocalDisableOverridesQuarantine(t *testing.T) {
	clientKP, _ := e2e.GenerateKeyPair()
	chain, _ := lockedChainForTest(t)
	d := newE2ETestNode(t)
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{chain}})
	if !d.Quarantined() {
		t.Fatal("precondition: gate must be tripped")
	}
	if err := d.LocalDisable(); err != nil {
		t.Fatalf("LocalDisable: %v", err)
	}
	r := d.newRelayResponder()
	if !runClientHandshake(t, r, clientKP) {
		t.Fatal("local-disable must override quarantine; handshake must succeed")
	}
}

// disabledChainNode returns a node pinned to its own genesis whose chain has been
// disabled by a break-glass secret — the state every device is left in after
// `argus lock disable`.
func disabledChainNode(t *testing.T) *Node {
	t.Helper()
	d := newLockTestNode(t)
	res, err := callLockInit(t, d, api.LockInitParams{GenDisablements: 1})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	raw, _ := json.Marshal(api.LockDisableParams{Secret: res.DisablementSecrets[0]})
	if _, err := d.handleLockDisable(context.Background(), raw); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if !d.TrustStore().Disabled() {
		t.Fatal("precondition: store must be disabled")
	}
	return d
}

// A disabled chain enforces nothing and can never be re-enabled, so once the network
// serves a different root the stale pin has all the costs of a pin and none of the
// protection. The node must fail closed exactly like an unpinned one.
func TestDisabledChainNodeQuarantinesOnForeignGenesis(t *testing.T) {
	d := disabledChainNode(t)
	foreign, foreignGenesis := lockedChainForTest(t)

	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{foreign}})

	if !d.Quarantined() {
		t.Fatal("a superseded node must quarantine")
	}
	if !d.rejectsChannels() {
		t.Fatal("a superseded node must reject channels")
	}
	if got := d.trustGate.Genesis(); !bytes.Equal(got, foreignGenesis) {
		t.Fatalf("gate genesis = %x, want the foreign root %x", got, foreignGenesis)
	}
}

// Branches of our own root are not a supersession, however many arrive.
func TestDisabledChainNodeIgnoresItsOwnGenesis(t *testing.T) {
	d := disabledChainNode(t)
	own := d.TrustStore().Bytes()

	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{own}})

	if d.Quarantined() {
		t.Fatal("our own chain must never quarantine us")
	}
}

// A live pin is exactly what should refuse a foreign root without going dark.
func TestEnforcingNodeIgnoresForeignGenesis(t *testing.T) {
	d := newLockTestNode(t)
	if _, err := callLockInit(t, d, api.LockInitParams{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	foreign, _ := lockedChainForTest(t)

	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{foreign}})

	if d.Quarantined() {
		t.Fatal("an enforcing node must reject a foreign chain without quarantining")
	}
}

// The offer must survive: a disabled node is how the disable entry reaches nodes
// that were offline when it happened, and the gateway's copy is not guaranteed.
// The gateway signals via Want that it needs our chain; the node must push.
func TestSupersededNodeStillOffersItsChain(t *testing.T) {
	d := disabledChainNode(t)
	foreign, _ := lockedChainForTest(t)
	// fakeTrustPeer doesn't support Want — use a fakeTrustCaller to simulate the
	// gateway requesting our disabled chain.
	var offers int
	peer := &fakeTrustCaller{
		fn: func(method string, params, out any) error {
			switch method {
			case api.MethodTrustLogSync:
				res := out.(*api.TrustLogSyncResult)
				raw, _ := trustlog.ChainEntries(foreign)
				res.Entries = raw
				res.Want = [][]byte{bytes.Repeat([]byte{0x01}, 32)}
			case api.MethodTrustLogPush:
				offers++
			}
			return nil
		},
	}

	d.syncTrustOnce(peer)

	if offers == 0 {
		t.Fatal("a superseded node must push its disabled chain when the gateway asks")
	}
}

// TestEnableTrustLogClearsGateWhenGenesisMatches checks that a node whose gate
// was tripped for genesis G is unquarantined by EnableTrustLog(G, path) without
// going through AdoptPin. AdoptPin already cleared the gate unconditionally;
// EnableTrustLog must do the same when it receives the matching genesis.
func TestEnableTrustLogClearsGateWhenGenesisMatches(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	path := filepath.Join(t.TempDir(), "chain")

	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{chain}})
	if !d.Quarantined() {
		t.Fatal("precondition: gate must be tripped before calling EnableTrustLog")
	}

	if err := d.EnableTrustLog(genesis, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}

	if d.Quarantined() {
		t.Fatal("gate must be cleared after EnableTrustLog with the matching genesis")
	}
	if d.rejectsChannels() {
		t.Fatal("node must accept channels after the gate is cleared")
	}
}

// The network can be relocked more than once. A first-sighting-wins gate keeps
// naming the earlier successor, so the operator compares a fingerprint that matches
// no node and pins a root that no longer exists.
func TestSupersededNodeFollowsTheLatestRoot(t *testing.T) {
	d := disabledChainNode(t)
	first, firstGenesis := lockedChainForTest(t)
	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{first}})
	if got := d.trustGate.Genesis(); !bytes.Equal(got, firstGenesis) {
		t.Fatalf("gate genesis = %x, want %x", got, firstGenesis)
	}

	second, secondGenesis := lockedChainForTest(t)
	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{second}})

	if got := d.trustGate.Genesis(); !bytes.Equal(got, secondGenesis) {
		t.Fatalf("gate genesis = %x, want the newer root %x", got, secondGenesis)
	}
}

// A dead root the gateway still retains must never outrank the live one: that
// fingerprint is what the operator compares against a working node.
func TestSupersededNodeNamesTheLiveRootNotTheDeadOne(t *testing.T) {
	d := disabledChainNode(t)
	own := d.TrustStore().Bytes()
	live, liveGenesis := lockedChainForTest(t)

	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{own, live}})

	if got := d.trustGate.Genesis(); !bytes.Equal(got, liveGenesis) {
		t.Fatalf("gate genesis = %x, want the live root %x", got, liveGenesis)
	}
}
