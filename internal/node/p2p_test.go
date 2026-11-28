package node

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// fakeTailnet stands in for tailscaled. Every caller is a tailnet peer unless
// deny or err is set; block, when set, holds WhoIs until it is closed. Each
// WhoIs names the next node in ids, or node-a once ids runs out.
type fakeTailnet struct {
	deny    atomic.Bool
	err     atomic.Pointer[error]
	block   chan struct{}
	waiting atomic.Int32

	mu  sync.Mutex
	ids []string
}

func (f *fakeTailnet) queue(ids ...string) {
	f.mu.Lock()
	f.ids = append(f.ids, ids...)
	f.mu.Unlock()
}

func (f *fakeTailnet) nextID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ids) == 0 {
		return "node-a"
	}
	id := f.ids[0]
	f.ids = f.ids[1:]
	return id
}

func (f *fakeTailnet) WhoIs(ctx context.Context, _ string) (*apitype.WhoIsResponse, error) {
	if f.block != nil {
		f.waiting.Add(1)
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if e := f.err.Load(); e != nil {
		return nil, *e
	}
	if f.deny.Load() {
		return nil, local.ErrPeerNotFound
	}
	return &apitype.WhoIsResponse{Node: &tailcfg.Node{StableID: tailcfg.StableNodeID(f.nextID())}}, nil
}

// Status reports a running tailscaled, so a node dialing out through f finds
// every WhoIs-approved host dialable.
func (f *fakeTailnet) Status(context.Context) (*ipnstate.Status, error) {
	return &ipnstate.Status{BackendState: "Running"}, nil
}

const directChanID = "p0123456789abcdef"

// directNode returns an E2EE node with a fake uplink (gw) and a /p2p server
// for it, reachable at url.
func directNode(t *testing.T) (d *Node, gw *api.Peer, url string, ft *fakeTailnet) {
	t.Helper()
	d = newE2ETestNode(t)
	d.SetE2EE(true)
	ft = &fakeTailnet{}
	d.SetTailnet(ft)
	r := d.newRelayResponder(nil)
	gw, _ = gatewayUplink(t, r)
	d.activeResponder.Store(r)
	t.Cleanup(func() { d.activeResponder.CompareAndSwap(r, nil) })
	return d, gw, serveDirectForTest(t, d), ft
}

func serveDirectForTest(t *testing.T, d *Node) string {
	t.Helper()
	srv := httptest.NewServer(d.p2pHandler())
	t.Cleanup(srv.Close)
	t.Cleanup(d.closeAllDirect)
	return wsURL(srv.URL) + "/p2p"
}

// bindClient binds kp to token hash h1 and marks h1 paired.
func bindClient(t *testing.T, d *Node, kp e2e.KeyPair) {
	t.Helper()
	s := d.bindings()
	if s == nil {
		t.Fatal("no binding store")
	}
	s.setValid([]string{"h1"})
	s.bind(kp.Public, "h1")
}

type directClient struct {
	t      *testing.T
	d      *Node
	ch     *api.Channel
	peer   *api.Peer
	frames chan api.RelayFrame
}

// dialDirect connects to url and handshakes chanID as kp. It reports whether
// the node answered with msg2; false means the node closed the connection.
func dialDirect(t *testing.T, d *Node, url string, kp e2e.KeyPair, chanID string) (*directClient, bool) {
	t.Helper()
	c := &directClient{t: t, d: d, frames: make(chan api.RelayFrame, 64)}
	peer, err := api.DialWSPeer(context.Background(), url, "", nil, api.PeerOptions{
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) {
			select {
			case c.frames <- f:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	c.peer = peer
	t.Cleanup(func() { peer.Close() })
	init, msg1, err := e2e.NewInitiator(kp, d.identity.Public, api.ChannelPrologue(d.id, chanID))
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	hf, _ := api.MarshalHandshakeFrame(chanID, msg1)
	if err := peer.SendRawFrame(api.EncodingJSON, hf); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	select {
	case f := <-c.frames:
		msg2, err := api.HandshakeFromFrame(f)
		if err != nil {
			t.Fatalf("HandshakeFromFrame: %v", err)
		}
		sess, err := init.Finish(msg2)
		if err != nil {
			t.Fatalf("Finish: %v", err)
		}
		c.ch = api.NewChannel(chanID, sess)
		return c, true
	case <-peer.Done():
		return c, false
	case <-time.After(5 * time.Second):
		t.Fatal("no handshake reply and no close from node")
	}
	return nil, false
}

func mustAdmit(t *testing.T, d *Node, url string, kp e2e.KeyPair) *directClient {
	t.Helper()
	c, ok := dialDirect(t, d, url, kp, directChanID)
	if !ok {
		t.Fatal("direct handshake rejected, want admitted")
	}
	return c
}

func mustReject(t *testing.T, d *Node, url string, kp e2e.KeyPair) {
	t.Helper()
	if _, ok := dialDirect(t, d, url, kp, directChanID); ok {
		t.Fatal("direct handshake admitted, want rejected")
	}
}

func (c *directClient) call(method string, params json.RawMessage) (json.RawMessage, *api.RPCError) {
	c.t.Helper()
	return callOver(c.t, c.ch, c.peer, c.frames, c.d.id, method, params)
}

func (c *directClient) waitClosed() {
	c.t.Helper()
	select {
	case <-c.peer.Done():
	case <-time.After(5 * time.Second):
		c.t.Fatal("direct connection still open, want closed")
	}
}

func handleEcho(d *Node) {
	d.server.Handle("test.echo", func(_ context.Context, params json.RawMessage) (any, error) {
		return params, nil
	})
}

func httpStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get("http" + strings.TrimPrefix(url, "ws"))
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestDirectAdmitsBoundClient(t *testing.T) {
	d, _, url, _ := directNode(t)
	handleEcho(d)
	kp := mustKeyPair(t)
	bindClient(t, d, kp)

	c := mustAdmit(t, d, url, kp)
	res, rpcErr := c.call("test.echo", json.RawMessage(`{"a":1}`))
	if rpcErr != nil || string(res) != `{"a":1}` {
		t.Fatalf("echo = %s, %v", res, rpcErr)
	}
	// Client dispatch: orch.* stays blocked.
	d.server.Handle("orch.test", func(context.Context, json.RawMessage) (any, error) { return "ok", nil })
	if _, rpcErr := c.call("orch.test", nil); rpcErr == nil || rpcErr.Code != api.CodeMethodNotFound {
		t.Fatalf("orch.test on a direct client channel: %v, want method not found", rpcErr)
	}
}

func TestDirectRejectsUnboundClient(t *testing.T) {
	d, _, url, _ := directNode(t)
	bindClient(t, d, mustKeyPair(t))
	mustReject(t, d, url, mustKeyPair(t))

	// A key bound to a token no longer paired is rejected too.
	kp := mustKeyPair(t)
	bindClient(t, d, kp)
	d.bindings().setValid(nil)
	mustReject(t, d, url, kp)
}

func TestDirectRejectsNonTailnet(t *testing.T) {
	d, _, url, ft := directNode(t)
	ft.deny.Store(true)
	if got := httpStatus(t, url); got != http.StatusForbidden {
		t.Fatalf("non-tailnet status = %d, want 403", got)
	}
	ft.deny.Store(false)
	err := errors.New("tailscaled down")
	ft.err.Store(&err)
	if got := httpStatus(t, url); got != http.StatusForbidden {
		t.Fatalf("tailscaled error status = %d, want 403", got)
	}
	if d.directPreAuthForTest() != 0 {
		t.Fatal("rejected requests must free their pre-auth slots")
	}
}

func TestDirectRejectsWithoutUplink(t *testing.T) {
	d := newE2ETestNode(t)
	d.SetE2EE(true)
	d.SetTailnet(&fakeTailnet{})
	url := serveDirectForTest(t, d)
	if got := httpStatus(t, url); got != http.StatusServiceUnavailable {
		t.Fatalf("no-uplink status = %d, want 503", got)
	}
}

func TestDirectClosesOnUplinkClose(t *testing.T) {
	ts := httptest.NewServer(gateway.NewServer(gateway.New(time.Second),
		func(tok string) bool { return tok == "dtok" }, nil).Handler())
	defer ts.Close()

	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.SetIdentity("home", "home-box")
	d.SetIdentityKey(mustKeyPair(t))
	d.SetE2EE(true)
	d.SetMasterToken("dtok")
	d.SetTailnet(&fakeTailnet{})
	url := serveDirectForTest(t, d)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.ConnectGateway(ctx, wsURL(ts.URL)+"/node", "dtok", nil) }()
	defer func() { cancel(); <-done }()
	waitFor(t, func() bool { return d.bindings() != nil })

	kp := mustKeyPair(t)
	bindClient(t, d, kp)
	c := mustAdmit(t, d, url, kp)

	cancel()
	<-done
	c.waitClosed()
	waitFor(t, func() bool { return d.directAdmittedForTest() == 0 })
}

func TestTokenRevokeClosesDirect(t *testing.T) {
	d, gw, url, _ := directNode(t)
	kp, other := mustKeyPair(t), mustKeyPair(t)
	s := d.bindings()
	s.setValid([]string{"h1", "h2"})
	s.bind(kp.Public, "h1")
	s.bind(other.Public, "h2")
	c := mustAdmit(t, d, url, kp)
	keep := mustAdmit(t, d, url, other)

	notifyGW(t, gw, api.MethodTokenListChanged, api.TokenListChangedParams{Hashes: []string{"h2"}})
	c.waitClosed()
	if _, rpcErr := keep.call(api.MethodPing, nil); rpcErr != nil {
		t.Fatalf("ping on a still-valid direct channel: %v", rpcErr)
	}
}

func TestTrustRevokeClosesDirect(t *testing.T) {
	d, _, url, _ := directNode(t)
	kp := mustKeyPair(t)
	bindClient(t, d, kp)
	lockTrust(t, d, kp.Public)
	c := mustAdmit(t, d, url, kp)

	lockTrust(t, d) // the device is no longer authorized
	d.reevaluateTrustChannels()
	c.waitClosed()
}

func TestQuarantineClosesDirect(t *testing.T) {
	d, _, url, _ := directNode(t)
	kp := mustKeyPair(t)
	bindClient(t, d, kp)
	c := mustAdmit(t, d, url, kp)

	d.trustGate.Trip([]byte("genesis"))
	d.reevaluateTrustChannels()
	c.waitClosed()

	mustReject(t, d, url, kp)
}

func TestDirectLockedRequiresTrustLog(t *testing.T) {
	d, _, url, _ := directNode(t)
	authorized, unauthorized, peerKP := mustKeyPair(t), mustKeyPair(t), mustKeyPair(t)
	bindClient(t, d, authorized)
	d.bindings().bind(unauthorized.Public, "h1")
	rosterKey(d, "b", peerKP.Public)
	lockTrust(t, d, authorized.Public)

	mustAdmit(t, d, url, authorized)
	mustReject(t, d, url, unauthorized)
	mustReject(t, d, url, peerKP)
}

func TestDirectPeerByRosterKey(t *testing.T) {
	d, _, url, _ := directNode(t)
	bKP := mustKeyPair(t)
	rosterKey(d, "b", bKP.Public)
	d.server.Handle("orch.test", func(ctx context.Context, _ json.RawMessage) (any, error) {
		id, _ := peerNodeFrom(ctx)
		return id, nil
	})

	c := mustAdmit(t, d, url, bKP)
	res, rpcErr := c.call("orch.test", nil)
	if rpcErr != nil || string(res) != `"b"` {
		t.Fatalf("orch.test on a direct peer channel = %s, %v; want \"b\"", res, rpcErr)
	}
	if d.bindings().countForTest() != 0 {
		t.Fatal("a direct peer channel must not bind")
	}
}

func TestDirectPeerClosedNotDemoted(t *testing.T) {
	t.Run("key change", func(t *testing.T) {
		d, _, url, _ := directNode(t)
		bKP := mustKeyPair(t)
		rosterKey(d, "b", bKP.Public)
		c := mustAdmit(t, d, url, bKP)
		rosterKey(d, "b", mustKeyPair(t).Public)
		c.waitClosed()
	})
	t.Run("removed", func(t *testing.T) {
		d, _, url, _ := directNode(t)
		bKP := mustKeyPair(t)
		rosterKey(d, "b", bKP.Public)
		c := mustAdmit(t, d, url, bKP)
		d.roster.apply(api.NodeEvent{Type: api.NodeEventRemoved, Node: api.NodeDescriptor{ID: "b"}})
		c.waitClosed()
	})
	t.Run("serve re-check", func(t *testing.T) {
		d, _, url, _ := directNode(t)
		bKP := mustKeyPair(t)
		rosterKey(d, "b", bKP.Public)
		c := mustAdmit(t, d, url, bKP)
		r := d.directRespondersForTest()[0]
		cs := r.lookup(directChanID)
		// Change the key without a roster event: the next request must close
		// the channel, not serve it as a client channel.
		d.roster.mu.Lock()
		n := d.roster.nodes["b"]
		n.IdentityPubKey = ""
		d.roster.nodes["b"] = n
		d.roster.mu.Unlock()
		if p := r.channelPeer(cs); p != "" {
			t.Fatalf("channelPeer = %q, want closed", p)
		}
		c.waitClosed()
	})
}

func TestDirectIgnoresUplinkMessages(t *testing.T) {
	d, _, url, _ := directNode(t)
	kp := mustKeyPair(t)
	bindClient(t, d, kp)
	c := mustAdmit(t, d, url, kp)

	msgs := []struct {
		method string
		params any
	}{
		{api.MethodNodeEvent, api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{ID: "evil", Online: true}}},
		{api.MethodTokenListChanged, api.TokenListChangedParams{Hashes: []string{"h9"}}},
		{api.MethodChannelOpened, api.ChannelOpenedParams{ChanID: "pfedcba9876543210", TokenHash: "h9"}},
		{api.MethodRelayPeerOpened, api.RelayPeerOpenedParams{ChanID: "pfedcba9876543210", FromNode: "evil"}},
		{api.MethodRelayClosed, api.RelayCloseParams{ChanID: directChanID}},
	}
	for _, m := range msgs {
		if err := c.peer.Notify(m.method, m.params); err != nil {
			t.Fatalf("notify %s: %v", m.method, err)
		}
	}
	// A request after the notifications proves they were all read.
	if _, rpcErr := c.call(api.MethodPing, nil); rpcErr != nil {
		t.Fatalf("ping after uplink messages: %v", rpcErr)
	}
	if _, ok := d.roster.get("evil"); ok {
		t.Fatal("node.event on a direct link changed the roster")
	}
	if !d.bindings().tokenValid("h1") || d.bindings().tokenValid("h9") {
		t.Fatal("token.list.changed on a direct link changed the token list")
	}
	if d.activeResponder.Load().pendingPeerCountForTest() != 0 {
		t.Fatal("channel.opened on a direct link reached the uplink responder")
	}
	// The uplink's dispatch is not reachable either: node.identify is served by
	// the channel, but a plain request on the connection gets no handler.
	if err := c.peer.Call(api.MethodNodeIdentify, nil, nil); !isCode(err, api.CodeMethodNotFound) {
		t.Fatalf("plain node.identify on a direct link: %v, want method not found", err)
	}
}

func TestDirectRejectsNonPrefixedChan(t *testing.T) {
	d, _, url, _ := directNode(t)
	kp := mustKeyPair(t)
	bindClient(t, d, kp)
	for _, id := range []string{"c1", "p1", "p0123456789ABCDEF", "q0123456789abcdef"} {
		if _, ok := dialDirect(t, d, url, kp, id); ok {
			t.Fatalf("direct handshake on %q admitted, want rejected", id)
		}
	}
}

func TestRelayRejectsPrefixedChan(t *testing.T) {
	d := newE2ETestNode(t)
	d.SetE2EE(true)
	r := d.newRelayResponder(nil)
	gw, fromNode := gatewayUplink(t, r)
	kp := mustKeyPair(t)

	init, msg1, err := e2e.NewInitiator(kp, r.static.Public, api.ChannelPrologue(d.id, directChanID))
	if err != nil {
		t.Fatal(err)
	}
	_ = init
	hf, _ := api.MarshalHandshakeFrame(directChanID, msg1)
	if err := gw.SendRawFrame(api.EncodingJSON, hf); err != nil {
		t.Fatal(err)
	}
	// Frames are handled in order: once c1's reply arrives, the p… frame has
	// been dropped.
	handshakeOver(t, r, gw, fromNode, "c1", kp)
	if r.lookup(directChanID) != nil {
		t.Fatal("relay responder accepted a p… channel id")
	}
	select {
	case f := <-fromNode:
		t.Fatalf("unexpected frame %+v", f.Route)
	default:
	}
}

func TestDirectPreAuthLimits(t *testing.T) {
	t.Run("per caller", func(t *testing.T) {
		d, _, url, _ := directNode(t)
		var conns []*api.Peer
		for i := 0; i < directPreAuthPerCaller; i++ {
			p, err := api.DialWSPeer(context.Background(), url, "", nil, api.PeerOptions{})
			if err != nil {
				t.Fatalf("dial %d: %v", i, err)
			}
			conns = append(conns, p)
		}
		waitFor(t, func() bool { return d.directPreAuthForTest() == directPreAuthPerCaller })
		if got := httpStatus(t, url); got != http.StatusServiceUnavailable {
			t.Fatalf("status past the per-caller cap = %d, want 503", got)
		}
		conns[0].Close()
		waitFor(t, func() bool { return d.directPreAuthForTest() == directPreAuthPerCaller-1 })
		p, err := api.DialWSPeer(context.Background(), url, "", nil, api.PeerOptions{})
		if err != nil {
			t.Fatalf("dial after a slot freed: %v", err)
		}
		p.Close()
		for _, c := range conns {
			c.Close()
		}
	})
	t.Run("per tailnet identity", func(t *testing.T) {
		// Userspace networking: every caller arrives from loopback, so the
		// per-caller cap must follow the tailnet node, not the socket IP.
		d, _, url, ft := directNode(t)
		var conns []*api.Peer
		defer func() {
			for _, c := range conns {
				c.Close()
			}
		}()
		dial := func(id string) error {
			ft.queue(id)
			p, err := api.DialWSPeer(context.Background(), url, "", nil, api.PeerOptions{})
			if err == nil {
				conns = append(conns, p)
			}
			return err
		}
		for i := 0; i < directPreAuthPerCaller; i++ {
			if err := dial("node-a"); err != nil {
				t.Fatalf("node-a dial %d: %v", i, err)
			}
		}
		waitFor(t, func() bool { return d.directPreAuthForTest() == directPreAuthPerCaller })
		if err := dial("node-a"); err == nil {
			t.Fatal("node-a past its per-caller cap was admitted, want refused")
		}
		if err := dial("node-b"); err != nil {
			t.Fatalf("node-b from the same IP refused: %v", err)
		}
		waitFor(t, func() bool { return d.directPreAuthForTest() == directPreAuthPerCaller+1 })
	})
	t.Run("total", func(t *testing.T) {
		d, _, _, ft := directNode(t)
		ft.block = make(chan struct{})
		h := d.p2pHandler()
		var wg sync.WaitGroup
		for i := 0; i < directPreAuthTotal; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodGet, "/p2p", nil)
				req.RemoteAddr = "100.64.0." + strconv.Itoa(i+1) + ":4000"
				h.ServeHTTP(httptest.NewRecorder(), req)
			}()
		}
		waitFor(t, func() bool { return ft.waiting.Load() == directPreAuthTotal })
		req := httptest.NewRequest(http.MethodGet, "/p2p", nil)
		req.RemoteAddr = "100.64.1.1:4000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status past the total cap = %d, want 503", rec.Code)
		}
		close(ft.block)
		wg.Wait()
		if d.directPreAuthForTest() != 0 {
			t.Fatalf("pre-auth slots held after requests ended: %d", d.directPreAuthForTest())
		}
	})
	t.Run("handshake timeout", func(t *testing.T) {
		d, _, url, _ := directNode(t)
		d.direct.limits.handshakeTimeout = 50 * time.Millisecond
		p, err := api.DialWSPeer(context.Background(), url, "", nil, api.PeerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		select {
		case <-p.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("unauthenticated connection outlived the handshake timeout")
		}
		waitFor(t, func() bool { return d.directPreAuthForTest() == 0 })
	})
}

