package node

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func testKey(i int) []byte {
	return bytes.Repeat([]byte{byte(i), byte(i >> 8)}, 16)
}

func TestBindAdmitsOnlyValidToken(t *testing.T) {
	s := newBindingStore("master")
	s.setValid([]string{"h1"})
	s.bind(testKey(1), "h1")
	s.bind(testKey(2), "h2")
	if !s.admits(testKey(1)) {
		t.Fatal("key bound to a listed token must be admitted")
	}
	if s.admits(testKey(2)) {
		t.Fatal("key bound to an unlisted token must not be admitted")
	}
	if s.admits(testKey(3)) {
		t.Fatal("unbound key must not be admitted")
	}
	if s.tokenValid("") {
		t.Fatal("empty hash must not be valid")
	}
}

func TestMasterHashAlwaysValid(t *testing.T) {
	s := newBindingStore("master")
	s.bind(testKey(1), "master")
	if !s.tokenValid("master") || !s.admits(testKey(1)) {
		t.Fatal("master hash must be valid with no client list")
	}
	if revoked := s.setValid([]string{"h1"}); len(revoked) != 0 {
		t.Fatalf("setValid revoked %d master-bound keys", len(revoked))
	}
	if !s.admits(testKey(1)) {
		t.Fatal("master hash must stay valid after the client list changes")
	}
	if newBindingStore("").tokenValid("") {
		t.Fatal("no master token must not make the empty hash valid")
	}
}

func TestSetValidReturnsRevoked(t *testing.T) {
	s := newBindingStore("master")
	s.setValid([]string{"h1", "h2"})
	s.bind(testKey(1), "h1")
	s.bind(testKey(2), "h2")
	s.bind(testKey(3), "h1")
	revoked := s.setValid([]string{"h2"})
	if len(revoked) != 2 {
		t.Fatalf("revoked = %d keys, want 2", len(revoked))
	}
	got := map[string]bool{}
	for _, k := range revoked {
		got[string(k)] = true
	}
	if !got[string(testKey(1))] || !got[string(testKey(3))] {
		t.Fatal("revoked must hold the keys bound to the removed token")
	}
	if s.admits(testKey(1)) || s.admits(testKey(3)) || !s.admits(testKey(2)) {
		t.Fatal("only keys of the remaining token must be admitted")
	}
	// The token returning does not restore the dropped bindings.
	s.setValid([]string{"h1", "h2"})
	if s.admits(testKey(1)) {
		t.Fatal("a revoked binding must not come back with its token")
	}
}

func TestBindCapsPerHash(t *testing.T) {
	s := newBindingStore("master")
	s.setValid([]string{"h1"})
	for i := 0; i < maxBindingsPerToken+1; i++ {
		s.bind(testKey(i), "h1")
	}
	if s.admits(testKey(0)) {
		t.Fatal("oldest key past the per-token cap must be evicted")
	}
	for i := 1; i <= maxBindingsPerToken; i++ {
		if !s.admits(testKey(i)) {
			t.Fatalf("key %d must still be bound", i)
		}
	}
}

func TestBindCapsTotal(t *testing.T) {
	s := newBindingStore("master")
	var hashes []string
	for i := 0; i < maxBindings; i++ {
		hashes = append(hashes, "h"+strconv.Itoa(i))
	}
	s.setValid(hashes)
	for i := 0; i < maxBindings; i++ {
		s.bind(testKey(i), hashes[i])
	}
	s.bind(testKey(maxBindings), hashes[0])
	if s.admits(testKey(0)) {
		t.Fatal("oldest binding past the total cap must be evicted")
	}
	if !s.admits(testKey(maxBindings)) || !s.admits(testKey(1)) {
		t.Fatal("newer bindings must survive")
	}
	if n := s.countForTest(); n != maxBindings {
		t.Fatalf("bindings = %d, want %d", n, maxBindings)
	}
}

func TestRebindMovesKey(t *testing.T) {
	s := newBindingStore("master")
	s.setValid([]string{"h1", "h2"})
	s.bind(testKey(1), "h1")
	s.bind(testKey(1), "h2")
	if revoked := s.setValid([]string{"h2"}); len(revoked) != 0 {
		t.Fatalf("key moved to h2 was revoked with h1: %d", len(revoked))
	}
	if !s.admits(testKey(1)) {
		t.Fatal("rebound key must follow its new token")
	}
	if revoked := s.setValid(nil); len(revoked) != 1 {
		t.Fatalf("revoked = %d, want the moved key once", len(revoked))
	}
}

// notifyGW sends a gateway->node notification over the fake uplink.
func notifyGW(t *testing.T, gw *api.Peer, method string, params any) {
	t.Helper()
	if err := gw.Notify(method, params); err != nil {
		t.Fatalf("notify %s: %v", method, err)
	}
}

