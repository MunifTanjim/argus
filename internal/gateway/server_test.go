package gateway

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/push"
)

// server.info reports the version set via SetVersion plus every connected node,
// so a client can both show the version and pick a spawn target.
func TestServerInfoReportsVersionAndNodes(t *testing.T) {
	a := New(time.Second)
	a.AddSource(newFakeSource("home", "home-box"))
	a.AddSource(newFakeSource("dev", "dev-box"))
	srv := NewServer(a, nil, nil)
	srv.SetVersion("1.2.3")
	dispatch := srv.clientSrv.DispatchFunc()

	res, err := dispatch(context.Background(), api.MethodServerInfo, nil)
	if err != nil {
		t.Fatalf("server.info dispatch: %v", err)
	}
	raw, _ := json.Marshal(res)
	var info api.ServerInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("decode info: %v (%s)", err, raw)
	}
	if info.Version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", info.Version)
	}
	got := map[string]string{}
	for _, n := range info.Nodes {
		got[n.ID] = n.Label
	}
	if got["home"] != "home-box" || got["dev"] != "dev-box" {
		t.Fatalf("nodes = %+v, want home-box/dev-box", info.Nodes)
	}
}

// delivererFunc adapts a func to push.Deliverer.
type delivererFunc func(context.Context, string, []byte, string, string) error

func (f delivererFunc) Deliver(ctx context.Context, ep string, b []byte, ttl, u string) error {
	return f(ctx, ep, b, ttl, u)
}

func newTestGatewayAndClient(t *testing.T) (*Server, *api.Peer) {
	t.Helper()
	srv := NewServer(New(time.Second), nil, nil)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	cli, err := api.DialWSPeer(context.Background(), gwWSURL(hs.URL, "/client"), "", nil, api.PeerOptions{})
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	t.Cleanup(func() { cli.Close() })
	return srv, cli
}

func offerChain(t *testing.T, srv *Server, chain []byte) {
	t.Helper()
	srv.trust.offer(chain)
}

func TestPullWithheldWhenFingerprintKnown(t *testing.T) {
	srv, cli := newTestGatewayAndClient(t)
	chain := marshalChainForTest(t, 3)
	offerChain(t, srv, chain)

	var first api.TrustLogPullResult
	if err := cli.Call(api.MethodTrustLogPull, nil, &first); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	if len(first.Chains) != 1 || len(first.Fingerprints) != 1 {
		t.Fatalf("first pull: chains=%d fps=%d, want 1 and 1", len(first.Chains), len(first.Fingerprints))
	}

	var second api.TrustLogPullResult
	if err := cli.Call(api.MethodTrustLogPull, api.TrustLogPullParams{Known: first.Fingerprints}, &second); err != nil {
		t.Fatalf("second pull: %v", err)
	}
	if len(second.Chains) != 0 {
		t.Fatalf("a known branch must not be re-sent, got %d chains", len(second.Chains))
	}
	if len(second.Fingerprints) != 1 {
		t.Fatal("fingerprints must always be complete, even when no chain is sent")
	}
}

// An old client sends no params at all; it must keep getting everything.
func TestPullWithoutParamsIsUnchanged(t *testing.T) {
	srv, cli := newTestGatewayAndClient(t)
	offerChain(t, srv, marshalChainForTest(t, 3))

	var res api.TrustLogPullResult
	if err := cli.Call(api.MethodTrustLogPull, nil, &res); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(res.Chains) != 1 {
		t.Fatal("omitted Known must mean send everything")
	}
}

// TestNodeRoutePullWithheldWhenFingerprintKnown exercises the node-uplink dispatch
// path with a Known fingerprint list. If the node route were still calling the old
// code path, this would fail even while the client-route tests pass.
func TestNodeRoutePullWithheldWhenFingerprintKnown(t *testing.T) {
	agg := New(time.Second)
	srv := NewServer(agg, nil, nil)
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	ctx := context.Background()

	chain := marshalChainForTest(t, 3)
	srv.trust.offer(chain)

	nodeDispatch := func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		if method == api.MethodNodeIdentify {
			return api.IdentifyResult{ID: "test-node"}, nil
		}
		return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
	}
	nodePeer, err := api.DialWSPeer(ctx, gwWSURL(hs.URL, "/node"), "", nil, api.PeerOptions{Dispatch: nodeDispatch})
	if err != nil {
		t.Fatalf("dial node: %v", err)
	}
	defer nodePeer.Close()

	var first api.TrustLogPullResult
	if err := nodePeer.Call(api.MethodTrustLogPull, nil, &first); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	if len(first.Chains) != 1 || len(first.Fingerprints) != 1 {
		t.Fatalf("first pull: chains=%d fps=%d, want 1 and 1", len(first.Chains), len(first.Fingerprints))
	}

	var second api.TrustLogPullResult
	if err := nodePeer.Call(api.MethodTrustLogPull, api.TrustLogPullParams{Known: first.Fingerprints}, &second); err != nil {
		t.Fatalf("second pull: %v", err)
	}
	if len(second.Chains) != 0 {
		t.Fatalf("a known branch must not be re-sent over node route, got %d chains", len(second.Chains))
	}
	if len(second.Fingerprints) != 1 {
		t.Fatal("fingerprints must always be complete over node route, even when no chain is sent")
	}
}

