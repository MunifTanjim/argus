package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func init() {
	p2pRetryInterval = 300 * time.Millisecond
	p2pKeepaliveInterval = 300 * time.Millisecond
	p2pKeepaliveTimeout = 300 * time.Millisecond
}

func alwaysDialable(context.Context, string) bool { return true }

// freezeConn stops delivering reads once frozen, so the far end's pings go
// unanswered: a half-open link.
type freezeConn struct {
	net.Conn
	frozen *atomic.Bool
	closed chan struct{}
	once   sync.Once
}

func (c *freezeConn) Read(b []byte) (int, error) {
	if c.frozen.Load() {
		<-c.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Read(b)
}

func (c *freezeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// directConnState is one connection to the fake direct listener.
type directConnState struct {
	peer   *api.Peer
	mu     sync.Mutex // serializes sealing between the read loop and notify
	ch     *api.Channel
	chanID string
}

// fakeDirect is a node's direct listener: Noise responder for key, then
// node.identify, sessions.list and echo over the sealed channel.
type fakeDirect struct {
	t      *testing.T
	nodeID string
	key    e2e.KeyPair
	srv    *httptest.Server
	url    string
	frozen atomic.Bool
	refuse atomic.Bool

	mu       sync.Mutex
	hold     chan struct{} // when set, dials wait on it before the upgrade
	listHold chan struct{} // when set, sessions.list replies wait on it
	sessions []session.Session
	conns    []*directConnState
	dials    int
	calls    map[string]int
}

func newFakeDirect(t *testing.T, n *fakeNode, sessions ...session.Session) *fakeDirect {
	t.Helper()
	d := &fakeDirect{t: t, nodeID: n.id, key: n.key, sessions: sessions, calls: map[string]int{}}
	d.srv = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.srv.Close)
	t.Cleanup(d.killAll)
	d.url = "ws" + strings.TrimPrefix(d.srv.URL, "http") + "/p2p"
	return d
}

func (d *fakeDirect) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	d.dials++
	hold := d.hold
	d.mu.Unlock()
	if hold != nil {
		<-hold
	}
	if d.refuse.Load() || r.URL.Path != "/p2p" {
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	conn, err := api.AcceptWS(w, r)
	if err != nil {
		return
	}
	fc := &freezeConn{Conn: conn, frozen: &d.frozen, closed: make(chan struct{})}
	st := &directConnState{}
	st.peer = api.NewPeer(fc, api.PeerOptions{
		OnRelayFrame: func(p *api.Peer, f api.RelayFrame) { d.onFrame(st, p, f) },
	})
	d.mu.Lock()
	d.conns = append(d.conns, st)
	d.mu.Unlock()
}

var directChanIDRe = regexp.MustCompile(`^p[0-9a-f]{16}$`)

func (d *fakeDirect) onFrame(st *directConnState, p *api.Peer, f api.RelayFrame) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if f.Method == api.MethodE2EHandshake {
		if !directChanIDRe.MatchString(f.Route.ChanID) {
			d.t.Errorf("direct chan id %q, want p + 16 lowercase hex", f.Route.ChanID)
			go p.Close()
			return
		}
		msg1, err := api.HandshakeFromFrame(f)
		if err != nil {
			return
		}
		sess, _, msg2, err := e2e.Respond(d.key, api.ChannelPrologue(d.nodeID, f.Route.ChanID), msg1)
		if err != nil {
			go p.Close()
			return
		}
		st.ch = api.NewChannel(f.Route.ChanID, sess)
		st.chanID = f.Route.ChanID
		hf, _ := api.MarshalHandshakeFrame(f.Route.ChanID, msg2)
		_ = p.SendRawFrame(api.EncodingJSON, hf)
		// A real node streams its session snapshot right after msg2.
		d.mu.Lock()
		snap := append([]session.Session(nil), d.sessions...)
		d.mu.Unlock()
		for _, s := range snap {
			b, _ := json.Marshal(registry.Event{Type: registry.EventAdded, Session: s})
			nf, _ := st.ch.SealNotificationFrame(api.EncodingJSON, api.MethodSessionEvent, api.RouteHeader{}, b)
			_ = p.SendRawFrame(api.EncodingJSON, nf)
		}
		return
	}
	if st.ch == nil {
		return
	}
	params, err := st.ch.OpenParams(f)
	if err != nil {
		return
	}
	d.mu.Lock()
	d.calls[f.Method]++
	d.mu.Unlock()
	var result json.RawMessage
	switch f.Method {
	case api.MethodNodeIdentify:
		result, _ = json.Marshal(api.IdentifyResult{ID: d.nodeID, Capabilities: api.NodeCapabilities{P2P: true}})
	case api.MethodSessionsList:
		d.mu.Lock()
		result, _ = json.Marshal(d.sessions)
		hold := d.listHold
		d.mu.Unlock()
		if hold != nil {
			<-hold
		}
	case "test.block":
		return // never answered
	default:
		result = params
	}
	rf, _ := st.ch.SealResponseFrame(api.EncodingJSON, f.ID, result, nil)
	_ = p.SendRawFrame(api.EncodingJSON, rf)
}

