package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
// and must send the known heads so the gateway can compute its delta.
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
		t.Fatal("the second sync must send the known heads")
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

	// Gateway restarted and signals it wants our branch back.
	peer.chains = nil
	peer.want = [][]byte{bytes.Repeat([]byte{0x01}, 32)}
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

	// Now set Want: every subsequent sync must trigger a push.
	peer := &recordingTrustPeer{chains: [][]byte{chain}, want: [][]byte{bytes.Repeat([]byte{0x01}, 32)}}
	d.syncTrustOnce(peer)
	d.syncTrustOnce(peer)

	if peer.offers < 2 {
		t.Fatalf("offers = %d; a gateway that sets Want on every sync must get a push each time", peer.offers)
	}
}

type recordingTrustPeer struct {
	mu        sync.Mutex
	chains    [][]byte            // served as entries on MethodTrustLogSync
	want      [][]byte            // returned as Want in sync response (signals the node to push)
	roster    []api.NodeDescriptor // served by nodes.list
	offers    int                 // MethodTrustLogPush calls
	pulls     int                 // MethodTrustLogSync calls
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
			p.lastKnown = pp.Heads
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

// ingestForTest ingests chain into d's trust store and records the head, mirroring
// what pullTrustOnce does so knownHeads is populated.
func ingestForTest(d *Node, chain []byte) error {
	st := d.trust.Load()
	if st == nil {
		return fmt.Errorf("no trust store")
	}
	if _, err := st.Ingest(chain); err != nil {
		return err
	}
	d.rememberHead(chain)
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
				res.Want = d.knownHeads()
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
