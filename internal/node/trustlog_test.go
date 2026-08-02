package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// A fakePeer serves a canned chain as entries on trustlog.sync and records pushed
// entries (reassembled into chains) for assertions, standing in for the gateway
// uplink so runTrustSync can be exercised without a network.
type fakePeer struct {
	pullChain []byte
	offered   [][]byte // chains reconstructed from MethodTrustLogPush calls
}

func (f *fakePeer) Call(method string, params, out any) error {
	switch method {
	case api.MethodTrustLogSync:
		res := out.(*api.TrustLogSyncResult)
		if f.pullChain != nil {
			if raw, err := trustlog.ChainEntries(f.pullChain); err == nil {
				res.Entries = raw
			}
		}
	case api.MethodTrustLogPush:
		entries := params.(api.TrustLogPushParams).Entries
		for _, chain := range trustlog.AssembleChains(entries) {
			f.offered = append(f.offered, chain)
		}
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
	d.syncTrustOnce(fp) // pull+ingest the long one

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
	// Wait the loop out before TempDir cleanup: a persist still in flight when the
	// context ends would otherwise recreate the chain file under RemoveAll.
	loopDone := make(chan struct{})
	t.Cleanup(func() { cancel(); <-loopDone })
	go func() {
		defer close(loopDone)
		d.runTrustSyncLoop(ctx, fp) // test-only loop over trustCaller (see note)
	}()

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

// The second sync must not push a chain the gateway already holds (Want is empty),
// and must send the known entry hashes so the gateway can compute its delta.
func TestSyncIsQuietWhenNothingChanged(t *testing.T) {
	chain, genesis := lockedChainForTest(t)

	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	peer := &recordingTrustPeer{chains: [][]byte{chain}} // Want is nil → no push

	d.syncTrustOnce(peer)
	firstOffers := peer.offers
	d.syncTrustOnce(peer)

	if peer.offers != firstOffers {
		t.Fatalf("offers went %d -> %d; gateway returned no Want so node must not push", firstOffers, peer.offers)
	}
	if len(peer.lastKnown) == 0 {
		t.Fatal("the second sync must send the known entry hashes")
	}
}

// If the gateway loses the branch and sets Want, the node must push again.
func TestNodeReoffersWhenGatewayForgets(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	peer := &recordingTrustPeer{chains: [][]byte{chain}} // Want is nil → no push
	d.syncTrustOnce(peer)
	before := peer.offers

	// Gateway restarted and signals it wants our branch back (real entry hashes).
	hashes, _ := d.knownHashes()
	peer.chains = nil
	peer.want = hashes
	d.syncTrustOnce(peer)

	if peer.offers <= before {
		t.Fatal("a gateway that sets Want must trigger a push from the node")
	}
}

// The node pushes whenever Want is set, and only then — no unconditional offers.
func TestNodePushesWhenGatewayWants(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	// First sync with no Want: ingest the chain so the store has bytes to push.
	converge := &recordingTrustPeer{chains: [][]byte{chain}}
	d.syncTrustOnce(converge)

	// Now set Want to actual entry hashes the node holds: every subsequent sync must trigger a push.
	hashes, _ := d.knownHashes()
	peer := &recordingTrustPeer{chains: [][]byte{chain}, want: hashes}
	d.syncTrustOnce(peer)
	d.syncTrustOnce(peer)

	if peer.offers < 2 {
		t.Fatalf("offers = %d; a gateway that sets Want on every sync must get a push each time", peer.offers)
	}
}

type recordingTrustPeer struct {
	mu        sync.Mutex
	chains    [][]byte             // served as entries on MethodTrustLogSync
	want      [][]byte             // returned as Want in sync response (signals the node to push)
	roster    []api.NodeDescriptor // served by nodes.list
	offers    int                  // MethodTrustLogPush calls
	pulls     int                  // MethodTrustLogSync calls
	rosters   int
	lastKnown [][]byte
}

func (p *recordingTrustPeer) rosterCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rosters
}

func (p *recordingTrustPeer) Call(method string, params, out any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch method {
	case api.MethodNodesList:
		p.rosters++
		if res, ok := out.(*api.NodesListResult); ok {
			res.Nodes = p.roster
		}
	case api.MethodTrustLogPush:
		p.offers++
	case api.MethodTrustLogSync:
		p.pulls++
		if pp, ok := params.(api.TrustLogSyncParams); ok {
			p.lastKnown = pp.Known
		}
		res, ok := out.(*api.TrustLogSyncResult)
		if !ok {
			return nil
		}
		var entries [][]byte
		for _, c := range p.chains {
			if raw, err := trustlog.ChainEntries(c); err == nil {
				entries = append(entries, raw...)
			}
		}
		res.Entries = entries
		res.Want = p.want
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
	peer := &recordingTrustPeer{chains: [][]byte{chain}}
	d.setTriggerPeerForTest(peer)

	for i := 0; i < 20; i++ {
		d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
	}
	t.Cleanup(d.trustPullTrigger.stop) // the single deferred pull outlives this test
	waitFor(t, "the immediate pull happens", func() bool {
		peer.mu.Lock()
		defer peer.mu.Unlock()
		return peer.pulls >= 1
	})
	time.Sleep(200 * time.Millisecond) // still well inside the window

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
	peer := &recordingTrustPeer{chains: [][]byte{chain}}
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
	peer := &recordingTrustPeer{chains: [][]byte{chain}}
	d.setTriggerPeerForTest(peer)

	// First notification: should trigger a pull.
	d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
	waitFor(t, "first pull completes", func() bool {
		return d.trustPullTrigger.idle()
	})
	peer.mu.Lock()
	after1 := peer.pulls
	peer.mu.Unlock()
	if after1 != 1 {
		t.Fatalf("after first notify: pulls = %d, want 1", after1)
	}

	// Backdate past the window, then notify: must pull straight away.
	d.trustPullTrigger.backdate(minTriggeredPullInterval() + time.Second)

	d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
	waitFor(t, "pull after window expiry", func() bool {
		peer.mu.Lock()
		defer peer.mu.Unlock()
		return peer.pulls >= 2
	})
}

// TestSuppressedNotificationIsDeferredNotDropped is the regression guard for the
// operator sequence `lock sign d1` then `lock revoke d2` seconds later: the second
// notification lands inside the rate-limit window and must still be acted on when
// the window closes. Dropping it costs the whole 5-minute backstop.
func TestSuppressedNotificationIsDeferredNotDropped(t *testing.T) {
	setTriggeredPullIntervalForTest(200 * time.Millisecond)
	t.Cleanup(func() { setTriggeredPullIntervalForTest(5 * time.Second) })

	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	peer := &recordingTrustPeer{chains: [][]byte{chain}}
	d.setTriggerPeerForTest(peer)

	d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
	waitFor(t, "first pull completes", func() bool {
		return d.trustPullTrigger.idle()
	})

	// Second change, inside the window: suppressed now, owed later.
	d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
	peer.mu.Lock()
	immediate := peer.pulls
	peer.mu.Unlock()
	if immediate != 1 {
		t.Fatalf("pulls = %d immediately after the second notify; the window must still hold", immediate)
	}

	// No further notification arrives: the deferred pull has to fire by itself.
	waitFor(t, "deferred pull fires without another notification", func() bool {
		peer.mu.Lock()
		defer peer.mu.Unlock()
		return peer.pulls >= 2
	})
}

// TestNotificationFloodCoalescesToOneDeferredPull holds the rate limit in place
// now that suppression defers instead of drops: a flood must collapse into a
// single owed pull, not a queue of them.
func TestNotificationFloodCoalescesToOneDeferredPull(t *testing.T) {
	setTriggeredPullIntervalForTest(100 * time.Millisecond)
	t.Cleanup(func() { setTriggeredPullIntervalForTest(5 * time.Second) })

	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	peer := &recordingTrustPeer{chains: [][]byte{chain}}
	d.setTriggerPeerForTest(peer)

	for i := 0; i < 50; i++ {
		d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
	}
	waitFor(t, "deferred pull fires", func() bool {
		peer.mu.Lock()
		defer peer.mu.Unlock()
		return peer.pulls >= 2
	})
	time.Sleep(300 * time.Millisecond) // three more windows: a queue would drain here

	peer.mu.Lock()
	pulls := peer.pulls
	peer.mu.Unlock()
	if pulls > 2 {
		t.Fatalf("pulls = %d; 50 notifications must coalesce into one deferred pull, not a queue", pulls)
	}
}

// TestNotificationPullSkipsTheConsistencyTick pins the protocol promise that a
// forged trustlog.changed cannot clock the node's N=2 equivocation guard: the
// notification path pulls and ingests, but only the node's own timer advances
// peerBeaconMiss.
func TestNotificationPullSkipsTheConsistencyTick(t *testing.T) {
	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	peer := &recordingTrustPeer{chains: [][]byte{chain}}
	d.setTriggerPeerForTest(peer)
	// Converge first so the node holds a chain to cross-check against.
	d.syncTrustOnce(peer)

	// A peer beacon whose tip is nowhere in the node's chain: one consistency tick
	// records a miss, two set the flag.
	bPub, bPriv := genBeaconKeyPair(t)
	divergent := api.SignBeacon(bPriv, bPub, bytes.Repeat([]byte{0xde}, 32), 1, 1)
	d.peerBeaconMu.Lock()
	d.peerBeaconPubs = map[string]bool{string(bPub): true}
	d.peerBeacons = map[string]api.Beacon{string(bPub): divergent}
	d.peerBeaconMu.Unlock()

	for i := 0; i < 5; i++ {
		d.trustPullTrigger.backdate(minTriggeredPullInterval() + time.Second)
		d.onGatewayNotify(api.Notification{Method: api.MethodTrustLogChanged})
		waitFor(t, "triggered pull completes", func() bool {
			return d.trustPullTrigger.idle()
		})
	}

	d.peerBeaconMu.Lock()
	miss := d.peerBeaconMiss[string(bPub)]
	d.peerBeaconMu.Unlock()
	if miss != nil {
		t.Fatalf("gateway notifications advanced peerBeaconMiss to %d; only the node's own tick may", miss.misses)
	}
	if d.equivocation.Load() {
		t.Fatal("a gateway that forges trustlog.changed must not be able to drive the equivocation flag")
	}
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

	entries, cerr := trustlog.ChainEntries(chain)
	if cerr != nil {
		t.Fatalf("ChainEntries: %v", cerr)
	}
	if err := offerer.Call(api.MethodTrustLogPush, api.TrustLogPushParams{Entries: entries}, nil); err != nil {
		t.Fatalf("push: %v", err)
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

// TestRosterSyncRunsOnItsOwnClock pins peer-beacon attribution to a clock of its
// own. Tied to the trust tick it inherited the 5-minute backstop times ten, and a
// newly joined node had every beacon rejected as "unknown beacon pub" for the
// better part of an hour.
func TestRosterSyncRunsOnItsOwnClock(t *testing.T) {
	SetTrustSyncIntervalForTest(10 * time.Minute) // no trust tick fires in this test
	setRosterSyncIntervalForTest(20 * time.Millisecond)
	t.Cleanup(func() {
		SetTrustSyncIntervalForTest(5 * time.Minute)
		setRosterSyncIntervalForTest(5 * time.Minute)
	})

	peer := &recordingTrustPeer{}
	d := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.runTrustSyncLoop(ctx, peer)

	waitFor(t, "roster refreshes without a trust tick", func() bool {
		return peer.rosterCount() >= 3 // one at startup plus two from its own ticker
	})
}

// nodeWithTrustStore returns a Node pinned to a genesis-only chain plus the
// marshaled chain bytes. The store is active but holds no ingested chain bytes
// until ingestForTest is called — simulating a node that pinned but has not yet
// pulled from the gateway.
func nodeWithTrustStore(t *testing.T) (*Node, []byte) {
	t.Helper()
	chain, genesis := lockedChainForTest(t)
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "trustlog-chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	return d, chain
}

// ingestForTest ingests chain into d's trust store and retains the entries,
// mirroring what pullTrustOnce + syncTrustChains do so knownHashes is populated.
func ingestForTest(d *Node, chain []byte) error {
	st := d.trust.Load()
	if st == nil {
		return fmt.Errorf("no trust store")
	}
	if _, err := st.Ingest(chain); err != nil {
		return err
	}
	raw, err := trustlog.ChainEntries(chain)
	if err != nil {
		return err
	}
	d.pinMu.Lock()
	if d.retainedEntries == nil {
		d.retainedEntries = trustlog.NewEntryStore()
	}
	d.retainedEntries.PutAll(raw)
	d.pinMu.Unlock()
	return nil
}

func TestSyncTrustChainsAssemblesGatewayEntries(t *testing.T) {
	d, chain := nodeWithTrustStore(t)

	raw, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}

	var gotMethod []string
	peer := &fakeTrustCaller{
		fn: func(method string, params, result any) error {
			gotMethod = append(gotMethod, method)
			if method == api.MethodTrustLogSync {
				res := result.(*api.TrustLogSyncResult)
				res.Entries = raw
				return nil
			}
			return nil
		},
	}

	chains, ok := d.syncTrustChains(peer)
	if !ok {
		t.Fatalf("syncTrustChains reported failure")
	}
	if len(chains) != 1 || !bytes.Equal(chains[0], chain) {
		t.Fatalf("assembled %d chains, want the original", len(chains))
	}
	if gotMethod[0] != api.MethodTrustLogSync {
		t.Fatalf("first call was %q, want trustlog.sync", gotMethod[0])
	}
}

func TestSyncTrustChainsPushesWhatTheGatewayWants(t *testing.T) {
	d, chain := nodeWithTrustStore(t)
	if err := ingestForTest(d, chain); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	var pushed [][]byte
	peer := &fakeTrustCaller{
		fn: func(method string, params, result any) error {
			switch method {
			case api.MethodTrustLogSync:
				res := result.(*api.TrustLogSyncResult)
				hashes, _ := d.knownHashes()
				res.Want = hashes
			case api.MethodTrustLogPush:
				pushed = params.(api.TrustLogPushParams).Entries
			}
			return nil
		},
	}

	if _, ok := d.syncTrustChains(peer); !ok {
		t.Fatalf("syncTrustChains reported failure")
	}
	want, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	if len(pushed) != len(want) {
		t.Fatalf("pushed %d entries, want %d", len(pushed), len(want))
	}
}

// TestSyncTrustChainsUnplacedEntriesAreWarned covers the case where the gateway
// returns an orphaned entry whose ancestors are not held by the node. After
// removing the re-sync recovery, the node issues exactly one sync and logs a
// warning rather than retrying.
func TestSyncTrustChainsUnplacedEntriesAreWarned(t *testing.T) {
	chain, _, _, _ := seedChain(t, true) // genesis + authorize → 2 entries
	raw, err := trustlog.ChainEntries(chain)
	if err != nil || len(raw) < 2 {
		t.Fatalf("need ≥2 entries: %v", err)
	}
	// Serve only the non-genesis entry; without the genesis it cannot be placed.
	orphan := raw[1]

	calls := 0
	peer := &fakeTrustCaller{
		fn: func(method string, params, out any) error {
			if method != api.MethodTrustLogSync {
				return nil
			}
			calls++
			out.(*api.TrustLogSyncResult).Entries = [][]byte{orphan}
			return nil
		},
	}

	d := New()

	_, ok := d.syncTrustChains(peer)
	if !ok {
		t.Fatalf("syncTrustChains reported failure")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 sync (no retry after removal), got %d", calls)
	}
}

// TestSyncTrustChainsRetainsRejectedBranchEntries covers the loss scenario: the
// node received branch Y (a fork that lost fork-choice to X), recorded its head
// in seenBranches, then discarded its entries. The gateway later serves only an
// extension entry D whose Prev points to the tip of Y. Without retention, D cannot
// be placed. After the fix the node retains Y's entries so assembly succeeds.
func TestSyncTrustChainsRetainsRejectedBranchEntries(t *testing.T) {
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}

	genLog, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisEntries := genLog.Entries()

	logX, err := trustlog.Load(genesisEntries)
	if err != nil {
		t.Fatalf("Load X: %v", err)
	}
	if err := logX.AuthorizeDevice(bytes.Repeat([]byte{0xAA}, 32), signer); err != nil {
		t.Fatalf("AuthorizeDevice X: %v", err)
	}
	chainX := trustlog.MarshalChain(logX.Entries())

	logY, err := trustlog.Load(genesisEntries)
	if err != nil {
		t.Fatalf("Load Y: %v", err)
	}
	if err := logY.AuthorizeDevice(bytes.Repeat([]byte{0xBB}, 32), signer); err != nil {
		t.Fatalf("AuthorizeDevice Y: %v", err)
	}
	chainY := trustlog.MarshalChain(logY.Entries())

	// D extends Y with a third device — the gateway serves only this entry.
	logD, err := trustlog.Load(logY.Entries())
	if err != nil {
		t.Fatalf("Load D: %v", err)
	}
	if err := logD.AuthorizeDevice(bytes.Repeat([]byte{0xCC}, 32), signer); err != nil {
		t.Fatalf("AuthorizeDevice D: %v", err)
	}
	rawD, err := trustlog.ChainEntries(trustlog.MarshalChain(logD.Entries()))
	if err != nil {
		t.Fatalf("ChainEntries D: %v", err)
	}
	dEntry := rawD[len(rawD)-1]

	// Build a node pinned to this test's own genesis.
	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "trustlog-chain"))
	genesis := genLog.Tip()
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	if err := ingestForTest(d, chainX); err != nil {
		t.Fatalf("ingest X: %v", err)
	}
	// Simulate receiving-and-rejecting Y: retain its entries without ingesting.
	rawY, err := trustlog.ChainEntries(chainY)
	if err != nil {
		t.Fatalf("ChainEntries Y: %v", err)
	}
	d.pinMu.Lock()
	if d.retainedEntries == nil {
		d.retainedEntries = trustlog.NewEntryStore()
	}
	d.retainedEntries.PutAll(rawY)
	d.pinMu.Unlock()

	peer := &fakeTrustCaller{
		fn: func(method string, params, out any) error {
			if method == api.MethodTrustLogSync {
				out.(*api.TrustLogSyncResult).Entries = [][]byte{dEntry}
			}
			return nil
		},
	}

	chains, ok := d.syncTrustChains(peer)
	if !ok {
		t.Fatalf("syncTrustChains reported failure")
	}

	// After the fix, node retains Y's entries so [genesis, devY, dEntry] assembles.
	found := false
	for _, c := range chains {
		if entries, err := trustlog.ChainEntries(c); err == nil && len(entries) >= 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("extension of rejected branch Y was lost: no assembled chain of length ≥3")
	}
}