func TestDirectAdmittedCap(t *testing.T) {
	d, _, url, _ := directNode(t)
	d.direct.limits.maxAdmitted = 1
	a, b := mustKeyPair(t), mustKeyPair(t)
	bindClient(t, d, a)
	d.bindings().bind(b.Public, "h1")

	first := mustAdmit(t, d, url, a)
	mustReject(t, d, url, b)
	first.peer.Close()
	waitFor(t, func() bool { return d.directAdmittedForTest() == 0 })
	mustAdmit(t, d, url, b)
}

func TestDirectPreAuthReadLimit(t *testing.T) {
	d, _, url, _ := directNode(t)
	handleEcho(d)
	kp := mustKeyPair(t)
	bindClient(t, d, kp)

	// After admission the normal limit applies.
	c := mustAdmit(t, d, url, kp)
	big := json.RawMessage(`"` + strings.Repeat("x", 100<<10) + `"`)
	if res, rpcErr := c.call("test.echo", big); rpcErr != nil || len(res) != len(big) {
		t.Fatalf("large request after admission: len %d, %v", len(res), rpcErr)
	}

	// Before admission a large message ends the connection.
	conn, err := api.DialWSConn(context.Background(), url, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(strings.Repeat("x", directPreAuthReadLimit+1))); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read after an oversized pre-auth message: %v, want the node to close", err)
	}
}

