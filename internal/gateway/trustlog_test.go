package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

func gwWSURL(httpURL, route string) string {
	return "ws://" + strings.TrimPrefix(httpURL, "http://") + route
}

// twoEntryChain returns a genesis+authorize chain (2 entries) and a genesis-only
// chain (1 entry) sharing the same signer, for keep-longest assertions.
func twoEntryChain(t *testing.T) (short, long []byte) {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	short = trustlog.MarshalChain(log.Entries())
	dev := make([]byte, 32)
	if err := log.AuthorizeDevice(dev, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	long = trustlog.MarshalChain(log.Entries())
	return short, long
}

func TestTrustStoreKeepsLongest(t *testing.T) {
	short, long := twoEntryChain(t)
	ts := &trustStore{}
	if ts.current() != nil {
		t.Fatal("empty store should return nil")
	}
	ts.offer(long)
	ts.offer(short) // shorter: ignored
	if got := ts.current(); string(got) != string(long) {
		t.Fatal("store should keep the longer chain")
	}
	ts.offer([]byte("garbage")) // unparseable: ignored
	if got := ts.current(); string(got) != string(long) {
		t.Fatal("garbage offer must not clobber the stored chain")
	}
}

// A node peer offers a chain; a client peer pulls it back — proving both link
// dispatches share one blob through the real Handler.
func TestTrustLogOfferPullThroughGateway(t *testing.T) {
	agg := New(time.Second)
	srv := NewServer(agg, nil, nil)
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	ctx := context.Background()

	_, long := twoEntryChain(t)

	// The gateway calls node.identify on every new node uplink; the test peer
	// must respond so serveNode keeps the connection alive.
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
	if err := nodePeer.Call(api.MethodTrustLogOffer, api.TrustLogChain{Chain: long}, nil); err != nil {
		t.Fatalf("offer: %v", err)
	}

	clientPeer, err := api.DialWSPeer(ctx, gwWSURL(hs.URL, "/client"), "", nil, api.PeerOptions{})
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer clientPeer.Close()
	var got api.TrustLogChain
	if err := clientPeer.Call(api.MethodTrustLogPull, nil, &got); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if string(got.Chain) != string(long) {
		t.Fatalf("pulled chain mismatch: got %d bytes want %d", len(got.Chain), len(long))
	}

	// A client may NOT offer (supplicants never push trust state).
	if err := clientPeer.Call(api.MethodTrustLogOffer, api.TrustLogChain{Chain: long}, nil); err == nil {
		t.Fatal("client trustlog.offer should be rejected")
	}
}
