package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// startPeerGateway runs an in-process gateway and returns its node uplink URL.
func startPeerGateway(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(gateway.NewServer(gateway.New(time.Second), nil, nil).Handler())
	t.Cleanup(ts.Close)
	return wsURL(ts.URL) + "/node"
}

// startPeerNode uplinks a tmux-less node with the given ID to the gateway. kp is
// used only when e2ee is on. The returned stop ends the uplink and waits for it
// to wind down; it is also registered as cleanup and is safe to call twice.
func startPeerNode(t *testing.T, url, id string, kp e2e.KeyPair, e2ee bool) (*Node, func()) {
	t.Helper()
	return startPeerNodeWith(t, url, id, kp, e2ee, nil)
}

// startPeerNodeWith is startPeerNode with setup run on the node before it uplinks.
func startPeerNodeWith(t *testing.T, url, id string, kp e2e.KeyPair, e2ee bool, setup func(*Node)) (*Node, func()) {
	t.Helper()
	n := newNode(map[session.TmuxServer]*tmux.Client{})
	n.SetIdentity(id, id)
	if e2ee {
		n.SetIdentityKey(kp)
		n.SetE2EE(true)
	}
	if setup != nil {
		setup(n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		n.ConnectGateway(ctx, url, "", nil)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stop)
	return n, stop
}

// waitPeerOnline blocks until n's roster lists id online (with its identity key
// when e2ee) and n's peer pool exists.
func waitPeerOnline(t *testing.T, n *Node, id string, e2ee bool) {
	t.Helper()
	waitFor(t, func() bool {
		if n.peers.Load() == nil {
			return false
		}
		nd, ok := n.roster.get(id)
		return ok && nd.Online && (!e2ee || nd.IdentityPubKey != "")
	})
}

func mustKeyPair(t *testing.T) e2e.KeyPair {
	t.Helper()
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	return kp
}

// twoNodes returns nodes "a" and "b" uplinked to one in-process gateway, each
// already rostered online on the other.
func twoNodes(t *testing.T, e2ee bool) (a, b *Node) {
	t.Helper()
	url := startPeerGateway(t)
	a, _ = startPeerNode(t, url, "a", mustKeyPair(t), e2ee)
	b, _ = startPeerNode(t, url, "b", mustKeyPair(t), e2ee)
	waitPeerOnline(t, a, "b", e2ee)
	waitPeerOnline(t, b, "a", e2ee)
	return a, b
}

func (p *peerPool) chanCountForTest() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byChan)
}

func (p *peerPool) chanIDForTest(nodeID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pc := p.byNode[nodeID]; pc != nil {
		return pc.chanID
	}
	return ""
}

func peerCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func forModes(t *testing.T, fn func(t *testing.T, e2ee bool)) {
	t.Run("e2ee", func(t *testing.T) { fn(t, true) })
	t.Run("plaintext", func(t *testing.T) { fn(t, false) })
}

func TestPeerChanCallsPing(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		a, _ := twoNodes(t, e2ee)
		if err := a.peers.Load().call(peerCtx(t), "b", api.MethodPing, nil, nil); err != nil {
			t.Fatalf("ping b: %v", err)
		}
	})
}

func TestPeerChanReusesChannel(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		a, b := twoNodes(t, e2ee)
		ctx := peerCtx(t)
		pool := a.peers.Load()
		// Concurrent first calls must share one open.
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- pool.call(ctx, "b", api.MethodPing, nil, nil)
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent ping: %v", err)
			}
		}
		if err := pool.call(ctx, "b", api.MethodPing, nil, nil); err != nil {
			t.Fatalf("second ping: %v", err)
		}
		if got := pool.chanCountForTest(); got != 1 {
			t.Fatalf("a holds %d peer channels, want 1", got)
		}
		if got := b.RelayChannelCountForTest(); got != 1 {
			t.Fatalf("b terminates %d channels, want 1", got)
		}
	})
}

