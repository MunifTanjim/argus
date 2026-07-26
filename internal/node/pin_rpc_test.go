package node

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/trustlog"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

func TestAdoptPinClearsQuarantineAndEnables(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{chain}})
	if !d.Quarantined() {
		t.Fatal("precondition: node should be quarantined")
	}

	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}

	if d.Quarantined() {
		t.Fatal("AdoptPin must clear the gate")
	}
	if d.TrustStore() == nil {
		t.Fatal("AdoptPin must enable the trust store")
	}
	if !bytes.Equal(d.pinGenesis, genesis) {
		t.Fatalf("pinGenesis = %x, want %x", d.pinGenesis, genesis)
	}
	persisted, err := trustpinFileForTest(d).Load()
	if err != nil {
		t.Fatalf("reload pin: %v", err)
	}
	if !bytes.Equal(persisted, genesis) {
		t.Fatal("AdoptPin must persist the pin so a reboot stays pinned")
	}
}

func TestAdoptPinIsIdempotentAndRefusesADifferentGenesis(t *testing.T) {
	_, genesis := lockedChainForTest(t)
	other := make([]byte, len(genesis))
	copy(other, genesis)
	other[0] ^= 0xFF

	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("re-pinning the same genesis must be a no-op, got: %v", err)
	}
	err := d.AdoptPin(other)
	if err == nil {
		t.Fatal("re-pinning a different genesis must be refused")
	}
	if !strings.Contains(err.Error(), "unpin") {
		t.Fatalf("error must name the recovery command, got: %v", err)
	}
}

func TestAdoptPinRejectsWrongLength(t *testing.T) {
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin([]byte{1, 2, 3}); err == nil {
		t.Fatal("a 3-byte genesis must be refused")
	}
}

func TestDropPinClearsEverything(t *testing.T) {
	_, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}

	if err := d.DropPin(); err != nil {
		t.Fatalf("DropPin: %v", err)
	}

	if d.TrustStore() != nil {
		t.Fatal("DropPin must clear the trust store")
	}
	if d.pinGenesis != nil {
		t.Fatal("DropPin must forget the pin")
	}
	got, err := trustpinFileForTest(d).Load()
	if err != nil || got != nil {
		t.Fatalf("DropPin must delete the pin file: got %v, %v", got, err)
	}
}

// pinnedNodeHoldingAChain returns a node pinned to a real genesis whose store has
// ingested the matching chain — i.e. one that provably saw the network's trust log
// — with clientPub authorized on it.
func pinnedNodeHoldingAChain(t *testing.T, clientPub []byte) (*Node, []byte) {
	t.Helper()
	d := newE2ETestNode(t)
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	lg, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesis := lg.Tip()
	if len(clientPub) > 0 {
		if err := lg.AuthorizeDevice(clientPub, signer); err != nil {
			t.Fatalf("AuthorizeDevice: %v", err)
		}
	}
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{trustlog.MarshalChain(lg.Entries())}})
	if d.TrustStore().Bytes() == nil {
		t.Fatal("precondition: the node must hold the chain")
	}
	return d, genesis
}

// TestDropPinQuarantinesANodeThatHeldAChain covers the unpin→pin rotation window:
// without an immediate re-trip the node serves any key the gateway introduces until
// the next detection tick.
func TestDropPinQuarantinesANodeThatHeldAChain(t *testing.T) {
	clientKP, _ := e2e.GenerateKeyPair()
	d, genesis := pinnedNodeHoldingAChain(t, clientKP.Public)

	if err := d.DropPin(); err != nil {
		t.Fatalf("DropPin: %v", err)
	}

	if !d.Quarantined() {
		t.Fatal("unpinning a node that held a chain must quarantine it immediately")
	}
	if !d.rejectsChannels() {
		t.Fatal("a quarantined node must reject channels")
	}
	if got := d.trustGate.Genesis(); !bytes.Equal(got, genesis) {
		t.Fatalf("gate genesis = %x, want the dropped pin %x", got, genesis)
	}
	if runClientHandshake(t, d.newRelayResponder(), clientKP) {
		t.Fatal("an unpinned node must not establish a channel after DropPin")
	}
}

// TestDropPinClosesLiveChannels asserts the pre-existing channels of a node that
// just unpinned are dropped, not left streaming until the next tick.
func TestDropPinClosesLiveChannels(t *testing.T) {
	const chanID = "reeval-test-chan"
	clientKP, _ := e2e.GenerateKeyPair()
	d, _ := pinnedNodeHoldingAChain(t, clientKP.Public)

	resp, _, _, _ := reevalHandshake(t, d, clientKP)
	if resp.lookup(chanID) == nil {
		t.Fatal("precondition: authorized client must have a live channel")
	}

	if err := d.DropPin(); err != nil {
		t.Fatalf("DropPin: %v", err)
	}

	if resp.lookup(chanID) != nil {
		t.Fatal("DropPin must drop live channels, not leave them streaming")
	}
}

