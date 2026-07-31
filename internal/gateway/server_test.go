package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

func TestNodeDispatchPushDeliverReportsGone(t *testing.T) {
	s := NewServer(New(0), nil, nil)
	var gotBody []byte
	s.SetPushDeliverer(delivererFunc(func(_ context.Context, ep string, body []byte, _, _ string) error {
		gotBody = body
		return push.ErrGone
	}))
	params := mustMarshal(api.PushDeliverParams{Endpoint: "https://p/ep", Ciphertext: base64.StdEncoding.EncodeToString([]byte("opaque"))})
	res, err := s.nodeDispatch(context.Background(), api.MethodPushDeliver, params)
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