// TestDirectPreAuthReadBudget: before admission the connection may read at
// most directPreAuthReadLimit bytes in total, however they are split.
func TestDirectPreAuthReadBudget(t *testing.T) {
	chunk := []byte(strings.Repeat("x", 40<<10)) // each message is under the per-message limit
	for name, first := range map[string][]byte{
		"split json line":   nil,
		"large cbor header": {0x00, 0x00, 0xff, 0xff, 0xff},
	} {
		t.Run(name, func(t *testing.T) {
			d, _, url, _ := directNode(t)
			conn, err := api.DialWSConn(context.Background(), url, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if first != nil {
				if _, err := conn.Write(first); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 3; i++ {
				if _, err := conn.Write(chunk); err != nil {
					break // the node already closed
				}
			}
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			if _, err := conn.Read(make([]byte, 1)); err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("read after %d pre-auth bytes: %v, want the node to close", 3*len(chunk), err)
			}
			waitFor(t, func() bool { return d.directPreAuthForTest() == 0 })
		})
	}
}

func TestDirectKeepaliveFreesSlot(t *testing.T) {
	d, _, url, _ := directNode(t)
	d.direct.limits.maxAdmitted = 1
	d.direct.limits.keepalive = 50 * time.Millisecond
	d.direct.limits.keepaliveTimeout = 50 * time.Millisecond
	d.direct.limits.keepaliveFailures = 1
	kp := mustKeyPair(t)
	bindClient(t, d, kp)

	// A client that handshakes and then never reads again.
	conn, err := api.DialWSConn(context.Background(), url, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, msg1, err := e2e.NewInitiator(kp, d.identity.Public, api.ChannelPrologue(d.id, directChanID))
	if err != nil {
		t.Fatal(err)
	}
	hf, _ := api.MarshalHandshakeFrame(directChanID, msg1)
	if _, err := conn.Write(append(hf, '\n')); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return d.directAdmittedForTest() == 1 })
	waitFor(t, func() bool { return d.directAdmittedForTest() == 0 })
	mustAdmit(t, d, url, kp)
}