// notify sends a sealed notification on every live direct connection.
func (d *fakeDirect) notify(method string, params any) {
	b, _ := json.Marshal(params)
	d.mu.Lock()
	conns := append([]*directConnState(nil), d.conns...)
	d.mu.Unlock()
	for _, st := range conns {
		st.mu.Lock()
		if st.ch != nil {
			nf, _ := st.ch.SealNotificationFrame(api.EncodingJSON, method, api.RouteHeader{}, b)
			_ = st.peer.SendRawFrame(api.EncodingJSON, nf)
		}
		st.mu.Unlock()
	}
}

func (d *fakeDirect) killAll() {
	d.mu.Lock()
	conns := append([]*directConnState(nil), d.conns...)
	d.mu.Unlock()
	for _, st := range conns {
		st.peer.Close()
	}
}

func (d *fakeDirect) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

func (d *fakeDirect) callCount(method string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[method]
}

func (d *fakeDirect) chanIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, st := range d.conns {
		st.mu.Lock()
		out = append(out, st.chanID)
		st.mu.Unlock()
	}
	return out
}

func (d *fakeDirect) lastConn() *directConnState {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.conns) == 0 {
		return nil
	}
	return d.conns[len(d.conns)-1]
}

// sealRelayNote sends a notification on n's relay channel, as the node would.
func (g *fakeMultiGateway) sealRelayNote(n *fakeNode, method string, params any) {
	b, _ := json.Marshal(params)
	g.sendMu.Lock()
	defer g.sendMu.Unlock()
	nf, _ := n.ch.SealNotificationFrame(api.EncodingJSON, method, api.RouteHeader{}, b)
	_ = g.peer.SendRawFrame(api.EncodingJSON, nf)
}

// replyRelay answers a silent request on n's relay channel.
func (g *fakeMultiGateway) replyRelay(n *fakeNode, id *json.RawMessage, result json.RawMessage) {
	g.sendMu.Lock()
	defer g.sendMu.Unlock()
	rf, _ := n.ch.SealResponseFrame(api.EncodingJSON, id, result, nil)
	_ = g.peer.SendRawFrame(api.EncodingJSON, rf)
}

func (g *fakeMultiGateway) silentIDs(n *fakeNode) []*json.RawMessage {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]*json.RawMessage(nil), n.silentIDs...)
}

func (g *fakeMultiGateway) relayOpenCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.relayOpens
}

// p2pNode is a relay node that serves sessions.list from sessions, advertises
// p2p and points its roster endpoint at a fake direct listener.
func p2pNode(t *testing.T, id string, sessions ...session.Session) (*fakeNode, *fakeDirect) {
	t.Helper()
	n := newLifecycleNode(t, id, sessions[0].ID)
	list, _ := json.Marshal(sessions)
	n.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method == api.MethodSessionsList {
			return list, nil, nil
		}
		return params, nil, nil
	}
	n.p2pCap = true
	d := newFakeDirect(t, n, sessions...)
	n.endpoints = []string{d.url}
	return n, d
}

type p2pSetup struct {
	g *fakeMultiGateway
	n *fakeNode
	d *fakeDirect
	c *E2EClient
}

func connectP2P(t *testing.T, gatewayP2P bool, mutate func(*fakeNode, *fakeDirect)) p2pSetup {
	t.Helper()
	n, d := p2pNode(t, "n1", session.Session{ID: "argus:%1", Agent: "claude"})
	if mutate != nil {
		mutate(n, d)
	}
	g, conn := newFakeMultiGateway(t, n)
	g.p2p = gatewayP2P
	t.Cleanup(func() { g.peer.Close() })
	c, err := NewE2EClient(conn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	awaitSessionEventFor(t, c, "n1")
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "gateway"}) // the relay open
	return p2pSetup{g: g, n: n, d: d, c: c}
}

