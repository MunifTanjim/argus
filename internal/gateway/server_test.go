package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

func gwWSURL(httpURL, route string) string {
	return "ws://" + strings.TrimPrefix(httpURL, "http://") + route
}

// marshalChainForTest builds a marshalled chain of n entries.
func marshalChainForTest(t *testing.T, n int) []byte {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	for i := 1; i < n; i++ {
		dev := bytes.Repeat([]byte{byte(i)}, 32)
		if err := log.AuthorizeDevice(dev, signer); err != nil {
			t.Fatalf("AuthorizeDevice: %v", err)
		}
	}
	return trustlog.MarshalChain(log.Entries())
}

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
	entries, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}

	// First push: new entries → the other node gets trustlog.changed, client gets
	// nothing, and the pushing node is not told about its own change.
	if err := offerer.Call(api.MethodTrustLogPush, api.TrustLogPushParams{Entries: entries}, nil); err != nil {
		t.Fatalf("push: %v", err)
	}
	select {
	case n := <-nodeGot:
		if n.Method != api.MethodTrustLogChanged {
			t.Fatalf("node got notification method %q, want %q", n.Method, api.MethodTrustLogChanged)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("node did not receive trustlog.changed after new entries were pushed")
	}
	select {
	case n := <-offererGot:
		t.Fatalf("the pushing node must not be notified of its own push, got %q", n.Method)
	case <-time.After(200 * time.Millisecond):
		// correct: self-notification would burn the pusher's own pull budget
	}
	select {
	case n := <-clientGot:
		t.Fatalf("client must not receive %q; clients learn from NodeEventBeacon", n.Method)
	case <-time.After(200 * time.Millisecond):
		// correct: client is not in nodePeers
	}

	// Re-push of the same entries: no new insertion → no notification.
	if err := offerer.Call(api.MethodTrustLogPush, api.TrustLogPushParams{Entries: entries}, nil); err != nil {
		t.Fatalf("re-push: %v", err)
	}
	select {
	case n := <-nodeGot:
		t.Fatalf("unexpected notification %q for a re-push of known entries", n.Method)
	case <-time.After(200 * time.Millisecond):
		// correct: no notification for known entries
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

// TestGatewayDisjointSuppressedWhenTruncated asserts both call sites in
// server.go: when Truncated is set the gateway must suppress the Disjoint flag
// so that a caller who under-reported due to the cap is not falsely quarantined.
func TestGatewayDisjointSuppressedWhenTruncated(t *testing.T) {
	s := NewServer(New(0), nil, nil)

	chain := marshalChainForTest(t, 2)
	entries, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	s.entries.PutAll(entries)

	// A hash the gateway does not hold — makes the offer disjoint.
	unknownHash := bytes.Repeat([]byte{0xAB}, 32)

	// Node uplink path.
	for _, truncated := range []bool{true, false} {
		params := mustMarshal(api.TrustLogSyncParams{Known: [][]byte{unknownHash}, Truncated: truncated})
		res, err := s.nodeDispatch(context.Background(), nil, api.MethodTrustLogSync, params)
		if err != nil {
			t.Fatalf("nodeDispatch truncated=%v: %v", truncated, err)
		}
		got := res.(api.TrustLogSyncResult).Disjoint
		want := !truncated
		if got != want {
			t.Fatalf("node path truncated=%v: Disjoint=%v, want %v", truncated, got, want)
		}
	}

	// Client server path.
	dispatch := s.clientSrv.DispatchFunc()
	for _, truncated := range []bool{true, false} {
		params := mustMarshal(api.TrustLogSyncParams{Known: [][]byte{unknownHash}, Truncated: truncated})
		res, err := dispatch(context.Background(), api.MethodTrustLogSync, params)
		if err != nil {
			t.Fatalf("client dispatch truncated=%v: %v", truncated, err)
		}
		got := res.(api.TrustLogSyncResult).Disjoint
		want := !truncated
		if got != want {
			t.Fatalf("client path truncated=%v: Disjoint=%v, want %v", truncated, got, want)
		}
	}
}