func TestDirectSendsCBORWhenClientDeclares(t *testing.T) {
	d, _, url, _ := directNode(t)
	handleTestEmit(d)
	kp := mustKeyPair(t)
	bindClient(t, d, kp)
	c := mustAdmit(t, d, url, kp)
	if f := clientIdentifyThenEmit(t, c.ch, c.peer, c.frames, clientCBORParams); f.Enc != api.EncodingCBOR {
		t.Fatal("terminal.output on a direct link after the client declared cbor: want CBOR")
	}

	c2 := mustAdmit(t, d, url, kp)
	if f := clientIdentifyThenEmit(t, c2.ch, c2.peer, c2.frames, nil); f.Enc != api.EncodingJSON {
		t.Fatal("terminal.output on a direct link without a cbor declaration: want JSON")
	}
}

func TestDirectStreamsSessionSnapshot(t *testing.T) {
	d, _, url, _ := directNode(t)
	d.reg.ReconcileSessions("claude", discovered("%1"))
	kp := mustKeyPair(t)
	bindClient(t, d, kp)
	c := mustAdmit(t, d, url, kp)
	if ev := awaitSessionEvent(t, c.ch, c.frames); ev.Session.ID != "argus:%1" {
		t.Fatalf("snapshot session = %q, want argus:%%1", ev.Session.ID)
	}
}

