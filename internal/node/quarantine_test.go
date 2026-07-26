package node

import (
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// fakeTrustPeer serves a fixed set of chains to trustlog.pull and records calls.
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
	case api.MethodTrustLogPull:
		f.pulls++
		if res, ok := out.(*api.TrustLogPullResult); ok {
			res.Chains = f.chains
		}
	case api.MethodTrustLogOffer:
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

func TestQuarantineRejectsHandshake(t *testing.T) {
	chain, _ := lockedChainForTest(t)
	d := New()
	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{chain}})
	if !d.Quarantined() {
		t.Fatal("precondition: node should be quarantined")
	}

	if !d.rejectsChannels() {
		t.Fatal("a quarantined node must reject inbound channels")
	}
}

func TestLocalDisableOverridesQuarantine(t *testing.T) {
	chain, _ := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{chain}})
	if err := d.LocalDisable(); err != nil {
		t.Fatalf("LocalDisable: %v", err)
	}

	if d.rejectsChannels() {
		t.Fatal("local-disable is the universal escape hatch; it must override quarantine")
	}
}