func awaitPath(t *testing.T, paths <-chan NodePath, want NodePath) {
	t.Helper()
	select {
	case p := <-paths:
		if p != want {
			t.Fatalf("path = %+v, want %+v", p, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no path change to %+v", want)
	}
}

func noPath(t *testing.T, paths <-chan NodePath, wait time.Duration) {
	t.Helper()
	select {
	case p := <-paths:
		t.Fatalf("unexpected path change %+v", p)
	case <-time.After(wait):
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// drainEvents returns every event that arrives within wait.
func drainEvents(c *E2EClient, wait time.Duration) []api.Notification {
	var out []api.Notification
	deadline := time.After(wait)
	for {
		select {
		case n := <-c.Events():
			out = append(out, n)
		case <-deadline:
			return out
		}
	}
}

func sessionEvents(t *testing.T, notes []api.Notification) []registry.Event {
	t.Helper()
	var out []registry.Event
	for _, n := range notes {
		if n.Method != api.MethodSessionEvent {
			continue
		}
		var ev registry.Event
		if err := json.Unmarshal(n.Params, &ev); err != nil {
			t.Fatalf("decode session.event: %v", err)
		}
		out = append(out, ev)
	}
	return out
}

func TestUpgradesToTCP(t *testing.T) {
	s := connectP2P(t, true, nil)
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})

	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	if got := s.c.NodeVias()["n1"]; got != "tcp" {
		t.Fatalf("via = %q, want tcp", got)
	}
	if ids := s.d.chanIDs(); len(ids) != 1 || !directChanIDRe.MatchString(ids[0]) {
		t.Fatalf("direct chan ids = %v", ids)
	}
	if s.d.callCount(api.MethodNodeIdentify) != 1 {
		t.Fatal("node.identify not sent on the direct link")
	}
	before := s.d.callCount(api.MethodSessionsList)
	var list []session.Session
	if err := s.c.Call(api.MethodSessionsList, nil, &list); err != nil {
		t.Fatalf("sessions.list: %v", err)
	}
	if len(list) != 1 || list[0].ID != session.CompositeID("n1", "argus:%1") {
		t.Fatalf("sessions = %+v", list)
	}
	if s.d.callCount(api.MethodSessionsList) != before+1 {
		t.Fatal("sessions.list did not travel over the direct link")
	}
	// The relay is released once the switch settles.
	select {
	case got := <-s.g.relayClosed:
		if got != "c1" {
			t.Fatalf("relay.close for %q, want c1", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("relay not closed after the switch")
	}
}

func TestDirectDropFallsBackToRelay(t *testing.T) {
	s := connectP2P(t, true, nil)
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	drainEvents(s.c, 100*time.Millisecond)

	s.d.refuse.Store(true)
	s.d.killAll()
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
	if got := s.c.NodeVias()["n1"]; got != "gateway" {
		t.Fatalf("via = %q, want gateway", got)
	}
	for _, ev := range sessionEvents(t, drainEvents(s.c, 200*time.Millisecond)) {
		if ev.Session.Offline || ev.Type == registry.EventRemoved {
			t.Fatalf("fallback surfaced %s offline=%v for %s", ev.Type, ev.Session.Offline, ev.Session.ID)
		}
	}
	var list []session.Session
	if err := s.c.Call(api.MethodSessionsList, nil, &list); err != nil || len(list) != 1 {
		t.Fatalf("sessions.list over relay = %+v, %v", list, err)
	}
	if s.g.relayOpenCount() != 2 {
		t.Fatalf("relay.open count = %d, want 2", s.g.relayOpenCount())
	}
}

func TestSwitchNoFlickerNoDuplicates(t *testing.T) {
	relaySessions := []session.Session{{ID: "argus:%1", Agent: "claude"}, {ID: "argus:%3", Agent: "claude"}}
	directSessions := []session.Session{{ID: "argus:%1", Agent: "claude", Name: "renamed"}, {ID: "argus:%2", Agent: "claude"}}
	s := connectP2P(t, true, func(n *fakeNode, d *fakeDirect) {
		n.silent = map[string]bool{"test.hang": true}
		d.sessions = directSessions
	})
	s.g.sealRelayNote(s.n, api.MethodSessionEvent, registry.Event{Type: registry.EventAdded, Session: relaySessions[1]})
	awaitSessionEventFor(t, s.c, "n1")

	// A call left pending on the relay link.
	hung := make(chan error, 1)
	go func() { hung <- s.c.callNode("n1", "test.hang", nil, nil) }()
	waitUntil(t, "pending relay call", func() bool { return len(s.g.silentIDs(s.n)) == 1 })

	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})

	got := map[string]registry.EventType{}
	for _, ev := range sessionEvents(t, drainEvents(s.c, 200*time.Millisecond)) {
		if ev.Session.Offline {
			t.Fatalf("switch surfaced %s as offline", ev.Session.ID)
		}
		if _, dup := got[ev.Session.ID]; dup {
			t.Fatalf("session %s reported twice", ev.Session.ID)
		}
		got[ev.Session.ID] = ev.Type
	}
	want := map[string]registry.EventType{
		session.CompositeID("n1", "argus:%1"): registry.EventUpdated,
		session.CompositeID("n1", "argus:%2"): registry.EventAdded,
		session.CompositeID("n1", "argus:%3"): registry.EventRemoved,
	}
	if len(got) != len(want) {
		t.Fatalf("reconcile events = %v, want %v", got, want)
	}
	for id, typ := range want {
		if got[id] != typ {
			t.Fatalf("reconcile events = %v, want %v", got, want)
		}
	}

	// The old link's notifications stop routing at the swap...
	delta := api.TranscriptDelta{SubID: "s1"}
	s.g.sealRelayNote(s.n, api.MethodTranscriptDelta, delta)
	// ...while the new link's do.
	s.d.notify(api.MethodTranscriptDelta, delta)
	deltas := 0
	for _, n := range drainEvents(s.c, 300*time.Millisecond) {
		if n.Method == api.MethodTranscriptDelta {
			deltas++
		}
	}
	if deltas != 1 {
		t.Fatalf("transcript.delta delivered %d times, want 1", deltas)
	}

	// A response to the old link's pending call still routes.
	s.g.replyRelay(s.n, s.g.silentIDs(s.n)[0], json.RawMessage(`{}`))
	select {
	case err := <-hung:
		if err != nil {
			t.Fatalf("pending relay call: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending relay call never returned")
	}
	select {
	case got := <-s.g.relayClosed:
		if got != "c1" {
			t.Fatalf("relay.close for %q, want c1", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old relay not closed after its call settled")
	}
}

func TestUnreachableEndpointStaysOnRelay(t *testing.T) {
	s := connectP2P(t, true, func(n *fakeNode, d *fakeDirect) {
		// A closed port, and a non-WebSocket URL that must never be dialed.
		n.endpoints = []string{"ws://127.0.0.1:1/p2p", "http" + strings.TrimPrefix(d.url, "ws")}
	})
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	noPath(t, s.c.Paths(), time.Second)
	if got := s.c.NodeVias()["n1"]; got != "gateway" {
		t.Fatalf("via = %q, want gateway", got)
	}
	if s.d.dialCount() != 0 {
		t.Fatalf("non-ws endpoint dialed %d times", s.d.dialCount())
	}
}

// A one-shot client never dials tcp.
func TestNoTCPStaysOnRelay(t *testing.T) {
	s := connectP2P(t, true, nil)
	s.c.SetP2POptions(P2POptions{Enabled: true, NoTCP: true, dialable: alwaysDialable})
	noPath(t, s.c.Paths(), 3*p2pRetryInterval)
	if s.d.dialCount() != 0 {
		t.Fatalf("tcp dialed %d times with NoTCP", s.d.dialCount())
	}
}

func TestNoUpgradeWhenTailnetDown(t *testing.T) {
	s := connectP2P(t, true, nil)
	var asked atomic.Int32
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: func(context.Context, string) bool {
		asked.Add(1)
		return false
	}})
	noPath(t, s.c.Paths(), time.Second)
	if asked.Load() < 2 {
		t.Fatalf("dialable asked %d times, want retries", asked.Load())
	}
	if s.d.dialCount() != 0 {
		t.Fatalf("dialed %d times with tailnet down", s.d.dialCount())
	}
}

func TestNoUpgradeWhenGatewayLacksP2P(t *testing.T) {
	s := connectP2P(t, false, nil)
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	noPath(t, s.c.Paths(), time.Second)
	if s.d.dialCount() != 0 {
		t.Fatalf("dialed %d times without gateway p2p", s.d.dialCount())
	}
}

func TestGatewayLossDropsDirect(t *testing.T) {
	s := connectP2P(t, true, nil)
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	conn := s.d.lastConn()

	s.g.peer.Close()
	select {
	case <-conn.peer.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("direct link survived gateway loss")
	}
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1"})
}

// A roster offline that lands while the fallback relay is opening accounts
// for the node once: no second Offline round, no restarted grace timer.
func TestFallbackRaceWithRosterOfflineLosesNodeOnce(t *testing.T) {
	s := connectP2P(t, true, nil)
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	drainEvents(s.c, 100*time.Millisecond)

	s.g.mu.Lock()
	s.g.relayOpenHook = func(count int) {
		if count == 2 { // the fallback relay
			s.g.emitNodeEvent(api.NodeEventOffline, s.n)
		}
	}
	s.g.mu.Unlock()
	s.d.refuse.Store(true)
	s.d.killAll()

	offline := 0
	for _, ev := range sessionEvents(t, drainEvents(s.c, time.Second)) {
		if ev.Session.Offline {
			offline++
		}
	}
	if offline != 1 {
		t.Fatalf("offline events = %d, want 1", offline)
	}
	if _, ok := s.c.NodeVias()["n1"]; ok {
		t.Fatal("offline node still has a link")
	}
	waitUntil(t, "fallback relay released", func() bool {
		s.c.mu.Lock()
		defer s.c.mu.Unlock()
		return len(s.c.byChanID) == 0
	})
}

// Paths is complete across a reconnect: a node on tcp is reported without a
// link when the gateway drops, then on gateway and tcp again.
func TestReconnectReportsNoLinkThenTCP(t *testing.T) {
	n, _ := p2pNode(t, "n1", session.Session{ID: "argus:%1", Agent: "claude"})
	var mu sync.Mutex
	var last *fakeMultiGateway
	dial := func(context.Context) (net.Conn, error) {
		g, conn := newFakeMultiGateway(t, n)
		g.p2p = true
		t.Cleanup(func() { g.peer.Close() })
		mu.Lock()
		last = g
		mu.Unlock()
		return conn, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := NewReconnectingE2EClient(ctx, dial)
	if err != nil {
		t.Fatalf("NewReconnectingE2EClient: %v", err)
	}
	defer c.Close()
	c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})

	mu.Lock()
	g := last
	mu.Unlock()
	g.peer.Close()
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1"})
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
	for {
		select {
		case p := <-c.Paths():
			if p.Via == "tcp" {
				if got := c.NodeVias()["n1"]; got != "tcp" {
					t.Fatalf("via = %q, want tcp", got)
				}
				return
			}
			if p != (NodePath{NodeID: "n1", Via: "gateway"}) {
				t.Fatalf("unexpected path %+v", p)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("node not upgraded again after reconnect")
		}
	}
}

func TestRosterOfflineClosesDirect(t *testing.T) {
	s := connectP2P(t, true, nil)
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	conn := s.d.lastConn()
	opens := s.g.relayOpenCount()

	s.g.emitNodeEvent(api.NodeEventOffline, s.n)
	select {
	case <-conn.peer.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("direct link survived roster offline")
	}
	time.Sleep(500 * time.Millisecond)
	if _, ok := s.c.NodeVias()["n1"]; ok {
		t.Fatal("offline node still has a link")
	}
	if s.g.relayOpenCount() != opens {
		t.Fatal("offline node fell back to the relay")
	}
}

func TestEndpointOverrideFromConfig(t *testing.T) {
	var d *fakeDirect
	s := connectP2P(t, true, func(n *fakeNode, fd *fakeDirect) {
		n.endpoints = []string{"ws://127.0.0.1:1/p2p"}
		d = fd
	})
	// Viper lowercases map keys; the label is "n1-box", so key it upper-case
	// in the node's label to prove the match ignores case both ways.
	s.c.SetP2POptions(P2POptions{
		Enabled:   true,
		Endpoints: map[string][]string{"N1-BOX": {d.url}},
		dialable:  alwaysDialable,
	})
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	if d.dialCount() == 0 {
		t.Fatal("override endpoint not dialed")
	}
}

func TestInflightCallFailsOnDrop(t *testing.T) {
	s := connectP2P(t, true, nil)
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})

	errc := make(chan error, 1)
	go func() { errc <- s.c.callNode("n1", "test.block", nil, nil) }()
	waitUntil(t, "blocked call on the direct link", func() bool { return s.d.callCount("test.block") == 1 })
	s.d.refuse.Store(true)
	s.d.killAll()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("in-flight call succeeded on a dropped link")
		}
	case <-time.After(callTimeout / 2):
		t.Fatal("in-flight call did not fail promptly on drop")
	}
	s.c.mu.Lock()
	n := len(s.c.pending)
	s.c.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d pending replies leaked", n)
	}
}