// TestSyncTrustChainsIssuesExactlyOneSync is a regression guard: a complete,
// fully-connected chain must resolve in one sync with no recovery re-sync.
func TestSyncTrustChainsIssuesExactlyOneSync(t *testing.T) {
	chain, _, _, _ := seedChain(t, true)
	raw, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}

	calls := 0
	peer := &fakeTrustCaller{
		fn: func(method string, params, out any) error {
			if method != api.MethodTrustLogSync {
				return nil
			}
			calls++
			out.(*api.TrustLogSyncResult).Entries = raw
			return nil
		},
	}

	d := New()
	chains, ok := d.syncTrustChains(peer)
	if !ok {
		t.Fatalf("syncTrustChains reported failure")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 sync, got %d", calls)
	}
	if len(chains) != 1 || !bytes.Equal(chains[0], chain) {
		t.Fatalf("assembled chain does not match original")
	}
}

// TestSyncTrustChainsNoRetryOnHappyPath asserts that when all returned entries
// assemble cleanly into complete chains, exactly one sync is issued — the retry
// must not fire on the happy path.
func TestSyncTrustChainsNoRetryOnHappyPath(t *testing.T) {
	chain, _, _, _ := seedChain(t, true)
	raw, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}

	calls := 0
	peer := &fakeTrustCaller{
		fn: func(method string, params, out any) error {
			if method != api.MethodTrustLogSync {
				return nil
			}
			calls++
			res := out.(*api.TrustLogSyncResult)
			res.Entries = raw
			return nil
		},
	}

	d := New()

	chains, ok := d.syncTrustChains(peer)
	if !ok {
		t.Fatalf("syncTrustChains reported failure")
	}
	if calls != 1 {
		t.Fatalf("happy path must issue exactly 1 sync, got %d", calls)
	}
	if len(chains) != 1 || !bytes.Equal(chains[0], chain) {
		t.Fatalf("assembled chain does not match original")
	}
}