// TestPullFingerprintsEmptyNotNullForEmptyStore checks that an empty store emits
// "fingerprints":[] not null. Absent Fingerprints signals an old gateway; present
// but empty signals a gateway that holds nothing. The two must not be conflated.
func TestPullFingerprintsEmptyNotNullForEmptyStore(t *testing.T) {
	_, cli := newTestGatewayAndClient(t)

	var res api.TrustLogPullResult
	if err := cli.Call(api.MethodTrustLogPull, nil, &res); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if res.Fingerprints == nil {
		t.Fatal("empty store must emit Fingerprints:[] not null; nil would be indistinguishable from an old gateway")
	}
	if len(res.Fingerprints) != 0 {
		t.Fatalf("empty store must have 0 fingerprints, got %d", len(res.Fingerprints))
	}
}

// waitForNodePeers blocks until n node uplinks are registered as relay targets.
func waitForNodePeers(t *testing.T, srv *Server, n int) {
	t.Helper()
	eventually(t, func() bool {
		srv.relayMu.Lock()
		defer srv.relayMu.Unlock()
		return len(srv.nodePeers) >= n
	})
}

// TestTrustLogChangedNotifiesPeers checks that offering a new branch sends a
// trustlog.changed notification to the OTHER connected node peers (not clients,
// and not the offering node itself), and that re-offering the same branch (no new
// insertion) sends nothing.
func TestTrustLogChangedNotifiesPeers(t *testing.T) {
	agg := New(time.Second)
	srv := NewServer(agg, nil, nil)
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	ctx := context.Background()

	dialNode := func(id string, got chan api.Notification) *api.Peer {
		t.Helper()
		p, err := api.DialWSPeer(ctx, gwWSURL(hs.URL, "/node"), "", nil, api.PeerOptions{
			Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
				if method == api.MethodNodeIdentify {
					return api.IdentifyResult{ID: id}, nil
				}
				return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
			},
			OnNotify: func(n api.Notification) { got <- n },
		})
		if err != nil {
			t.Fatalf("dial node %s: %v", id, err)
		}
		return p
	}

	// offerer publishes; nodeGot belongs to the peer that must be told about it.
	nodeGot := make(chan api.Notification, 4)
	nodePeer := dialNode("test-node", nodeGot)
	defer nodePeer.Close()

	offererGot := make(chan api.Notification, 4)
	offerer := dialNode("offering-node", offererGot)
	defer offerer.Close()
	waitForNodePeers(t, srv, 2)

	// A client peer must NOT receive trustlog.changed; clients learn from NodeEventBeacon.
	clientGot := make(chan api.Notification, 4)
	clientPeer, err := api.DialWSPeer(ctx, gwWSURL(hs.URL, "/client"), "", nil, api.PeerOptions{
		OnNotify: func(n api.Notification) {
			if n.Method == api.MethodTrustLogChanged {
				clientGot <- n
			}
		},
	})
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer clientPeer.Close()

	chain := marshalChainForTest(t, 2)

	// First offer: new branch → the other node gets trustlog.changed, client gets
	// nothing, and the offering node is not told about its own change.
	if err := offerer.Call(api.MethodTrustLogOffer, api.TrustLogChain{Chain: chain}, nil); err != nil {
		t.Fatalf("offer: %v", err)
	}
	select {
	case n := <-nodeGot:
		if n.Method != api.MethodTrustLogChanged {
			t.Fatalf("node got notification method %q, want %q", n.Method, api.MethodTrustLogChanged)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("node did not receive trustlog.changed after a new branch was offered")
	}
	select {
	case n := <-offererGot:
		t.Fatalf("the offering node must not be notified of its own offer, got %q", n.Method)
	case <-time.After(200 * time.Millisecond):
		// correct: self-notification would burn the offerer's own pull budget
	}
	select {
	case n := <-clientGot:
		t.Fatalf("client must not receive %q; clients learn from NodeEventBeacon", n.Method)
	case <-time.After(200 * time.Millisecond):
		// correct: client is not in nodePeers
	}

	// Second offer of the same chain: no new insertion → no notification.
	if err := offerer.Call(api.MethodTrustLogOffer, api.TrustLogChain{Chain: chain}, nil); err != nil {
		t.Fatalf("re-offer: %v", err)
	}
	select {
	case n := <-nodeGot:
		t.Fatalf("unexpected notification %q for a re-offer of a known branch", n.Method)
	case <-time.After(200 * time.Millisecond):
		// correct: no notification for a known branch
	}
}