func TestKeepaliveDetectsDeadLink(t *testing.T) {
	s := connectP2P(t, true, nil)
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})

	s.d.refuse.Store(true)
	s.d.frozen.Store(true) // half-open: the node stops reading, nothing errors
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
}

// Paths survives the reconnecting wrapper, next to States.
func TestPathsReportsVia(t *testing.T) {
	n, d := p2pNode(t, "n1", session.Session{ID: "argus:%1", Agent: "claude"})
	dial := func(context.Context) (net.Conn, error) {
		g, conn := newFakeMultiGateway(t, n)
		g.p2p = true
		t.Cleanup(func() { g.peer.Close() })
		return conn, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := NewReconnectingE2EClient(ctx, dial)
	if err != nil {
		t.Fatalf("NewReconnectingE2EClient: %v", err)
	}
	defer c.Close()
	c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})

	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	if got := c.NodeVias()["n1"]; got != "tcp" {
		t.Fatalf("via = %q, want tcp", got)
	}
	d.refuse.Store(true)
	d.killAll()
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
	if got := c.NodeVias()["n1"]; got != "gateway" {
		t.Fatalf("via = %q, want gateway", got)
	}
}

// A node lost while on tcp leaves the path state: a later gateway drop does
// not report it back on gateway.
func TestLostNodeNotReportedOnReconnect(t *testing.T) {
	n, _ := p2pNode(t, "n1", session.Session{ID: "argus:%1", Agent: "claude"})
	var mu sync.Mutex
	var gws []*fakeMultiGateway
	dial := func(context.Context) (net.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		var g *fakeMultiGateway
		var conn net.Conn
		if len(gws) == 0 {
			g, conn = newFakeMultiGateway(t, n)
		} else {
			g, conn = newFakeMultiGateway(t) // n1 is gone
		}
		g.p2p = true
		t.Cleanup(func() { g.peer.Close() })
		gws = append(gws, g)
		return conn, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := NewReconnectingE2EClient(ctx, dial)
	if err != nil {
		t.Fatalf("NewReconnectingE2EClient: %v", err)
	}
	defer c.Close()
	c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})

	mu.Lock()
	g := gws[0]
	mu.Unlock()
	g.emitNodeEvent(api.NodeEventOffline, n)
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1"})

	g.peer.Close()
	waitUntil(t, "reconnect", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(gws) == 2
	})
	noPath(t, c.Paths(), time.Second)
}