func TestPeerChanOfflineNode(t *testing.T) {
	a, _ := twoNodes(t, true)
	err := a.peers.Load().call(peerCtx(t), "zzz", api.MethodPing, nil, nil)
	if !errors.Is(err, errPeerOffline) {
		t.Fatalf("err = %v, want errPeerOffline", err)
	}
	if !strings.Contains(err.Error(), "zzz") {
		t.Fatalf("err %q does not name the node", err)
	}
}

func TestPeerChanReopensAfterPeerReconnect(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		url := startPeerGateway(t)
		a, _ := startPeerNode(t, url, "a", mustKeyPair(t), e2ee)
		bKey := mustKeyPair(t)
		_, stopB := startPeerNode(t, url, "b", bKey, e2ee)
		waitPeerOnline(t, a, "b", e2ee)
		ctx := peerCtx(t)
		pool := a.peers.Load()
		if err := pool.call(ctx, "b", api.MethodPing, nil, nil); err != nil {
			t.Fatalf("first ping: %v", err)
		}

		stopB()
		// relay.closed must drop a's channel to the old b.
		waitFor(t, func() bool { return pool.chanCountForTest() == 0 })

		b2, _ := startPeerNode(t, url, "b", bKey, e2ee)
		waitPeerOnline(t, b2, "a", e2ee)
		waitPeerOnline(t, a, "b", e2ee)
		if err := pool.call(ctx, "b", api.MethodPing, nil, nil); err != nil {
			t.Fatalf("ping after b restart: %v", err)
		}
		if got := b2.RelayChannelCountForTest(); got != 1 {
			t.Fatalf("restarted b terminates %d channels, want 1", got)
		}
	})
}

func TestResponderIgnoresInitiatorFrames(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		a, _ := twoNodes(t, e2ee)
		if err := a.peers.Load().call(peerCtx(t), "b", api.MethodPing, nil, nil); err != nil {
			t.Fatalf("ping b: %v", err)
		}
		if got := a.RelayChannelCountForTest(); got != 0 {
			t.Fatalf("a's responder holds %d channels, want 0", got)
		}
	})
}

// fakeRosterEntry puts id in n's roster as online with a fresh, unrelated key.
func fakeRosterEntry(t *testing.T, n *Node, id string) {
	t.Helper()
	n.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{
		ID: id, Online: true, IdentityPubKey: base64.StdEncoding.EncodeToString(mustKeyPair(t).Public),
		Capabilities: api.NodeCapabilities{CrossNodeOrch: crossNodeOrchVersion},
	}})
}

func TestPeerChanUnknownNodeIsOffline(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		a, _ := twoNodes(t, e2ee)
		// Online in a's roster, unknown to the gateway: relay.open fails.
		fakeRosterEntry(t, a, "ghost")
		err := a.peers.Load().call(peerCtx(t), "ghost", api.MethodPing, nil, nil)
		if !errors.Is(err, errPeerOffline) {
			t.Fatalf("err = %v, want errPeerOffline", err)
		}
		if !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("err %q does not name the node", err)
		}
	})
}

func TestPeerChanTimeoutKeepsChannel(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		a, b := twoNodes(t, e2ee)
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		b.server.Handle("test.slow", func(context.Context, json.RawMessage) (any, error) {
			<-release
			return nil, nil
		})
		prev := peerCallTimeoutNs.Load()
		peerCallTimeoutNs.Store(int64(200 * time.Millisecond))
		t.Cleanup(func() { peerCallTimeoutNs.Store(prev) })

		ctx := peerCtx(t)
		pool := a.peers.Load()
		if err := pool.call(ctx, "b", api.MethodPing, nil, nil); err != nil {
			t.Fatalf("first ping: %v", err)
		}
		before := pool.chanIDForTest("b")
		if err := pool.call(ctx, "b", "test.slow", nil, nil); err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("slow call err = %v, want timeout", err)
		}
		if err := pool.call(ctx, "b", api.MethodPing, nil, nil); err != nil {
			t.Fatalf("ping after timeout: %v", err)
		}
		if after := pool.chanIDForTest("b"); after != before {
			t.Fatalf("channel changed from %q to %q across a call timeout", before, after)
		}
		if got := pool.chanCountForTest(); got != 1 {
			t.Fatalf("a holds %d peer channels, want 1", got)
		}
		if got := b.RelayChannelCountForTest(); got != 1 {
			t.Fatalf("b terminates %d channels, want 1 (channel was reopened)", got)
		}
	})
}