func TestNodeDispatchPushDeliverReportsGone(t *testing.T) {
	s := NewServer(New(0), nil, nil)
	var gotBody []byte
	s.SetPushDeliverer(delivererFunc(func(_ context.Context, ep string, body []byte, _, _ string) error {
		gotBody = body
		return push.ErrGone
	}))
	params := mustMarshal(api.PushDeliverParams{Endpoint: "https://p/ep", Ciphertext: base64.StdEncoding.EncodeToString([]byte("opaque"))})
	res, err := s.nodeDispatch(context.Background(), nil, api.MethodPushDeliver, params)
	if err != nil {
		t.Fatalf("nodeDispatch: %v", err)
	}
	if !res.(api.PushDeliverResult).Gone {
		t.Fatal("want Gone=true")
	}
	if string(gotBody) != "opaque" {
		t.Fatalf("deliverer got %q, want decoded ciphertext", gotBody)
	}
}

// TestNodeUplinkServesNodesList verifies that nodes.list is reachable over the
// node-uplink route so a node's syncRoster call populates peer beacon attribution
// rather than receiving method-not-found.
func TestNodeUplinkServesNodesList(t *testing.T) {
	srv := NewServer(New(time.Second), nil, nil)
	alpha := newFakeSource("alpha", "alpha-box")
	alpha.beaconPubKey = "dGVzdC1iZWFjb24tdGVzdA==" // non-empty sentinel value
	srv.agg.AddSource(alpha)
	defer alpha.close()

	nodePeer, _ := adoptFakeNode(t, srv, "beta", "PUBKEY-BETA")
	defer nodePeer.Close()

	var result api.NodesListResult
	if err := nodePeer.Call(api.MethodNodesList, nil, &result); err != nil {
		t.Fatalf("nodes.list over node uplink: %v", err)
	}
	var found bool
	for _, nd := range result.Nodes {
		if nd.ID == "alpha" && nd.BeaconPubKey == "dGVzdC1iZWFjb24tdGVzdA==" {
			found = true
		}
	}
	if !found {
		t.Fatalf("nodes.list result %+v missing alpha with expected beacon pub key", result.Nodes)
	}
}

// TestGatewayPingsNodesAtTheConfiguredInterval covers the half of
// gateway.keepalive-interval that used to be hardcoded: the gateway's own pings to
// each node uplink. Without it an operator raising the interval fleet-wide gets
// only the node-side half of the reduction.
func TestGatewayPingsNodesAtTheConfiguredInterval(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	srv.SetNodeKeepaliveInterval(30 * time.Millisecond)

	gwConn, nodeConn := net.Pipe()
	go srv.serveNode(gwConn)
	defer nodeConn.Close()

	pings := make(chan struct{}, 16)
	go func() {
		sc := bufio.NewScanner(nodeConn)
		enc := json.NewEncoder(nodeConn)
		for sc.Scan() {
			var m struct {
				ID     *json.RawMessage `json:"id"`
				Method string           `json:"method"`
			}
			if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil {
				continue
			}
			switch m.Method {
			case api.MethodNodeIdentify:
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": api.IdentifyResult{ID: "keepalive-node"}})
			case api.MethodPing:
				select {
				case pings <- struct{}{}:
				default:
				}
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": nil})
			}
		}
	}()

	// Three pings well inside the window the 15s default could ever produce one in.
	for i := 0; i < 3; i++ {
		select {
		case <-pings:
		case <-time.After(2 * time.Second):
			t.Fatalf("gateway sent %d pings; gateway.keepalive-interval must drive the gateway's own pings too", i)
		}
	}
}