// An upgrade still dialing when the node is lost must not switch the node's
// next relay channel: only the run started for that channel may.
func TestStaleUpgradeDoesNotSwapNewRelay(t *testing.T) {
	s := connectP2P(t, true, nil)
	hold := make(chan struct{})
	s.d.mu.Lock()
	s.d.hold = hold
	s.d.mu.Unlock()
	s.c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	waitUntil(t, "first dial", func() bool { return s.d.dialCount() == 1 })

	s.g.emitNodeEvent(api.NodeEventOffline, s.n)
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1"})
	s.g.emitNodeEvent(api.NodeEventOnline, s.n)
	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
	waitUntil(t, "second dial", func() bool { return s.d.dialCount() == 2 })
	close(hold)

	awaitPath(t, s.c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	noPath(t, s.c.Paths(), time.Second)
	if got := s.c.NodeVias()["n1"]; got != "tcp" {
		t.Fatalf("via = %q, want tcp", got)
	}
	live := 0
	s.d.mu.Lock()
	conns := append([]*directConnState(nil), s.d.conns...)
	s.d.mu.Unlock()
	for _, st := range conns {
		select {
		case <-st.peer.Done():
		default:
			live++
		}
	}
	if live != 1 {
		t.Fatalf("live direct links = %d, want 1", live)
	}
}

// A node lost while its switch is still listing sessions is not reported on
// tcp after its empty Via, so a later gateway drop does not resurrect it.
func TestLostDuringSwitchNotReportedTCP(t *testing.T) {
	n, d := p2pNode(t, "n1", session.Session{ID: "argus:%1", Agent: "claude"})
	hold := make(chan struct{})
	d.mu.Lock()
	d.listHold = hold
	d.mu.Unlock()
	var mu sync.Mutex
	var gws []*fakeMultiGateway
	dial := func(context.Context) (net.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		var g *fakeMultiGateway
		var conn net.Conn
		if len(gws) == 0 {
			g, conn = newFakeMultiGateway(t, n)
		} else {
			g, conn = newFakeMultiGateway(t) // n1 is gone
		}
		g.p2p = true
		t.Cleanup(func() { g.peer.Close() })
		gws = append(gws, g)
		return conn, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := NewReconnectingE2EClient(ctx, dial)
	if err != nil {
		t.Fatalf("NewReconnectingE2EClient: %v", err)
	}
	defer c.Close()
	c.SetP2POptions(P2POptions{Enabled: true, dialable: alwaysDialable})
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
	waitUntil(t, "switch listing sessions", func() bool { return d.callCount(api.MethodSessionsList) == 1 })

	mu.Lock()
	g := gws[0]
	mu.Unlock()
	g.emitNodeEvent(api.NodeEventOffline, n)
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1"})
	close(hold)
	noPath(t, c.Paths(), 500*time.Millisecond)

	g.peer.Close()
	waitUntil(t, "reconnect", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(gws) == 2
	})
	noPath(t, c.Paths(), time.Second)
}

// recordConn keeps every byte read from the client.
type recordConn struct {
	net.Conn
	mu  *sync.Mutex
	buf *[]byte
}

func (c *recordConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.mu.Lock()
	*c.buf = append(*c.buf, b[:n]...)
	c.mu.Unlock()
	return n, err
}

// plainDirect is a plaintext node's /p2p listener: token.challenge,
// token.present, then a plain channel serving node.identify, sessions.list and
// echo. mode selects a misbehaving node.
type plainDirect struct {
	t      *testing.T
	nodeID string
	hash   string // the token hash the node holds
	mode   string // "", "wrongmac" or "close"
	refuse atomic.Bool
	srv    *httptest.Server
	url    string

	mu        sync.Mutex
	recv      []byte
	presented []string
	chanID    string
	nonce     string
	ch        *api.Channel
	peer      atomic.Pointer[api.Peer]
	calls     map[string]int
	sessions  []session.Session
}

func newPlainDirect(t *testing.T, n *fakeNode, token string, sessions ...session.Session) *plainDirect {
	t.Helper()
	d := &plainDirect{t: t, nodeID: n.id, hash: clienttoken.HashToken(token), calls: map[string]int{}, sessions: sessions}
	d.srv = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.srv.Close)
	d.url = "ws" + strings.TrimPrefix(d.srv.URL, "http") + "/p2p"
	return d
}