func TestPeerChanClosedDuringHandshake(t *testing.T) {
	a, _ := twoNodes(t, true)
	// A wrong key for b: b drops msg1, so the handshake never completes.
	fakeRosterEntry(t, a, "b")
	pool := a.peers.Load()
	start := time.Now()
	errc := make(chan error, 1)
	go func() { errc <- pool.call(peerCtx(t), "b", api.MethodPing, nil, nil) }()

	var chanID string
	waitFor(t, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		for id := range pool.byChan {
			chanID = id
		}
		return chanID != ""
	})
	pool.closeChan(chanID)
	select {
	case err := <-errc:
		if !errors.Is(err, errPeerChanClosed) {
			t.Fatalf("err = %v, want errPeerChanClosed", err)
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Fatalf("call returned after %v, want well under the handshake timeout", el)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call still waiting after relay.closed")
	}
}

func TestPeerChanClosesOnRosterOffline(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		a, _ := twoNodes(t, e2ee)
		pool := a.peers.Load()
		if err := pool.call(peerCtx(t), "b", api.MethodPing, nil, nil); err != nil {
			t.Fatalf("ping b: %v", err)
		}
		a.roster.apply(api.NodeEvent{Type: api.NodeEventOffline, Node: api.NodeDescriptor{ID: "b"}})
		waitFor(t, func() bool { return pool.chanCountForTest() == 0 })
	})
}
func TestPeerChanClosesOnRosterOnline(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		a, _ := twoNodes(t, e2ee)
		pool := a.peers.Load()
		if err := pool.call(peerCtx(t), "b", api.MethodPing, nil, nil); err != nil {
			t.Fatalf("ping b: %v", err)
		}
		// b came back on a new uplink within its grace period: the old channel
		// is bound to the old uplink.
		nd, _ := a.roster.get("b")
		a.roster.apply(api.NodeEvent{Type: api.NodeEventOnline, Node: nd})
		waitFor(t, func() bool { return pool.chanCountForTest() == 0 })
		if err := pool.call(peerCtx(t), "b", api.MethodPing, nil, nil); err != nil {
			t.Fatalf("ping b after reconnect: %v", err)
		}
	})
}

// lockTrust puts n in locked mode with a trust log that authorizes pubs.
func lockTrust(t *testing.T, n *Node, pubs ...[]byte) {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatal(err)
	}
	genesis := lg.Tip()
	for _, pub := range pubs {
		if err := lg.AuthorizeDevice(pub, signer); err != nil {
			t.Fatal(err)
		}
	}
	ss := trustlog.NewSyncStore(genesis)
	if _, err := ss.Ingest(trustlog.MarshalChain(lg.Entries())); err != nil {
		t.Fatal(err)
	}
	n.trust.Store(ss)
}

func TestPeerChanLockedInitiatorRefusesUnauthorizedKey(t *testing.T) {
	a, b := twoNodes(t, true)
	// a's trust log does not hold b's key, so the roster key can be the gateway's.
	lockTrust(t, a, mustKeyPair(t).Public)
	err := a.peers.Load().call(peerCtx(t), "b", api.MethodPing, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("err = %v, want a refused open", err)
	}
	if n := a.peers.Load().chanCountForTest(); n != 0 {
		t.Fatalf("channels = %d, want 0", n)
	}

	lockTrust(t, a, b.identity.Public)
	lockTrust(t, b, a.identity.Public)
	if err := a.peers.Load().call(peerCtx(t), "b", api.MethodPing, nil, nil); err != nil {
		t.Fatalf("authorized key: %v", err)
	}
}