// TestKnownHashesReflectsCeiling pins the node-level invariant: when the entry
// store is at its ceiling, a sync that receives new entries must not include the
// refused ones in the next Known offer. Driven through syncTrustChains so it
// tests the actual node code path, not just EntryStore internals.
func TestKnownHashesReflectsCeiling(t *testing.T) {
	prev := trustlog.SetMaxRetainedEntriesForTest(1)
	t.Cleanup(func() { trustlog.SetMaxRetainedEntriesForTest(prev) })

	// chainA fills the ceiling; chainB's entry must be refused.
	chainA, genesisA := lockedChainForTest(t) // 1 genesis entry
	chainB, _ := lockedChainForTest(t)        // 1 genesis entry, different genesis

	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "chain"))
	if err := d.AdoptPin(genesisA); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}

	// First sync delivers chainA: fills the store to ceiling (1 entry).
	d.syncTrustChains(&fakeTrustCaller{fn: func(method string, params, result any) error {
		if method == api.MethodTrustLogSync {
			raw, _ := trustlog.ChainEntries(chainA)
			result.(*api.TrustLogSyncResult).Entries = raw
		}
		return nil
	}})
	hashesBefore, _ := d.knownHashes()
	if len(hashesBefore) != 1 {
		t.Fatalf("after first sync: expected 1 hash, got %d", len(hashesBefore))
	}

	// Second sync delivers chainB: ceiling is full, the entry must be refused.
	d.syncTrustChains(&fakeTrustCaller{fn: func(method string, params, result any) error {
		if method == api.MethodTrustLogSync {
			raw, _ := trustlog.ChainEntries(chainB)
			result.(*api.TrustLogSyncResult).Entries = raw
		}
		return nil
	}})
	hashesAfter, _ := d.knownHashes()
	if len(hashesAfter) > len(hashesBefore) {
		t.Fatalf("at ceiling: refused entry must not appear in known; got %d hashes, want %d",
			len(hashesAfter), len(hashesBefore))
	}
}