func (d *plainDirect) serve(w http.ResponseWriter, r *http.Request) {
	if d.refuse.Load() {
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	conn, err := api.AcceptWS(w, r)
	if err != nil {
		return
	}
	rc := &recordConn{Conn: conn, mu: &d.mu, buf: &d.recv}
	p := api.NewPeer(rc, api.PeerOptions{
		OnRelayFrame: d.onFrame,
		Dispatch: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			switch method {
			case api.MethodTokenChallenge:
				var p api.TokenChallengeParams
				_ = json.Unmarshal(params, &p)
				if d.mode == "close" {
					go d.closePeer()
					return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "not admitted"}
				}
				if want, _ := api.P2PTag(d.hash, p.Nonce); want != p.Tag {
					go d.closePeer()
					return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "not admitted"}
				}
				mac, _ := api.P2PMAC(d.hash, d.nodeID, p.ChanID, p.Nonce)
				if d.mode == "wrongmac" {
					mac, _ = api.P2PMAC(d.hash, d.nodeID, p.ChanID, p.Nonce+"x")
				}
				d.mu.Lock()
				d.chanID, d.nonce = p.ChanID, p.Nonce
				d.mu.Unlock()
				return api.TokenChallengeResult{MAC: mac}, nil
			case api.MethodTokenPresent:
				var p api.TokenPresentParams
				_ = json.Unmarshal(params, &p)
				d.mu.Lock()
				d.presented = append(d.presented, p.Token)
				d.mu.Unlock()
				return nil, nil
			}
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found"}
		},
	})
	d.peer.Store(p)
}

