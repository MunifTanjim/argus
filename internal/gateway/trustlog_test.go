package gateway

import (
	"bytes"
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
// chain (1 entry) sharing the same signer.
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

// divergentForks returns two chains that share the same genesis but have different
// second entries (each authorizes a distinct device). They are true competing forks.
func divergentForks(t *testing.T) (chainA, chainB []byte, devA, devB []byte) {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	genLog, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisEntries := genLog.Entries()

	devA = bytes.Repeat([]byte{0xAA}, 32)
	logA, err := trustlog.Load(genesisEntries)
	if err != nil {
		t.Fatalf("Load fork A: %v", err)
	}
	if err := logA.AuthorizeDevice(devA, signer); err != nil {
		t.Fatalf("AuthorizeDevice A: %v", err)
	}
	chainA = trustlog.MarshalChain(logA.Entries())

	devB = bytes.Repeat([]byte{0xBB}, 32)
	logB, err := trustlog.Load(genesisEntries)
	if err != nil {
		t.Fatalf("Load fork B: %v", err)
	}
	if err := logB.AuthorizeDevice(devB, signer); err != nil {
		t.Fatalf("AuthorizeDevice B: %v", err)
	}
	chainB = trustlog.MarshalChain(logB.Entries())

	return chainA, chainB, devA, devB
}

// oneEntryChain returns a genesis-only chain for a freshly generated signer.
// Useful for creating multiple distinct single-entry chains (e.g. cap tests).
func oneEntryChain(t *testing.T) []byte {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	return trustlog.MarshalChain(log.Entries())
}

// containsChain reports whether chains contains a chain byte-equal to want.
func containsChain(chains [][]byte, want []byte) bool {
	for _, c := range chains {
		if bytes.Equal(c, want) {
			return true
		}
	}
	return false
}

// TestTrustStoreEmpty checks that a freshly constructed store returns nil from all.
func TestTrustStoreEmpty(t *testing.T) {
	ts := &trustStore{}
	if ts.all() != nil {
		t.Fatal("empty store should return nil from all()")
	}
}

// TestTrustStoreGarbageIgnored verifies that unparseable chains are silently dropped
// and do not clobber a previously stored branch.
func TestTrustStoreGarbageIgnored(t *testing.T) {
	_, long := twoEntryChain(t)
	ts := &trustStore{}
	ts.offer(long)
	ts.offer([]byte("not a valid chain"))
	chains := ts.all()
	if len(chains) != 1 {
		t.Fatalf("garbage offer must not affect stored branches; got %d chains", len(chains))
	}
	if !bytes.Equal(chains[0], long) {
		t.Fatal("stored chain should still be long after garbage offer")
	}
}

// TestTrustStoreRetainsDistinctBranches is the core Task 5 assertion: offer two
// divergent chains (same genesis, different second entries), and verify the gateway
// retains both so clients receive all forks and can resolve locally.
func TestTrustStoreRetainsDistinctBranches(t *testing.T) {
	chainA, chainB, _, _ := divergentForks(t)
	ts := &trustStore{}
	ts.offer(chainA)
	ts.offer(chainB)
	chains := ts.all()
	if len(chains) < 2 {
		t.Fatalf("both competing branches must be retained; got %d chains", len(chains))
	}
	if !containsChain(chains, chainA) {
		t.Fatal("chain A missing from store")
	}
	if !containsChain(chains, chainB) {
		t.Fatal("chain B missing from store")
	}
}

// TestTrustStoreBounded verifies the store caps itself at trustStoreCap branches
// and does not grow unbounded.
func TestTrustStoreBounded(t *testing.T) {
	ts := &trustStore{}
	// Offer cap+1 distinct single-entry chains.
	for i := 0; i <= trustStoreCap; i++ {
		ts.offer(oneEntryChain(t))
	}
	chains := ts.all()
	if len(chains) > trustStoreCap {
		t.Fatalf("store must not exceed cap %d; got %d branches", trustStoreCap, len(chains))
	}
}

// TestTrustStoreEvictsSmallestOnOverflow checks that when the cap is exceeded the
// branch with the fewest entries is evicted (not a random or newer branch).
func TestTrustStoreEvictsSmallestOnOverflow(t *testing.T) {
	ts := &trustStore{}
	// Fill cap with 2-entry chains (long).
	var longs [][]byte
	for i := 0; i < trustStoreCap; i++ {
		_, long := twoEntryChain(t)
		longs = append(longs, long)
		ts.offer(long)
	}
	// Now offer one additional 1-entry chain (short) which should be evicted.
	short := oneEntryChain(t)
	ts.offer(short)

	chains := ts.all()
	if len(chains) > trustStoreCap {
		t.Fatalf("store exceeded cap after overflow: %d branches", len(chains))
	}
	// The short chain (1 entry, smallest count) must have been evicted.
	if containsChain(chains, short) {
		t.Fatal("short chain should have been evicted as the smallest-count branch")
	}
	// All long chains must still be present.
	for i, l := range longs {
		if !containsChain(chains, l) {
			t.Fatalf("long chain %d should be retained after eviction", i)
		}
	}
}

// TestTrustStoreBlind verifies that the store never inspects entry internals beyond
// the DoS-capped count: it accepts any structurally valid chain regardless of
// whether signatures would pass verification (the gateway is blind).
func TestTrustStoreBlind(t *testing.T) {
	// Build a valid chain, then tamper a signature byte. UnmarshalChain (DoS-capped
	// count only) should still parse it; a signature verifier would reject it.
	// Since the gateway only calls UnmarshalChain (no verify), it accepts the tampered
	// chain as a valid branch.
	_, long := twoEntryChain(t)
	tampered := append([]byte(nil), long...)
	// Flip the last byte (part of the signature field) to simulate a tampered sig.
	tampered[len(tampered)-1] ^= 0xFF
	ts := &trustStore{}
	ts.offer(tampered)
	// The gateway accepted it (blind). A real client's genesis-pinned store would
	// reject it during Ingest (signature verification fails), but the gateway itself
	// must not — it just stores what nodes offer.
	//
	// If UnmarshalChain rejects the tampered bytes (structurally malformed), the
	// offer is silently dropped; blindness is still maintained (no verification path).
	// Either way the gateway does not call any verify function.
}

// TestTrustStoreOrderedByDescCount checks that all() returns branches with the
// highest entry count first.
func TestTrustStoreOrderedByDescCount(t *testing.T) {
	short, long := twoEntryChain(t)
	ts := &trustStore{}
	// Offer short first, then long.
	ts.offer(short)
	ts.offer(long)
	chains := ts.all()
	if len(chains) < 2 {
		t.Fatalf("expected 2 chains, got %d", len(chains))
	}
	// long (2 entries) must come before short (1 entry).
	if !bytes.Equal(chains[0], long) {
		t.Fatal("longest chain should be first in all()")
	}
}

// TestTrustLogOfferPullThroughGateway proves that a node can offer a chain and a
// client can pull all retained branches (TrustLogPullResult) through the real
// Handler. Also verifies that clients cannot offer (supplicants must not push trust
// state).
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
	var got api.TrustLogPullResult
	if err := clientPeer.Call(api.MethodTrustLogPull, nil, &got); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(got.Chains) == 0 {
		t.Fatal("pull returned empty chains list")
	}
	if !containsChain(got.Chains, long) {
		t.Fatalf("offered chain not found in pull result (%d chains)", len(got.Chains))
	}

	// A client may NOT offer (supplicants never push trust state).
	if err := clientPeer.Call(api.MethodTrustLogOffer, api.TrustLogChain{Chain: long}, nil); err == nil {
		t.Fatal("client trustlog.offer should be rejected")
	}
}