// TestPushWantedIgnoresUnknownHashes verifies that a Want naming a hash the node
// does not hold produces no trustlog.push call. This pins the behaviour change
// from pushHeldEntries (which pushed the whole chain) to pushWanted (exact match).
func TestPushWantedIgnoresUnknownHashes(t *testing.T) {
	d, chain := nodeWithTrustStore(t)
	if err := ingestForTest(d, chain); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	pushed := false
	peer := &fakeTrustCaller{fn: func(method string, params, result any) error {
		switch method {
		case api.MethodTrustLogSync:
			// Want a hash the node definitely does not hold.
			result.(*api.TrustLogSyncResult).Want = [][]byte{bytes.Repeat([]byte{0x99}, 32)}
		case api.MethodTrustLogPush:
			pushed = true
		}
		return nil
	}}
	d.syncTrustChains(peer)

	if pushed {
		t.Fatal("Want naming an unknown hash must not trigger a push; pushHeldEntries regression would send the whole chain")
	}
}

// TestSyncTrustChainsOffersEveryRetainedHash checks that syncTrustChains sends
// every retained entry hash in Known, not just branch tips.
func TestSyncTrustChainsOffersEveryRetainedHash(t *testing.T) {
	d, chain := nodeWithTrustStore(t)
	if err := ingestForTest(d, chain); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	var got api.TrustLogSyncParams
	peer := &fakeTrustCaller{fn: func(method string, params, result any) error {
		if method == api.MethodTrustLogSync {
			got = params.(api.TrustLogSyncParams)
		}
		return nil
	}}
	d.syncTrustChains(peer)

	want, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	if len(got.Known) != len(want) {
		t.Fatalf("offered %d hashes, want %d", len(got.Known), len(want))
	}
	if got.Truncated {
		t.Fatalf("small store must not truncate")
	}
}

