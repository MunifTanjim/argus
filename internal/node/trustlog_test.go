package node

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/trustlog"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

// seedChain builds genesis[+authorize] and returns marshaled bytes + pieces.
func seedChain(t *testing.T, withDevice bool) (chain, head, device []byte, signer trustlog.SignerKey) {
	t.Helper()
	var err error
	signer, err = trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	head = log.Tip()
	device = bytes.Repeat([]byte{0x11}, 32)
	if withDevice {
		if err := log.AuthorizeDevice(device, signer); err != nil {
			t.Fatalf("AuthorizeDevice: %v", err)
		}
	}
	return trustlog.MarshalChain(log.Entries()), head, device, signer
}

func TestEnableTrustLogLoadsFromDisk(t *testing.T) {
	chain, head, device, _ := seedChain(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	if err := os.WriteFile(path, chain, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	if !d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("device from disk chain should be authorized")
	}
}

// A fakePeer records offered chains and serves a canned pull, standing in for the
// gateway uplink so runTrustSync can be exercised without a network.
type fakePeer struct {
	pullChain []byte
	offered   [][]byte
}

func (f *fakePeer) Call(method string, params, out any) error {
	switch method {
	case api.MethodTrustLogOffer:
		f.offered = append(f.offered, params.(api.TrustLogChain).Chain)
	case api.MethodTrustLogPull:
		var chains [][]byte
		if f.pullChain != nil {
			chains = [][]byte{f.pullChain}
		}
		*(out.(*api.TrustLogPullResult)) = api.TrustLogPullResult{Chains: chains}
	}
	return nil
}

func TestSyncOnceOffersAndIngests(t *testing.T) {
	// Node starts with a genesis-only chain; gateway offers a longer one.
	shortChain, head, device, signer := seedChain(t, false)
	log, err := trustlog.Load(mustUnmarshalNode(t, shortChain))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := log.AuthorizeDevice(device, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	longChain := trustlog.MarshalChain(log.Entries())

	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	if err := os.WriteFile(path, shortChain, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}

	fp := &fakePeer{pullChain: longChain}
	d.syncTrustOnce(fp) // pull+ingest the long one, then offer it

	if len(fp.offered) != 1 || !bytes.Equal(fp.offered[0], longChain) {
		t.Fatalf("expected our chain offered after ingest, got %d offers", len(fp.offered))
	}
	if !d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("device from pulled chain should be authorized")
	}
	// Persisted to disk on advance.
	onDisk, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(onDisk, longChain) {
		t.Fatalf("chain not persisted after ingest advance")
	}
}

func TestSyncRejectsRollback(t *testing.T) {
	// Disk has the long chain; a malicious gateway offers the short (stale) one.
	shortChain, head, device, signer := seedChain(t, false)
	log, err := trustlog.Load(mustUnmarshalNode(t, shortChain))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := log.AuthorizeDevice(device, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	longChain := trustlog.MarshalChain(log.Entries())

	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	if err := os.WriteFile(path, longChain, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}

	fp := &fakePeer{pullChain: shortChain}
	d.syncTrustOnce(fp)

	if !d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("rollback must be rejected; device should stay authorized")
	}
}

func mustUnmarshalNode(t *testing.T, b []byte) []trustlog.Entry {
	t.Helper()
	e, err := trustlog.UnmarshalChain(b)
	if err != nil {
		t.Fatalf("UnmarshalChain: %v", err)
	}
	return e
}

// Compile-time check: fakePeer satisfies trustCaller.
var _ trustCaller = (*fakePeer)(nil)

// Compile-time check: runTrustSync exists and takes *api.Peer (used with context).
var _ = (*Node).runTrustSync

func TestWriteGenesisHashRoundTrip(t *testing.T) {
	dir := t.TempDir()
	d := New()
	d.trustPath = filepath.Join(dir, "trustlog-chain")
	head := bytes.Repeat([]byte{0x7E}, 32)
	if err := d.writeGenesisHash(head); err != nil {
		t.Fatalf("writeGenesisHash: %v", err)
	}
	got, err := trustpin.New(filepath.Join(dir, "trustlog-genesis")).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(got, head) {
		t.Fatalf("Load = %x, want %x", got, head)
	}
	// Absent file: open mode is legitimate — must return (nil, nil).
	absent, aerr := trustpin.New(filepath.Join(dir, "absent")).Load()
	if aerr != nil {
		t.Fatalf("absent genesis should return nil error, got: %v", aerr)
	}
	if absent != nil {
		t.Fatal("absent genesis file should return nil head")
	}
	// Present but wrong-length: corrupt — must return an error (fail-closed).
	corruptPath := filepath.Join(dir, "trustlog-genesis-corrupt")
	if err := os.WriteFile(corruptPath, []byte("tooshort"), 0o600); err != nil {
		t.Fatalf("write corrupt genesis: %v", err)
	}
	_, cerr := trustpin.New(corruptPath).Load()
	if cerr == nil {
		t.Fatal("corrupt genesis file (wrong length) should return an error")
	}
}

func TestRunTrustSyncPollsLiveEnable(t *testing.T) {
	trustSyncInterval.Store(int64(10 * time.Millisecond))
	t.Cleanup(func() { trustSyncInterval.Store(int64(5 * time.Minute)) })

	// Build a chain + a fake peer serving it.
	chain, head, device, _ := seedChain(t, true) // genesis+authorize (existing helper)
	dir := t.TempDir()
	d := New()
	d.trustPath = filepath.Join(dir, "trustlog-chain")

	fp := &fakePeer{pullChain: chain}
	// runTrustSync must NOT early-return when trust is nil; start it, then enable.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.runTrustSyncLoop(ctx, fp) // test-only loop over trustCaller (see note)

	// Enable after the loop is already running.
	ss := trustlog.NewSyncStore(head)
	if err := d.activateTrust(ss, head, d.trustPath); err != nil {
		t.Fatalf("activateTrust: %v", err)
	}
	waitFor(t, "device authorized after live enable", func() bool {
		return d.TrustStore() != nil && d.TrustStore().DeviceAuthorized(device)
	})
}

func TestEnableTrustLogIgnoresCorruptDisk(t *testing.T) {
	_, head, _, _ := seedChain(t, false)
	dir := t.TempDir()
	path := filepath.Join(dir, "trustlog-chain")
	if err := os.WriteFile(path, []byte("garbage not a chain"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := New()
	if err := d.EnableTrustLog(head, path); err != nil {
		t.Fatalf("EnableTrustLog returned error on corrupt disk chain: %v", err)
	}
	if d.TrustStore() == nil {
		t.Fatal("TrustStore should be non-nil after EnableTrustLog")
	}
	if d.TrustStore().Tip() != nil {
		t.Fatal("Head should be nil when bad chain was ignored")
	}
	device := bytes.Repeat([]byte{0x22}, 32)
	if d.TrustStore().DeviceAuthorized(device) {
		t.Fatal("DeviceAuthorized should be false when no chain was loaded")
	}
}

// The second sync must not re-download a branch, and must not re-offer a chain the
// gateway confirms it holds.
func TestSyncIsQuietWhenNothingChanged(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	fp := branchFingerprint(chain)

	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	peer := &recordingTrustPeer{chains: [][]byte{chain}, fingerprints: [][]byte{fp[:]}}

	d.syncTrustOnce(peer)
	firstOffers := peer.offers
	d.syncTrustOnce(peer)

	if peer.offers != firstOffers {
		t.Fatalf("offers went %d -> %d; a chain the gateway already holds must not be re-offered", firstOffers, peer.offers)
	}
	if len(peer.lastKnown) == 0 {
		t.Fatal("the second pull must send the fingerprints already seen")
	}
}

// If the gateway loses the branch, the node must notice and re-offer.
func TestNodeReoffersWhenGatewayForgets(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	fp := branchFingerprint(chain)
	peer := &recordingTrustPeer{chains: [][]byte{chain}, fingerprints: [][]byte{fp[:]}}
	d.syncTrustOnce(peer)
	before := peer.offers

	peer.fingerprints = nil // gateway restarted: it holds nothing
	peer.chains = nil
	d.syncTrustOnce(peer)

	if peer.offers <= before {
		t.Fatal("a gateway that no longer lists our fingerprint must trigger a re-offer")
	}
}

// An old gateway returns no Fingerprints at all. That is not "holds nothing".
func TestOldGatewayStillGetsOffers(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	peer := &recordingTrustPeer{chains: [][]byte{chain}, legacy: true}

	d.syncTrustOnce(peer)
	d.syncTrustOnce(peer)

	if peer.offers < 2 {
		t.Fatalf("offers = %d; against a gateway with no Fingerprints the node must offer unconditionally", peer.offers)
	}
}

type recordingTrustPeer struct {
	mu           sync.Mutex
	chains       [][]byte
	fingerprints [][]byte
	legacy       bool // omit Fingerprints entirely, like a gateway that predates them
	offers       int
	pulls        int
	lastKnown    [][]byte
}

func (p *recordingTrustPeer) Call(method string, params, out any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch method {
	case api.MethodTrustLogOffer:
		p.offers++
	case api.MethodTrustLogPull:
		p.pulls++
		if pp, ok := params.(api.TrustLogPullParams); ok {
			p.lastKnown = pp.Known
		}
		res, ok := out.(*api.TrustLogPullResult)
		if !ok {
			return nil
		}
		res.Chains = p.chains
		if !p.legacy {
			res.Fingerprints = p.fingerprints
		}
	}
	return nil
}

func TestTriggeredPullIsRateLimited(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	fp := branchFingerprint(chain)
	peer := &recordingTrustPeer{chains: [][]byte{chain}, fingerprints: [][]byte{fp[:]}}
	d.setTriggerPeerForTest(peer)

	for i := 0; i < 20; i++ {
		d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
	}
	// Wait for any in-flight pull goroutine to complete before asserting.
	waitFor(t, "no pull in flight", func() bool {
		d.triggerMu.Lock()
		defer d.triggerMu.Unlock()
		return !d.triggeredPullInFlight
	})

	peer.mu.Lock()
	pulls := peer.pulls
	peer.mu.Unlock()
	if pulls > 1 {
		t.Fatalf("pulls = %d; a notification flood must not amplify into pulls", pulls)
	}
}

func TestNotificationTriggersAPull(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	fp := branchFingerprint(chain)
	peer := &recordingTrustPeer{chains: [][]byte{chain}, fingerprints: [][]byte{fp[:]}}
	d.setTriggerPeerForTest(peer)

	d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
	waitFor(t, "one pull from notification", func() bool {
		peer.mu.Lock()
		defer peer.mu.Unlock()
		return peer.pulls >= 1
	})

	peer.mu.Lock()
	pulls := peer.pulls
	peer.mu.Unlock()
	if pulls != 1 {
		t.Fatalf("pulls = %d, want exactly 1", pulls)
	}
}

// TestTriggeredPullResumesAfterWindow proves the rate limiter is not a one-shot
// block: once the window expires a fresh notification triggers another pull.
func TestTriggeredPullResumesAfterWindow(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	fp := branchFingerprint(chain)
	peer := &recordingTrustPeer{chains: [][]byte{chain}, fingerprints: [][]byte{fp[:]}}
	d.setTriggerPeerForTest(peer)

	// First notification: should trigger a pull.
	d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
	waitFor(t, "first pull completes", func() bool {
		d.triggerMu.Lock()
		defer d.triggerMu.Unlock()
		return !d.triggeredPullInFlight
	})
	peer.mu.Lock()
	after1 := peer.pulls
	peer.mu.Unlock()
	if after1 != 1 {
		t.Fatalf("after first notify: pulls = %d, want 1", after1)
	}

	// Second notification within the window: must be suppressed.
	d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
	waitFor(t, "no second pull in flight", func() bool {
		d.triggerMu.Lock()
		defer d.triggerMu.Unlock()
		return !d.triggeredPullInFlight
	})
	peer.mu.Lock()
	after2 := peer.pulls
	peer.mu.Unlock()
	if after2 > 1 {
		t.Fatalf("second notify (within window) triggered a pull: pulls = %d", after2)
	}

	// Backdate the timestamp past the window; the next notification must pull.
	d.triggerMu.Lock()
	d.lastTriggeredPull = time.Now().Add(-(minTriggeredPullInterval + time.Second))
	d.triggerMu.Unlock()

	d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
	waitFor(t, "pull after window expiry", func() bool {
		peer.mu.Lock()
		defer peer.mu.Unlock()
		return peer.pulls >= 2
	})
}

// TestNotificationConvergesChainAndKeepsUplinkAlive is an integration test: a
// real node dials a real gateway, a second peer offers a branch, the resulting
// trustlog.changed notification triggers a pull off the read loop, and the chain
// converges. The uplink must remain alive throughout — this is the test that
// catches the read-loop deadlock (Critical 1).
func TestNotificationConvergesChainAndKeepsUplinkAlive(t *testing.T) {
	// Long sync interval: convergence must come from the notification, not the timer.
	SetTrustSyncIntervalForTest(10 * time.Minute)
	t.Cleanup(func() { SetTrustSyncIntervalForTest(5 * time.Minute) })

	chain, genesis := lockedChainForTest(t)

	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	d := New()
	d.SetIdentity("live-node", "live-box")
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.ConnectGateway(ctx, wsURL(hs.URL)+"/node", "", nil)

	waitFor(t, "uplink established", func() bool { return d.activeUplink.Load() != nil })

	// A second node peer offers the chain; the gateway inserts it and notifies all
	// node peers including the live node.
	offerer, err := api.DialWSPeer(context.Background(), wsURL(hs.URL)+"/node", "", nil, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				return api.IdentifyResult{ID: "offerer-node"}, nil
			}
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "not found"}
		},
	})
	if err != nil {
		t.Fatalf("dial offerer: %v", err)
	}
	defer offerer.Close()

	if err := offerer.Call(api.MethodTrustLogOffer, api.TrustLogChain{Chain: chain}, nil); err != nil {
		t.Fatalf("offer: %v", err)
	}

	// The notification-driven pull must converge the chain.
	waitFor(t, "trust store converged", func() bool {
		ts := d.TrustStore()
		return ts != nil && ts.Tip() != nil
	})

	// The uplink must still be alive — a read-loop deadlock would have killed it.
	if d.activeUplink.Load() == nil {
		t.Fatal("uplink died after receiving trustlog.changed notification (read-loop deadlock?)")
	}
}