func (d *Node) directPreAuthForTest() int {
	d.direct.mu.Lock()
	defer d.direct.mu.Unlock()
	return d.direct.preAuth
}

func (d *Node) directAdmittedForTest() int {
	d.direct.mu.Lock()
	defer d.direct.mu.Unlock()
	return d.direct.admitted
}

func (d *Node) directRespondersForTest() []*relayResponder { return d.directResponders() }

const (
	plainTok  = "client-token-one"
	plainTok2 = "client-token-two"
	masterTok = "master-token"
)

// plainDirectNode returns a plaintext node with a fake uplink that pairs
// plainTok and plainTok2, its master token set, and a /p2p server.
func plainDirectNode(t *testing.T) (d *Node, gw *api.Peer, url string) {
	t.Helper()
	d = newE2ETestNode(t)
	d.SetMasterToken(masterTok)
	d.SetTailnet(&fakeTailnet{})
	r := d.newRelayResponder(nil)
	gw, _ = gatewayUplink(t, r)
	d.activeResponder.Store(r)
	t.Cleanup(func() { d.activeResponder.CompareAndSwap(r, nil) })
	r.binds.setValid([]string{clienttoken.HashToken(plainTok), clienttoken.HashToken(plainTok2)})
	return d, gw, serveDirectForTest(t, d)
}

type plainClient struct {
	t      *testing.T
	d      *Node
	peer   *api.Peer
	ch     *api.Channel
	frames chan api.RelayFrame
}