func TestPeerChanSpawnGetsLongerTimeout(t *testing.T) {
	a, b := twoNodes(t, false)
	b.server.Handle(api.MethodOrchSessionSpawn, func(context.Context, json.RawMessage) (any, error) {
		time.Sleep(400 * time.Millisecond)
		return nil, nil
	})
	prev := peerCallTimeoutNs.Load()
	peerCallTimeoutNs.Store(int64(200 * time.Millisecond))
	t.Cleanup(func() { peerCallTimeoutNs.Store(prev) })
	if err := a.peers.Load().call(peerCtx(t), "b", api.MethodOrchSessionSpawn, nil, nil); err != nil {
		t.Fatalf("spawn slower than a call, within the spawn timeout: %v", err)
	}
}

// directPair is nodes a and b on one gateway, sharing a master token. b
// listens for direct links and advertises its listener; a dials out through
// aNet. setupB runs after b's defaults, so it can replace the endpoints.
type directPair struct {
	a, b  *Node
	aNet  *fakeTailnet
	stopA func()
}

func newDirectPair(t *testing.T, e2ee bool, setupB func(*Node)) directPair {
	t.Helper()
	url := startPeerGateway(t)
	dp := directPair{aNet: &fakeTailnet{}}
	dp.a, dp.stopA = startPeerNodeWith(t, url, "a", mustKeyPair(t), e2ee, func(n *Node) {
		n.SetMasterToken(masterTok)
		n.SetTailnet(dp.aNet)
	})
	dp.b, _ = startPeerNodeWith(t, url, "b", mustKeyPair(t), e2ee, func(n *Node) {
		n.SetMasterToken(masterTok)
		n.SetTailnet(&fakeTailnet{})
		n.SetP2PEndpoints([]string{serveDirectForTest(t, n)})
		if setupB != nil {
			setupB(n)
		}
	})
	waitPeerOnline(t, dp.a, "b", e2ee)
	waitPeerOnline(t, dp.b, "a", e2ee)
	return dp
}

// pingDirect pings b from a and requires the channel to be direct.
func (dp directPair) pingDirect(t *testing.T) {
	t.Helper()
	pool := dp.a.peers.Load()
	if err := pool.call(peerCtx(t), "b", api.MethodPing, nil, nil); err != nil {
		t.Fatalf("ping b: %v", err)
	}
	if via := dp.a.PeerViaForTest("b"); via != viaTCP {
		t.Fatalf("via = %q, want %q", via, viaTCP)
	}
}

// slowCall starts a call to test.slow on b, which never returns on its own.
func (dp directPair) slowCall(t *testing.T) <-chan error {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	started := make(chan struct{}, 1)
	dp.b.server.Handle("test.slow", func(context.Context, json.RawMessage) (any, error) {
		started <- struct{}{}
		<-release
		return nil, nil
	})
	errc := make(chan error, 1)
	go func() { errc <- dp.a.peers.Load().call(peerCtx(t), "b", "test.slow", nil, nil) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("test.slow never reached b")
	}
	return errc
}

func wantPromptFailure(t *testing.T, errc <-chan error) {
	t.Helper()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("in-flight call succeeded, want it failed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight call still waiting after its link dropped")
	}
}

func TestPeerOpensDirect(t *testing.T) {
	dp := newDirectPair(t, true, nil)
	dp.pingDirect(t)
	if id := dp.a.peers.Load().chanIDForTest("b"); !validDirectChanID(id) {
		t.Fatalf("chan id = %q, want p and 16 hex digits", id)
	}
	// The gateway carried no node-opened channel.
	if n := dp.b.RelayChannelCountForTest(); n != 0 {
		t.Fatalf("b terminates %d relay channels, want 0", n)
	}
	if n := dp.b.activeResponder.Load().pendingPeerCountForTest(); n != 0 {
		t.Fatalf("b holds %d channel.opened records, want 0", n)
	}
	if n := len(dp.b.directRespondersForTest()); n != 1 {
		t.Fatalf("b serves %d direct connections, want 1", n)
	}
}