// TestRejectedBranchIsNotRefetched checks that a same-genesis fork that loses
// fork-choice is not re-downloaded on every tick. The fork's entries must appear
// in Known after the first sync so the gateway sees the node already holds them.
func TestRejectedBranchIsNotRefetched(t *testing.T) {
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	genLog, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisEntries := genLog.Entries()
	genesis := genLog.Tip()

	// winner: the fork this node ingests and adopts.
	logA, err := trustlog.Load(genesisEntries)
	if err != nil {
		t.Fatalf("Load A: %v", err)
	}
	if err := logA.AuthorizeDevice(bytes.Repeat([]byte{0xAA}, 32), signer); err != nil {
		t.Fatalf("AuthorizeDevice A: %v", err)
	}
	winner := trustlog.MarshalChain(logA.Entries())

	// rejected: a same-genesis fork that Ingest rejects on fork-choice.
	logB, err := trustlog.Load(genesisEntries)
	if err != nil {
		t.Fatalf("Load B: %v", err)
	}
	if err := logB.AuthorizeDevice(bytes.Repeat([]byte{0xBB}, 32), signer); err != nil {
		t.Fatalf("AuthorizeDevice B: %v", err)
	}
	rejected := trustlog.MarshalChain(logB.Entries())

	d := New()
	d.SetTrustChainPath(filepath.Join(t.TempDir(), "trustlog-chain"))
	if err := d.AdoptPin(genesis); err != nil {
		t.Fatalf("AdoptPin: %v", err)
	}
	if err := ingestForTest(d, winner); err != nil {
		t.Fatalf("ingest winner: %v", err)
	}

	calls := 0
	var second api.TrustLogSyncParams
	peer := &fakeTrustCaller{fn: func(method string, params, result any) error {
		if method != api.MethodTrustLogSync {
			return nil
		}
		calls++
		p := params.(api.TrustLogSyncParams)
		if calls == 1 {
			raw, err := trustlog.ChainEntries(rejected)
			if err != nil {
				t.Fatalf("ChainEntries: %v", err)
			}
			result.(*api.TrustLogSyncResult).Entries = raw
			return nil
		}
		second = p
		return nil
	}}

	d.syncTrustChains(peer)
	d.syncTrustChains(peer)

	rejectedRaw, err := trustlog.ChainEntries(rejected)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	offered := map[string]bool{}
	for _, h := range second.Known {
		offered[string(h)] = true
	}
	for _, r := range rejectedRaw {
		e, uerr := trustlog.UnmarshalEntry(r)
		if uerr != nil {
			t.Fatalf("UnmarshalEntry: %v", uerr)
		}
		if !offered[string(trustlog.HashEntry(&e))] {
			t.Fatalf("second offer omitted a rejected-branch entry; it would be re-downloaded forever")
		}
	}
}