// dialPlain connects to url; dispatch, when set, answers the node's requests.
func dialPlain(t *testing.T, d *Node, url string, dispatch api.DispatchFunc) *plainClient {
	t.Helper()
	c := &plainClient{t: t, d: d, frames: make(chan api.RelayFrame, 64), ch: api.NewPlainChannel(directChanID)}
	peer, err := api.DialWSPeer(context.Background(), url, "", nil, api.PeerOptions{
		Dispatch: dispatch,
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) {
			select {
			case c.frames <- f:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	c.peer = peer
	t.Cleanup(func() { peer.Close() })
	return c
}

func newNonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// challenge sends token.challenge for tok and returns the raw result.
func (c *plainClient) challenge(tok, nonce string) (json.RawMessage, error) {
	c.t.Helper()
	tag, err := api.P2PTag(clienttoken.HashToken(tok), nonce)
	if err != nil {
		c.t.Fatal(err)
	}
	var raw json.RawMessage
	err = c.callTimeout(api.MethodTokenChallenge, api.TokenChallengeParams{ChanID: directChanID, Nonce: nonce, Tag: tag}, &raw)
	return raw, err
}

func (c *plainClient) present(tok string) error {
	c.t.Helper()
	return c.callTimeout(api.MethodTokenPresent, api.TokenPresentParams{ChanID: directChanID, Token: tok}, nil)
}

func (c *plainClient) callTimeout(method string, params, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.peer.CallContext(ctx, method, params, out)
}

// admit runs the full exchange for tok and checks the node's MAC.
func (c *plainClient) admit(tok string) {
	c.t.Helper()
	nonce := newNonce(c.t)
	raw, err := c.challenge(tok, nonce)
	if err != nil {
		c.t.Fatalf("token.challenge: %v", err)
	}
	var res api.TokenChallengeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		c.t.Fatal(err)
	}
	want, _ := api.P2PMAC(clienttoken.HashToken(tok), c.d.id, directChanID, nonce)
	if res.MAC != want {
		c.t.Fatalf("mac = %q, want %q", res.MAC, want)
	}
	if err := c.present(tok); err != nil {
		c.t.Fatalf("token.present: %v", err)
	}
}

func (c *plainClient) call(method string, params json.RawMessage) (json.RawMessage, *api.RPCError) {
	c.t.Helper()
	return callOver(c.t, c.ch, c.peer, c.frames, c.d.id, method, params)
}

func (c *plainClient) waitClosed() {
	c.t.Helper()
	select {
	case <-c.peer.Done():
	case <-time.After(5 * time.Second):
		c.t.Fatal("direct connection still open, want closed")
	}
}

// expectEchoAndSnapshot opens the channel with a test.echo request and waits
// for both its reply and the session snapshot (argus:%1) the channel streams.
func (c *plainClient) expectEchoAndSnapshot() {
	t, d := c.t, c.d
	t.Helper()
	// The first channel frame opens the channel, which streams the snapshot.
	id := json.RawMessage("1")
	req, _ := c.ch.SealRequestFrame(api.EncodingJSON, &id, "test.echo", d.id, json.RawMessage(`{"a":1}`))
	if err := c.peer.SendRawFrame(api.EncodingJSON, req); err != nil {
		t.Fatal(err)
	}
	var echoed, snapshot bool
	for deadline := time.After(5 * time.Second); !echoed || !snapshot; {
		select {
		case f := <-c.frames:
			if f.Method == "" {
				res, rpcErr, err := c.ch.OpenResponse(f)
				if err != nil || rpcErr != nil || string(res) != `{"a":1}` {
					t.Fatalf("echo = %s, %v, %v", res, rpcErr, err)
				}
				echoed = true
				continue
			}
			p, err := c.ch.OpenParams(f)
			if err != nil {
				t.Fatal(err)
			}
			if f.Method == api.MethodSessionEvent && strings.Contains(string(p), `"argus:%1"`) {
				snapshot = true
			}
		case <-deadline:
			t.Fatalf("echoed %v, snapshot %v; want both", echoed, snapshot)
		}
	}
}

// identifyAs answers node.identify with id.
func identifyAs(id string) api.DispatchFunc {
	return func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		if method != api.MethodNodeIdentify {
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
		}
		return api.IdentifyResult{ID: id}, nil
	}
}

func rosterPlain(d *Node, id string) {
	d.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{ID: id, Online: true}})
}

func TestPlainUnknownTagClosed(t *testing.T) {
	d, _, url := plainDirectNode(t)
	c := dialPlain(t, d, url, nil)
	if _, err := c.challenge("unknown-token", newNonce(t)); err == nil {
		t.Fatal("token.challenge with an unknown tag answered")
	}
	c.waitClosed()
}