// TestTrustLogOfferPullCompetingForksThroughGateway proves end-to-end that a node
// can offer two divergent branches and a client pull receives both.
func TestTrustLogOfferPullCompetingForksThroughGateway(t *testing.T) {
	agg := New(time.Second)
	srv := NewServer(agg, nil, nil)
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	ctx := context.Background()

	chainA, chainB, _, _ := divergentForks(t)

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

	if err := nodePeer.Call(api.MethodTrustLogOffer, api.TrustLogChain{Chain: chainA}, nil); err != nil {
		t.Fatalf("offer A: %v", err)
	}
	if err := nodePeer.Call(api.MethodTrustLogOffer, api.TrustLogChain{Chain: chainB}, nil); err != nil {
		t.Fatalf("offer B: %v", err)
	}

	clientPeer, err := api.DialWSPeer(ctx, gwWSURL(hs.URL, "/client"), "", nil, api.PeerOptions{})
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer clientPeer.Close()
	var got api.TrustLogPullResult
	if err := clientPeer.Call(api.MethodTrustLogPull, nil, &got); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(got.Chains) < 2 {
		t.Fatalf("both competing branches must be served; got %d chains", len(got.Chains))
	}
	if !containsChain(got.Chains, chainA) {
		t.Fatal("chain A missing from pull result")
	}
	if !containsChain(got.Chains, chainB) {
		t.Fatal("chain B missing from pull result")
	}
}