func TestUnplacedWarningSuppressedWhenUnchanged(t *testing.T) {
	// Build a chain the node knows (chain1) and pin to it.
	chain1, head1, _, _ := seedChain(t, false)
	dir := t.TempDir()
	path := filepath.Join(dir, "chain")
	if err := os.WriteFile(path, chain1, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := New()
	if err := d.EnableTrustLog(head1, path); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}

	// Build an orphan chain (separate signer, drop genesis → all entries unplaced).
	signer2, _ := trustlog.GenerateSigner()
	log2, _ := trustlog.NewGenesis([][]byte{signer2.Public}, signer2, nil)
	dev := bytes.Repeat([]byte{0xBB}, 32)
	_ = log2.AuthorizeDevice(dev, signer2)
	full2 := trustlog.MarshalChain(log2.Entries())
	allEntries, _ := trustlog.UnmarshalChain(full2)
	orphanChain1 := trustlog.MarshalChain(allEntries[1:]) // 1 orphan entry (drop genesis)

	var logs syncBuffer
	d.SetLogger(debugLogger(&logs))

	fp := &fakePeer{pullChain: orphanChain1}

	// First sync: unplaced count changed from 0 → 1; warning must appear.
	d.syncTrustChains(fp)
	if !strings.Contains(logs.String(), "unplaced") {
		t.Fatal("first call should log the unplaced warning")
	}

	// Second sync: same unplaced count (1); warning must NOT repeat.
	logs = syncBuffer{}
	d.SetLogger(debugLogger(&logs))
	d.syncTrustChains(fp)
	if strings.Contains(logs.String(), "unplaced") {
		t.Fatal("second call with identical unplaced count must not log")
	}

	// Sync with no orphans (count returns to 0): no log, but memory resets.
	fp.pullChain = nil
	logs = syncBuffer{}
	d.SetLogger(debugLogger(&logs))
	d.syncTrustChains(fp)

	// Recurrence: same orphan count as the first wave (1); must warn again.
	fp.pullChain = orphanChain1
	logs = syncBuffer{}
	d.SetLogger(debugLogger(&logs))
	d.syncTrustChains(fp)
	if !strings.Contains(logs.String(), "unplaced") {
		t.Fatal("recurrence after zero must log again")
	}
}