func TestPlainChallengeNeverEchoesHash(t *testing.T) {
	d, _, url := plainDirectNode(t)
	c := dialPlain(t, d, url, nil)
	nonce := newNonce(t)
	raw, err := c.challenge(plainTok, nonce)
	if err != nil {
		t.Fatalf("token.challenge: %v", err)
	}
	h := clienttoken.HashToken(plainTok)
	if strings.Contains(string(raw), h) {
		t.Fatalf("challenge reply %s carries the token hash", raw)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 1 || fields["mac"] == nil {
		t.Fatalf("challenge reply %s, want only mac", raw)
	}
	want, _ := api.P2PMAC(h, d.id, directChanID, nonce)
	if string(fields["mac"]) != `"`+want+`"` {
		t.Fatalf("mac = %s, want %q", fields["mac"], want)
	}
}

func TestPlainWrongTokenClosed(t *testing.T) {
	d, _, url := plainDirectNode(t)
	c := dialPlain(t, d, url, nil)
	if _, err := c.challenge(plainTok, newNonce(t)); err != nil {
		t.Fatalf("token.challenge: %v", err)
	}
	// plainTok2 is valid too, but not the token the challenge matched.
	if err := c.present(plainTok2); err == nil {
		t.Fatal("token.present with another token answered")
	}
	c.waitClosed()

	// A token.present for another channel id is refused as well.
	c = dialPlain(t, d, url, nil)
	if _, err := c.challenge(plainTok, newNonce(t)); err != nil {
		t.Fatalf("token.challenge: %v", err)
	}
	err := c.callTimeout(api.MethodTokenPresent, api.TokenPresentParams{ChanID: "pfedcba9876543210", Token: plainTok}, nil)
	if err == nil {
		t.Fatal("token.present on another chan_id answered")
	}
	c.waitClosed()
}

func TestPlainAdmitsValidToken(t *testing.T) {
	d, _, url := plainDirectNode(t)
	d.reg.ReconcileSessions("claude", discovered("%1"))
	handleEcho(d)
	d.server.Handle("orch.test", func(context.Context, json.RawMessage) (any, error) { return "ok", nil })
	c := dialPlain(t, d, url, nil)
	c.admit(plainTok)

	c.expectEchoAndSnapshot()
	if _, rpcErr := c.call("orch.test", nil); rpcErr == nil || rpcErr.Code != api.CodeMethodNotFound {
		t.Fatalf("orch.test on a plaintext direct client channel: %v, want method not found", rpcErr)
	}
	if d.directAdmittedForTest() != 1 {
		t.Fatalf("admitted = %d, want 1", d.directAdmittedForTest())
	}
	// Other frames on the channel id are refused.
	if err := c.callTimeout(api.MethodNodeIdentify, nil, nil); !isCode(err, api.CodeMethodNotFound) {
		t.Fatalf("plain node.identify after admission: %v, want method not found", err)
	}
}

func TestPlainListenP2P(t *testing.T) {
	d := newE2ETestNode(t)
	d.SetTailnet(&fakeTailnet{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.ListenP2P(ctx, []string{"127.0.0.1:0"})
}

func TestPlainRevokedTokenClosed(t *testing.T) {
	d, gw, url := plainDirectNode(t)
	c := dialPlain(t, d, url, nil)
	c.admit(plainTok)
	keep := dialPlain(t, d, url, nil)
	keep.admit(plainTok2)
	if _, rpcErr := keep.call(api.MethodPing, nil); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	// Admitted but no channel frame yet: revocation must still reach it.
	pending := dialPlain(t, d, url, nil)
	pending.admit(plainTok)

	notifyGW(t, gw, api.MethodTokenListChanged, api.TokenListChangedParams{Hashes: []string{clienttoken.HashToken(plainTok2)}})
	c.waitClosed()
	pending.waitClosed()
	if _, rpcErr := keep.call(api.MethodPing, nil); rpcErr != nil {
		t.Fatalf("ping on a still-valid plaintext direct channel: %v", rpcErr)
	}
}

func TestPlainQuarantineClosesDirect(t *testing.T) {
	d, _, url := plainDirectNode(t)
	c := dialPlain(t, d, url, nil)
	c.admit(plainTok)
	if _, rpcErr := c.call(api.MethodPing, nil); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	d.trustGate.Trip([]byte("genesis"))
	d.reevaluateTrustChannels()
	c.waitClosed()

	c = dialPlain(t, d, url, nil)
	if _, err := c.challenge(plainTok, newNonce(t)); err != nil {
		t.Fatalf("token.challenge: %v", err)
	}
	if err := c.present(plainTok); err == nil {
		t.Fatal("token.present on a quarantined node answered")
	}
	c.waitClosed()
}

func TestPlainPeerViaIdentify(t *testing.T) {
	d, _, url := plainDirectNode(t)
	rosterPlain(d, "b")
	d.server.Handle("orch.test", func(ctx context.Context, _ json.RawMessage) (any, error) {
		id, _ := peerNodeFrom(ctx)
		return id, nil
	})
	c := dialPlain(t, d, url, identifyAs("b"))
	c.admit(masterTok)
	res, rpcErr := c.call("orch.test", nil)
	if rpcErr != nil || string(res) != `"b"` {
		t.Fatalf("orch.test on a plaintext direct peer channel = %s, %v; want \"b\"", res, rpcErr)
	}

	// Leaving the roster closes the peer channel; it is never demoted.
	d.roster.apply(api.NodeEvent{Type: api.NodeEventRemoved, Node: api.NodeDescriptor{ID: "b"}})
	c.waitClosed()
}

// TestPlainPeerNotInRosterServedAsClient: a master-token opener naming a
// node outside the roster is an admin client, never a peer.
func TestPlainPeerNotInRosterServedAsClient(t *testing.T) {
	d, _, url := plainDirectNode(t)
	rosterPlain(d, "b")
	d.reg.ReconcileSessions("claude", discovered("%1"))
	handleEcho(d)
	c := dialPlain(t, d, url, identifyAs("zzz"))
	c.admit(masterTok)
	c.expectClientChannel()
}

// TestPlainMasterTokenClientServedAsClient: an admin client holds the master
// token and does not serve node.identify; it gets a client channel.
func TestPlainMasterTokenClientServedAsClient(t *testing.T) {
	t.Run("identify refused", func(t *testing.T) {
		d, _, url := plainDirectNode(t)
		d.reg.ReconcileSessions("claude", discovered("%1"))
		handleEcho(d)
		c := dialPlain(t, d, url, nil)
		c.admit(masterTok)
		c.expectClientChannel()
	})
	t.Run("identify unanswered", func(t *testing.T) {
		d, _, url := plainDirectNode(t)
		d.direct.limits.identifyTimeout = 50 * time.Millisecond
		d.reg.ReconcileSessions("claude", discovered("%1"))
		handleEcho(d)
		stall := make(chan struct{})
		t.Cleanup(func() { close(stall) })
		c := dialPlain(t, d, url, func(context.Context, string, json.RawMessage) (any, error) {
			<-stall
			return nil, nil
		})
		start := time.Now()
		c.admit(masterTok)
		if el := time.Since(start); el > 2*time.Second {
			t.Fatalf("admission took %v with an unanswered node.identify", el)
		}
		c.expectClientChannel()
	})
}

// expectClientChannel checks the admitted channel is a client channel: it
// streams the session snapshot and refuses peer-only methods.
func (c *plainClient) expectClientChannel() {
	c.t.Helper()
	c.expectEchoAndSnapshot()
	if _, rpcErr := c.call(api.MethodOrchPeerDeliver, json.RawMessage(`{}`)); rpcErr == nil || rpcErr.Code != api.CodeMethodNotFound {
		c.t.Fatalf("orch.peer.deliver on a client channel: %v, want method not found", rpcErr)
	}
}

func TestPlainPeerSelfIDClosed(t *testing.T) {
	d, _, url := plainDirectNode(t)
	rosterPlain(d, d.id)
	c := dialPlain(t, d, url, identifyAs(d.id))
	if _, err := c.challenge(masterTok, newNonce(t)); err != nil {
		t.Fatalf("token.challenge: %v", err)
	}
	if err := c.present(masterTok); err == nil {
		t.Fatal("token.present by an opener claiming the node's own id answered")
	}
	c.waitClosed()
}

func TestPlainNeverLogsToken(t *testing.T) {
	var logs syncBuffer
	d, _, url := plainDirectNode(t)
	d.SetLogger(debugLogger(&logs))
	rosterPlain(d, "b")

	c := dialPlain(t, d, url, nil)
	c.admit(plainTok)
	if _, rpcErr := c.call(api.MethodPing, nil); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	bad := dialPlain(t, d, url, nil)
	_, _ = bad.challenge(plainTok2, newNonce(t))
	_ = bad.present(plainTok)
	bad.waitClosed()
	peer := dialPlain(t, d, url, identifyAs(d.id))
	_, _ = peer.challenge(masterTok, newNonce(t))
	_ = peer.present(masterTok)
	peer.waitClosed()

	out := logs.String()
	for _, s := range []string{plainTok, plainTok2, masterTok, clienttoken.HashToken(plainTok), clienttoken.HashToken(plainTok2), clienttoken.HashToken(masterTok)} {
		if strings.Contains(out, s) {
			t.Fatalf("log carries %q:\n%s", s, out)
		}
	}
}

func TestPlainRequiresChallengeFirst(t *testing.T) {
	t.Run("present first", func(t *testing.T) {
		d, _, url := plainDirectNode(t)
		c := dialPlain(t, d, url, nil)
		if err := c.present(plainTok); err == nil {
			t.Fatal("token.present before token.challenge answered")
		}
		c.waitClosed()
	})
	t.Run("other request first", func(t *testing.T) {
		d, _, url := plainDirectNode(t)
		c := dialPlain(t, d, url, nil)
		_ = c.callTimeout(api.MethodNodeIdentify, nil, nil)
		c.waitClosed()
	})
	t.Run("channel frame first", func(t *testing.T) {
		d, _, url := plainDirectNode(t)
		c := dialPlain(t, d, url, nil)
		id := json.RawMessage("1")
		req, _ := c.ch.SealRequestFrame(api.EncodingJSON, &id, api.MethodPing, d.id, nil)
		if err := c.peer.SendRawFrame(api.EncodingJSON, req); err != nil {
			t.Fatal(err)
		}
		c.waitClosed()
	})
	t.Run("channel frame before present", func(t *testing.T) {
		d, _, url := plainDirectNode(t)
		c := dialPlain(t, d, url, nil)
		if _, err := c.challenge(plainTok, newNonce(t)); err != nil {
			t.Fatal(err)
		}
		id := json.RawMessage("1")
		req, _ := c.ch.SealRequestFrame(api.EncodingJSON, &id, api.MethodPing, d.id, nil)
		if err := c.peer.SendRawFrame(api.EncodingJSON, req); err != nil {
			t.Fatal(err)
		}
		c.waitClosed()
	})
	t.Run("second challenge", func(t *testing.T) {
		d, _, url := plainDirectNode(t)
		c := dialPlain(t, d, url, nil)
		if _, err := c.challenge(plainTok, newNonce(t)); err != nil {
			t.Fatal(err)
		}
		_, _ = c.challenge(plainTok, newNonce(t))
		c.waitClosed()
	})
	t.Run("bad chan id", func(t *testing.T) {
		d, _, url := plainDirectNode(t)
		c := dialPlain(t, d, url, nil)
		nonce := newNonce(t)
		tag, _ := api.P2PTag(clienttoken.HashToken(plainTok), nonce)
		_ = c.callTimeout(api.MethodTokenChallenge, api.TokenChallengeParams{ChanID: "c1", Nonce: nonce, Tag: tag}, nil)
		c.waitClosed()
	})
}

// bindMany binds n fresh keys to h1, enough to push older keys past the
// per-token cap.
func bindMany(d *Node, n int) {
	for range n {
		kp, _ := e2e.GenerateKeyPair()
		d.bindings().bind(kp.Public, "h1")
	}
}

func TestTokenRevokeClosesDirectAfterManyBinds(t *testing.T) {
	d, gw, url, _ := directNode(t)
	kp := mustKeyPair(t)
	bindClient(t, d, kp)
	c := mustAdmit(t, d, url, kp)
	bindMany(d, maxBindingsPerToken)

	notifyGW(t, gw, api.MethodTokenListChanged, api.TokenListChangedParams{Hashes: []string{"h2"}})
	c.waitClosed()
}

func TestLiveDirectBindingSurvivesManyBinds(t *testing.T) {
	d, _, url, _ := directNode(t)
	kp := mustKeyPair(t)
	bindClient(t, d, kp)
	c := mustAdmit(t, d, url, kp)
	bindMany(d, maxBindingsPerToken)

	d.reevaluateTrustChannels()
	if _, rpcErr := c.call(api.MethodPing, nil); rpcErr != nil {
		t.Fatalf("ping after a trust recheck: %v", rpcErr)
	}
}