func TestPlainPeerOpensDirect(t *testing.T) {
	dp := newDirectPair(t, false, nil)
	dp.b.server.Handle("orch.test", func(ctx context.Context, _ json.RawMessage) (any, error) {
		id, _ := peerNodeFrom(ctx)
		return id, nil
	})
	dp.pingDirect(t)
	var from string
	if err := dp.a.peers.Load().call(peerCtx(t), "b", "orch.test", nil, &from); err != nil {
		t.Fatalf("orch.test: %v", err)
	}
	if from != "a" {
		t.Fatalf("b served the call as peer %q, want a", from)
	}
	if n := dp.b.RelayChannelCountForTest(); n != 0 {
		t.Fatalf("b terminates %d relay channels, want 0", n)
	}
	if n := len(dp.b.directRespondersForTest()); n != 1 {
		t.Fatalf("b serves %d direct connections, want 1", n)
	}
}

func TestPeerDirectFallsBackToRelay(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		t.Run("unreachable", func(t *testing.T) {
			dp := newDirectPair(t, e2ee, func(n *Node) {
				n.SetP2PEndpoints([]string{"ws://127.0.0.1:1/p2p"})
			})
			pool := dp.a.peers.Load()
			if err := pool.call(peerCtx(t), "b", api.MethodPing, nil, nil); err != nil {
				t.Fatalf("ping b: %v", err)
			}
			if via := dp.a.PeerViaForTest("b"); via != viaGateway {
				t.Fatalf("via = %q, want %q", via, viaGateway)
			}
		})
		t.Run("not a tailnet peer", func(t *testing.T) {
			dp := newDirectPair(t, e2ee, nil)
			dp.aNet.deny.Store(true)
			pool := dp.a.peers.Load()
			if err := pool.call(peerCtx(t), "b", api.MethodPing, nil, nil); err != nil {
				t.Fatalf("ping b: %v", err)
			}
			if via := dp.a.PeerViaForTest("b"); via != viaGateway {
				t.Fatalf("via = %q, want %q", via, viaGateway)
			}
			if n := len(dp.b.directRespondersForTest()); n != 0 {
				t.Fatalf("b serves %d direct connections, want 0", n)
			}
		})
		t.Run("dropped", func(t *testing.T) {
			dp := newDirectPair(t, e2ee, nil)
			dp.pingDirect(t)
			errc := dp.slowCall(t)
			dp.b.closeAllDirect()
			wantPromptFailure(t, errc)
			pool := dp.a.peers.Load()
			// The relay channel opens at once, before the next call.
			waitFor(t, func() bool { return dp.a.PeerViaForTest("b") == viaGateway && pool.chanCountForTest() == 1 })
			if err := pool.call(peerCtx(t), "b", api.MethodPing, nil, nil); err != nil {
				t.Fatalf("ping b on the relay: %v", err)
			}
			if n := dp.b.RelayChannelCountForTest(); n != 1 {
				t.Fatalf("b terminates %d relay channels, want 1", n)
			}
		})
	})
}

func TestPeerUpgradesLater(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		prev := peerUpgradeIntervalNs.Load()
		peerUpgradeIntervalNs.Store(int64(100 * time.Millisecond))
		t.Cleanup(func() { peerUpgradeIntervalNs.Store(prev) })

		dp := newDirectPair(t, e2ee, nil)
		dp.aNet.deny.Store(true)
		pool := dp.a.peers.Load()
		if err := pool.call(peerCtx(t), "b", api.MethodPing, nil, nil); err != nil {
			t.Fatalf("ping b: %v", err)
		}
		if via := dp.a.PeerViaForTest("b"); via != viaGateway {
			t.Fatalf("via = %q, want %q", via, viaGateway)
		}
		dp.aNet.deny.Store(false)
		waitFor(t, func() bool { return dp.a.PeerViaForTest("b") == viaTCP })
		dp.pingDirect(t)
		// The replaced relay channel is released once it is idle.
		waitFor(t, func() bool { return pool.chanCountForTest() == 1 && dp.b.RelayChannelCountForTest() == 0 })
	})
}