func (d *plainDirect) closePeer() {
	for d.peer.Load() == nil {
		time.Sleep(time.Millisecond)
	}
	d.peer.Load().Close()
}

func (d *plainDirect) onFrame(p *api.Peer, f api.RelayFrame) {
	d.mu.Lock()
	if d.ch == nil {
		if f.Route.ChanID != d.chanID || len(d.presented) == 0 {
			d.mu.Unlock()
			go p.Close()
			return
		}
		d.ch = api.NewPlainChannel(f.Route.ChanID)
	}
	ch := d.ch
	d.calls[f.Method]++
	sessions := d.sessions
	d.mu.Unlock()
	params, err := ch.OpenParams(f)
	if err != nil {
		return
	}
	var result json.RawMessage
	switch f.Method {
	case api.MethodNodeIdentify:
		result, _ = json.Marshal(api.IdentifyResult{ID: d.nodeID, Capabilities: api.NodeCapabilities{P2P: true}})
	case api.MethodSessionsList:
		result, _ = json.Marshal(sessions)
	case "test.block":
		return // never answered
	default:
		result = params
	}
	rf, _ := ch.SealResponseFrame(api.EncodingJSON, f.ID, result, nil)
	_ = p.SendRawFrame(api.EncodingJSON, rf)
}

func (d *plainDirect) received() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return string(d.recv)
}

func (d *plainDirect) callCount(method string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[method]
}

const plainTestToken = "tok-secret-0123456789abcdef"