// TestDropPinWithoutAChainLeavesTheNodeOpen guards the other direction: a node that
// never held a chain has no evidence the network is locked, and quarantining it
// would strand it — `lock pin` would find nothing to pin.
func TestDropPinWithoutAChainLeavesTheNodeOpen(t *testing.T) {
	_, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}

	if err := d.DropPin(); err != nil {
		t.Fatalf("DropPin: %v", err)
	}

	if d.Quarantined() {
		t.Fatal("a node that never saw a chain must stay open after unpinning")
	}
}

func TestDropPinDoesNotReleaseAQuarantine(t *testing.T) {
	chain, _ := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	d.syncTrustOnce(&fakeTrustPeer{chains: [][]byte{chain}})
	if !d.Quarantined() {
		t.Fatal("precondition: node should be quarantined")
	}

	if err := d.DropPin(); err != nil {
		t.Fatalf("DropPin: %v", err)
	}

	if !d.Quarantined() {
		t.Fatal("unpin must never be a way out of quarantine; that is local-disable's job")
	}
}

// TestLockStatusRacesPinAndUnpin runs lock.status against a node being pinned and
// unpinned. pinGenesis is a slice header and pinSource a string header, so an
// unsynchronized status read tears rather than merely going stale. Run under -race.
func TestLockStatusRacesPinAndUnpin(t *testing.T) {
	_, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < 300; i++ {
			if err := d.AdoptPin(genesis); err != nil {
				t.Errorf("AdoptPin: %v", err)
				return
			}
			if err := d.DropPin(); err != nil {
				t.Errorf("DropPin: %v", err)
				return
			}
		}
	}()
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				raw, err := d.handleLockStatus(context.Background(), nil)
				if err != nil {
					t.Errorf("lock.status: %v", err)
					return
				}
				st := raw.(api.LockStatusResult)
				if st.Pinned && !st.Quarantined && len(st.PinGenesis) != trustpin.GenesisLen {
					t.Errorf("torn pin read: %d bytes, want %d", len(st.PinGenesis), trustpin.GenesisLen)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestDropPinLeavesLocalDisableAlone(t *testing.T) {
	_, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	if err := d.LocalDisable(); err != nil {
		t.Fatalf("LocalDisable: %v", err)
	}

	if err := d.DropPin(); err != nil {
		t.Fatalf("DropPin: %v", err)
	}

	if !d.localDisabled() {
		t.Fatal("unpin and local-disable must stay orthogonal")
	}
}

// gatedTrustPeer is a fake trust peer that blocks at the end of a
// MethodTrustLogPull response until gate is closed. It forces detectUnpinnedChain's
// trip decision to happen after whatever the caller does before closing gate —
// reliably placing the test at the exact race window without depending on scheduler
// timing.
type gatedTrustPeer struct {
	chains [][]byte
	gate   chan struct{}
}

func (p *gatedTrustPeer) Call(method string, params, out any) error {
	if method == api.MethodTrustLogPull {
		if res, ok := out.(*api.TrustLogPullResult); ok {
			res.Chains = p.chains
		}
		<-p.gate
	}
	return nil
}

// TestAdoptPinAndDetectRaceInvariant asserts that "trust store non-nil → not
// quarantined" always holds when AdoptPin and detectUnpinnedChain race. The gated
// peer blocks detectUnpinnedChain's pull call until AdoptPin completes, so the trip
// decision always happens after the pin is adopted. Removing pinMu from the trip path
// makes every iteration fail.
func TestAdoptPinAndDetectRaceInvariant(t *testing.T) {
	chain, genesis := lockedChainForTest(t)

	const iters = 200
	for i := range iters {
		d := New()
		d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))

		gate := make(chan struct{})
		peer := &gatedTrustPeer{chains: [][]byte{chain}, gate: gate}

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.syncTrustOnce(peer)
		}()

		_ = d.AdoptPin(genesis) // adopt while detect is blocked at peer.Call
		close(gate)             // let detect proceed to the trip decision
		wg.Wait()

		if d.TrustStore() != nil && d.Quarantined() {
			t.Fatalf("iter %d: trust store is set but node is quarantined", i)
		}
	}
}

func trustpinFileForTest(d *Node) *trustpin.File {
	return trustpin.New(genesisHashPath(d.trustPath))
}