func TestPeerDirectLockedRejectsUnauthorizedTarget(t *testing.T) {
	dp := newDirectPair(t, true, nil)
	// a's trust log does not hold b's key, so the roster key can be the gateway's.
	lockTrust(t, dp.a, mustKeyPair(t).Public)
	err := dp.a.peers.Load().call(peerCtx(t), "b", api.MethodPing, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("err = %v, want a refused open", err)
	}
	if n := len(dp.b.directRespondersForTest()); n != 0 {
		t.Fatalf("b serves %d direct connections, want 0", n)
	}
	if n := dp.a.peers.Load().chanCountForTest(); n != 0 {
		t.Fatalf("channels = %d, want 0", n)
	}

	lockTrust(t, dp.a, dp.b.identity.Public)
	lockTrust(t, dp.b, dp.a.identity.Public)
	dp.pingDirect(t)
}

func TestPeerDirectClosesOnUplinkClose(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		dp := newDirectPair(t, e2ee, nil)
		dp.pingDirect(t)
		errc := dp.slowCall(t)
		dp.stopA()
		wantPromptFailure(t, errc)
		waitFor(t, func() bool { return len(dp.b.directRespondersForTest()) == 0 })
	})
}

func TestPeerDirectClosesOnRosterOffline(t *testing.T) {
	forModes(t, func(t *testing.T, e2ee bool) {
		dp := newDirectPair(t, e2ee, nil)
		dp.pingDirect(t)
		pool := dp.a.peers.Load()
		dp.a.roster.apply(api.NodeEvent{Type: api.NodeEventOffline, Node: api.NodeDescriptor{ID: "b"}})
		waitFor(t, func() bool { return len(dp.b.directRespondersForTest()) == 0 })
		// The roster governs: no relay fallback for an offline node.
		time.Sleep(100 * time.Millisecond)
		if n := pool.chanCountForTest(); n != 0 {
			t.Fatalf("channels = %d, want 0", n)
		}
	})
}

func TestPeerDirectClosesOnQuarantine(t *testing.T) {
	dp := newDirectPair(t, true, nil)
	dp.pingDirect(t)
	pool := dp.a.peers.Load()
	dp.a.trustGate.Trip([]byte("genesis"))
	dp.a.reevaluateTrustChannels()
	waitFor(t, func() bool { return len(dp.b.directRespondersForTest()) == 0 })
	time.Sleep(100 * time.Millisecond)
	if n := pool.chanCountForTest(); n != 0 {
		t.Fatalf("channels = %d, want 0", n)
	}
}

// TestPeerDirectIgnoresUplinkFrames: a frame the gateway sends on a direct
// channel's id never reaches that channel.
func TestPeerDirectIgnoresUplinkFrames(t *testing.T) {
	dp := newDirectPair(t, false, nil)
	dp.pingDirect(t)
	pool := dp.a.peers.Load()
	chanID := pool.chanIDForTest("b")
	pool.mu.Lock()
	pc := pool.byNode["b"]
	replyCh := make(chan peerReply, 1)
	pc.pending[999] = replyCh
	pool.mu.Unlock()
	id := json.RawMessage("999")
	raw, err := api.NewPlainChannel(chanID).SealResponseFrame(api.EncodingJSON, &id, []byte(`"forged"`), nil)
	if err != nil {
		t.Fatal(err)
	}
	f, err := api.ParseRelayFrame(api.EncodingJSON, raw)
	if err != nil {
		t.Fatal(err)
	}
	pool.onFrame(f)
	select {
	case r := <-replyCh:
		t.Fatalf("uplink frame reached the direct channel: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}