// connectPlainP2P connects a plaintext client to a relay node whose roster
// entry points at a plaintext direct listener.
func connectPlainP2P(t *testing.T, mode string) (*E2EClient, *plainDirect, *fakeMultiGateway) {
	t.Helper()
	n, _ := p2pNode(t, "n1", session.Session{ID: "argus:%1", Agent: "claude"})
	n.plaintext = true
	d := newPlainDirect(t, n, plainTestToken, session.Session{ID: "argus:%1", Agent: "claude"})
	d.mode = mode
	n.endpoints = []string{d.url}
	g, conn := newFakeMultiGateway(t, n)
	g.p2p = true
	t.Cleanup(func() { g.peer.Close() })
	c, err := NewE2EClientPlain(conn)
	if err != nil {
		t.Fatalf("NewE2EClientPlain: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
	return c, d, g
}

func TestPlaintextUpgradesToTCP(t *testing.T) {
	c, d, g := connectPlainP2P(t, "")
	c.SetP2POptions(P2POptions{Enabled: true, Token: plainTestToken, dialable: alwaysDialable})

	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	if got := c.NodeVias()["n1"]; got != "tcp" {
		t.Fatalf("via = %q, want tcp", got)
	}
	d.mu.Lock()
	presented := append([]string(nil), d.presented...)
	chanID := d.chanID
	d.mu.Unlock()
	if len(presented) != 1 || presented[0] != plainTestToken {
		t.Fatalf("presented tokens = %v", presented)
	}
	if !directChanIDRe.MatchString(chanID) {
		t.Fatalf("chan id %q, want p + 16 lowercase hex", chanID)
	}
	if d.callCount(api.MethodNodeIdentify) != 1 {
		t.Fatal("node.identify not sent on the direct link")
	}
	before := d.callCount(api.MethodSessionsList)
	var list []session.Session
	if err := c.Call(api.MethodSessionsList, nil, &list); err != nil {
		t.Fatalf("sessions.list: %v", err)
	}
	if len(list) != 1 || d.callCount(api.MethodSessionsList) != before+1 {
		t.Fatalf("sessions.list did not travel over the direct link: %+v", list)
	}
	select {
	case <-g.relayClosed:
	case <-time.After(10 * time.Second):
		t.Fatal("relay not closed after the switch")
	}
}

func TestPlaintextNeverSendsHash(t *testing.T) {
	c, d, _ := connectPlainP2P(t, "")
	c.SetP2POptions(P2POptions{Enabled: true, Token: plainTestToken, dialable: alwaysDialable})
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	if got := d.received(); !strings.Contains(got, plainTestToken) {
		t.Fatal("recorder did not see the token-bearing frame")
	}
	if got := d.received(); strings.Contains(got, d.hash) {
		t.Fatalf("token hash reached the wire: %s", got)
	}
}

func TestPlaintextNoTokenWithoutProof(t *testing.T) {
	for _, mode := range []string{"wrongmac", "close"} {
		t.Run(mode, func(t *testing.T) {
			c, d, _ := connectPlainP2P(t, mode)
			c.SetP2POptions(P2POptions{Enabled: true, Token: plainTestToken, dialable: alwaysDialable})
			noPath(t, c.Paths(), time.Second)
			if got := c.NodeVias()["n1"]; got != "gateway" {
				t.Fatalf("via = %q, want gateway", got)
			}
			if got := d.received(); got == "" || strings.Contains(got, plainTestToken) || strings.Contains(got, d.hash) {
				t.Fatalf("token or hash on the wire (or nothing received): %q", got)
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			if len(d.presented) != 0 {
				t.Fatalf("token presented: %v", d.presented)
			}
		})
	}
}

func TestPlaintextNoUpgradeWhenTailnetDown(t *testing.T) {
	c, d, _ := connectPlainP2P(t, "")
	var asked atomic.Int32
	c.SetP2POptions(P2POptions{Enabled: true, Token: plainTestToken, dialable: func(context.Context, string) bool {
		asked.Add(1)
		return false
	}})
	noPath(t, c.Paths(), time.Second)
	if asked.Load() < 2 {
		t.Fatalf("dialable asked %d times, want retries", asked.Load())
	}
	if got := d.received(); got != "" {
		t.Fatalf("dialed with tailnet down: %q", got)
	}
}

func TestPlaintextDropFallsBackToRelay(t *testing.T) {
	c, d, _ := connectPlainP2P(t, "")
	c.SetP2POptions(P2POptions{Enabled: true, Token: plainTestToken, dialable: alwaysDialable})
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "tcp"})
	drainEvents(c, 100*time.Millisecond)

	errc := make(chan error, 1)
	go func() { errc <- c.callNode("n1", "test.block", nil, nil) }()
	waitUntil(t, "blocked call on the direct link", func() bool { return d.callCount("test.block") == 1 })

	d.refuse.Store(true)
	d.peer.Load().Close()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("in-flight call succeeded on a dropped link")
		}
	case <-time.After(callTimeout / 2):
		t.Fatal("in-flight call did not fail promptly on drop")
	}
	awaitPath(t, c.Paths(), NodePath{NodeID: "n1", Via: "gateway"})
	noPath(t, c.Paths(), time.Second)
	if got := c.NodeVias()["n1"]; got != "gateway" {
		t.Fatalf("via = %q, want gateway", got)
	}
	for _, ev := range sessionEvents(t, drainEvents(c, 200*time.Millisecond)) {
		if ev.Session.Offline || ev.Type == registry.EventRemoved {
			t.Fatalf("fallback surfaced %s offline=%v for %s", ev.Type, ev.Session.Offline, ev.Session.ID)
		}
	}
	var list []session.Session
	if err := c.Call(api.MethodSessionsList, nil, &list); err != nil || len(list) != 1 {
		t.Fatalf("sessions.list over relay = %+v, %v", list, err)
	}
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d pending replies leaked", n)
	}
}