// TestFirstSyncOfferIncludesOwnChain is the reboot scenario: the node holds a
// verified chain (loaded from disk by EnableTrustLog) but its entry store is
// fresh. Without the seed step the first offer is empty, and the gateway
// responds with Want=[], so the chain is republished only on the NEXT tick —
// up to trustSyncInterval away. With the fix the chain is seeded into the
// entry store before the offer is built, so the hashes appear on tick 1.
func TestFirstSyncOfferIncludesOwnChain(t *testing.T) {
	chain, genesis := lockedChainForTest(t)

	dir := t.TempDir()
	chainPath := filepath.Join(dir, "trustlog-chain")
	if err := os.WriteFile(chainPath, chain, 0o600); err != nil {
		t.Fatalf("write chain: %v", err)
	}

	d := New()
	if err := d.EnableTrustLog(genesis, chainPath); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	// After EnableTrustLog, trust store has the chain but retainedEntries is empty.

	var firstKnown [][]byte
	peer := &fakeTrustCaller{
		fn: func(method string, params, out any) error {
			if method == api.MethodTrustLogSync {
				if pp, ok := params.(api.TrustLogSyncParams); ok && firstKnown == nil {
					firstKnown = pp.Known
				}
			}
			return nil
		},
	}

	d.syncTrustChains(peer)

	wantRaw, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	known := make(map[string]bool, len(firstKnown))
	for _, h := range firstKnown {
		known[string(h)] = true
	}
	for _, raw := range wantRaw {
		e, err := trustlog.UnmarshalEntry(raw)
		if err != nil {
			t.Fatalf("UnmarshalEntry: %v", err)
		}
		if !known[string(trustlog.HashEntry(&e))] {
			t.Fatalf("first offer missing chain entry hash; known has %d entries", len(firstKnown))
		}
	}
}

// TestSyncTrustChainsDisjointLatch verifies that the disjoint warning fires on
// the first disjoint sync and is suppressed on subsequent ones. Checked via
// lastDisjointLogged state transitions.
func TestSyncTrustChainsDisjointLatch(t *testing.T) {
	d := New()
	peer := &fakeTrustCaller{
		fn: func(method string, params, out any) error {
			if method == api.MethodTrustLogSync {
				out.(*api.TrustLogSyncResult).Disjoint = true
			}
			return nil
		},
	}
	d.syncTrustChains(peer)
	d.pinMu.Lock()
	latched := d.lastDisjointLogged
	d.pinMu.Unlock()
	if !latched {
		t.Fatal("lastDisjointLogged must be true after first disjoint sync")
	}
	d.syncTrustChains(peer)
	d.pinMu.Lock()
	latched = d.lastDisjointLogged
	d.pinMu.Unlock()
	if !latched {
		t.Fatal("lastDisjointLogged must remain true after repeated disjoint sync")
	}
}