func TestRelayHandshakeBindsKey(t *testing.T) {
	d := newE2ETestNode(t)
	r := d.newRelayResponder(nil)
	gw, fromNode := gatewayUplink(t, r)
	kp := mustKeyPair(t)

	notifyGW(t, gw, api.MethodTokenListChanged, api.TokenListChangedParams{Hashes: []string{"h1"}})
	notifyGW(t, gw, api.MethodChannelOpened, api.ChannelOpenedParams{ChanID: "c1", TokenHash: "h1"})
	handshakeOver(t, r, gw, fromNode, "c1", kp)
	waitFor(t, func() bool { return r.binds.admits(kp.Public) })
	if cs := r.lookup("c1"); cs == nil || r.channelPeer(cs) != "" {
		t.Fatal("channel.opened without node_id must be a client channel")
	}

	// The token is unpaired: the binding goes.
	notifyGW(t, gw, api.MethodTokenListChanged, api.TokenListChangedParams{Hashes: nil})
	waitFor(t, func() bool { return !r.binds.admits(kp.Public) })
}

func TestNoChannelOpenedNoBind(t *testing.T) {
	d := newE2ETestNode(t)
	r := d.newRelayResponder(nil)
	gw, fromNode := gatewayUplink(t, r)
	notifyGW(t, gw, api.MethodTokenListChanged, api.TokenListChangedParams{Hashes: []string{"h1"}})

	kp := mustKeyPair(t)
	handshakeOver(t, r, gw, fromNode, "c1", kp)

	// A failed handshake consumes channel.opened without binding.
	bad := mustKeyPair(t)
	notifyGW(t, gw, api.MethodChannelOpened, api.ChannelOpenedParams{ChanID: "c2", TokenHash: "h1"})
	hf, _ := api.MarshalHandshakeFrame("c2", []byte("not a noise message"))
	if err := gw.SendRawFrame(api.EncodingJSON, hf); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return r.pendingPeerCountForTest() == 0 })

	// Locked-mode rejection never binds.
	locked := buildLockedResponder(t, mustKeyPair(t).Public)
	locked.binds.setValid([]string{"h1"})
	locked.notePeerOpened("enforce-test-chan", "", "h1")
	if runClientHandshake(t, locked, bad) {
		t.Fatal("unauthorized client must be rejected")
	}

	time.Sleep(50 * time.Millisecond)
	if r.binds.countForTest() != 0 || locked.binds.countForTest() != 0 {
		t.Fatal("no binding without a completed, accepted handshake carrying channel.opened")
	}
}

func TestPeerChannelNeverBinds(t *testing.T) {
	d := newE2ETestNode(t)
	d.SetE2EE(true)
	r := d.newRelayResponder(nil)
	gw, fromNode := gatewayUplink(t, r)
	bKP := mustKeyPair(t)
	rosterKey(d, "b", bKP.Public)

	notifyGW(t, gw, api.MethodTokenListChanged, api.TokenListChangedParams{Hashes: []string{"h1"}})
	notifyGW(t, gw, api.MethodChannelOpened, api.ChannelOpenedParams{ChanID: "n1", TokenHash: "h1", NodeID: "b"})
	handshakeOver(t, r, gw, fromNode, "n1", bKP)
	if cs := r.lookup("n1"); cs == nil || r.channelPeer(cs) != "b" {
		t.Fatalf("n1 = %+v, want a peer channel from b", cs)
	}
	time.Sleep(50 * time.Millisecond)
	if r.binds.admits(bKP.Public) || r.binds.countForTest() != 0 {
		t.Fatal("a peer channel must never bind its key")
	}
}

func TestLegacyPeerOpenedStillMarksPeer(t *testing.T) {
	d := newE2ETestNode(t)
	d.SetE2EE(true)
	r := d.newRelayResponder(nil)
	gw, fromNode := gatewayUplink(t, r)
	bKP := mustKeyPair(t)
	rosterKey(d, "b", bKP.Public)

	announcePeer(t, gw, "n1", "b")
	handshakeOver(t, r, gw, fromNode, "n1", bKP)
	if cs := r.lookup("n1"); cs == nil || r.channelPeer(cs) != "b" {
		t.Fatalf("n1 = %+v, want a peer channel from b", cs)
	}
	if r.binds.countForTest() != 0 {
		t.Fatal("relay.peer_opened must not bind")
	}
}

func TestUplinkCloseClearsBindings(t *testing.T) {
	agg := gateway.New(time.Second)
	hsrv := gateway.NewServer(agg,
		func(tok string) bool { return tok == "dtok" },
		func(tok string) bool { return tok == "ctok" },
	)
	ts := httptest.NewServer(hsrv.Handler())
	defer ts.Close()

	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.SetIdentity("home", "home-box")
	d.SetMasterToken("dtok")
	if d.bindings() != nil {
		t.Fatal("no bindings without an uplink")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.ConnectGateway(ctx, wsURL(ts.URL)+"/node", "dtok", nil) }()

	waitFor(t, func() bool { return d.bindings() != nil })
	s := d.bindings()
	key := testKey(1)
	s.bind(key, clienttoken.HashToken("dtok"))
	if !s.admits(key) {
		t.Fatal("key bound to the master token must be admitted")
	}

	cancel()
	<-done
	if d.bindings() != nil {
		t.Fatal("bindings must be nil once the uplink closes")
	}
	if s.admits(key) || s.countForTest() != 0 {
		t.Fatal("closing the uplink must clear its bindings")
	}
}
