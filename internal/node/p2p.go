package node

import (
	"bytes"
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/tailnet"
)

// Direct listener limits. Pre-auth covers a connection from its first request
// until its channel is admitted; a caller is one tailnet node, so callers that
// tailscaled forwards from loopback still get separate caps.
const (
	directPreAuthTotal     = 16
	directPreAuthPerCaller = 4
	directHandshakeTimeout = 10 * time.Second
	directPreAuthReadLimit = 64 << 10
	maxDirectChannels      = 64
)

type directLimits struct {
	preAuthTotal      int
	preAuthPerCaller  int
	handshakeTimeout  time.Duration
	maxAdmitted       int
	keepalive         time.Duration
	keepaliveTimeout  time.Duration
	keepaliveFailures int
}

func defaultDirectLimits() directLimits {
	return directLimits{
		preAuthTotal:      directPreAuthTotal,
		preAuthPerCaller:  directPreAuthPerCaller,
		handshakeTimeout:  directHandshakeTimeout,
		maxAdmitted:       maxDirectChannels,
		keepalive:         api.DefaultKeepaliveInterval,
		keepaliveTimeout:  api.DefaultKeepaliveTimeout,
		keepaliveFailures: api.DefaultKeepaliveFailures,
	}
}

// directSet holds every live direct connection, so node-wide re-checks reach
// them, and the slot counts the limits apply to.
type directSet struct {
	limits directLimits // set before serving; read-only after

	mu        sync.Mutex
	conns     map[*directConn]struct{}
	preAuth   int
	preAuthBy map[string]int // pre-auth connections per caller
	admitted  int
	warned    bool // tailscaled unreachable was logged
}

func newDirectSet() *directSet {
	return &directSet{
		limits:    defaultDirectLimits(),
		conns:     map[*directConn]struct{}{},
		preAuthBy: map[string]int{},
	}
}

type directState int

const (
	directPreAuth directState = iota
	directAdmitted
	directGone
)

// directConn is one connection on the direct listener. It carries at most one
// channel, served by its own responder.
type directConn struct {
	caller string // tailnet node holding a per-caller slot; "" before WhoIs
	conn   net.Conn
	peer   *api.Peer
	resp   *relayResponder
	state  directState // guarded by directSet.mu
}

// close ends the connection without waiting: a WebSocket close waits for the
// remote's close frame, and callers include the uplink read loop.
func (dc *directConn) close() {
	if dc.peer != nil {
		go dc.peer.Close()
	}
}

// acquirePreAuth takes a slot from the total pre-auth cap. It is taken
// before WhoIs, so the cap also bounds concurrent WhoIs calls.
func (s *directSet) acquirePreAuth() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.preAuth >= s.limits.preAuthTotal {
		return false
	}
	s.preAuth++
	return true
}

// acquireCaller adds dc, which holds a total slot, to caller's pre-auth cap.
func (s *directSet) acquireCaller(dc *directConn, caller string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.preAuthBy[caller] >= s.limits.preAuthPerCaller {
		return false
	}
	s.preAuthBy[caller]++
	dc.caller = caller
	return true
}

func (s *directSet) releasePreAuthLocked(dc *directConn) {
	s.preAuth--
	if dc.caller == "" {
		return
	}
	if s.preAuthBy[dc.caller]--; s.preAuthBy[dc.caller] <= 0 {
		delete(s.preAuthBy, dc.caller)
	}
}

func (s *directSet) add(dc *directConn) {
	s.mu.Lock()
	s.conns[dc] = struct{}{}
	s.mu.Unlock()
}

// admit moves dc from pre-auth to an admitted channel slot. It fails when dc
// already holds a channel, has ended, or every slot is taken.
func (s *directSet) admit(dc *directConn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dc.state != directPreAuth || s.admitted >= s.limits.maxAdmitted {
		return false
	}
	s.releasePreAuthLocked(dc)
	s.admitted++
	dc.state = directAdmitted
	return true
}

func (s *directSet) isAdmitted(dc *directConn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return dc.state == directAdmitted
}

// remove frees dc's slot. Safe to call once dc has ended, before or after add.
func (s *directSet) remove(dc *directConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, dc)
	switch dc.state {
	case directPreAuth:
		s.releasePreAuthLocked(dc)
	case directAdmitted:
		s.admitted--
	}
	dc.state = directGone
}

func (s *directSet) snapshot() []*directConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*directConn, 0, len(s.conns))
	for dc := range s.conns {
		out = append(out, dc)
	}
	return out
}

// SetTailnet sets the tailscaled used to identify direct callers. Call before
// ListenP2P; without one every direct caller is rejected.
func (d *Node) SetTailnet(t tailnet.Daemon) { d.tailnetD = t }

// p2pListenRetryBase and p2pListenRetryMax bound the backoff between binds of
// a direct listen address that failed, e.g. a tailnet address tailscaled has
// not assigned yet. Vars so tests can shrink them.
var (
	p2pListenRetryBase = time.Second
	p2pListenRetryMax  = 30 * time.Second
)

// ListenP2P serves the direct listener on addrs until ctx ends. Every address
// that binds is served when it returns; one that fails is logged and bound
// again in the background with capped backoff. Only E2EE nodes may listen.
func (d *Node) ListenP2P(ctx context.Context, addrs []string) {
	if !d.e2ee {
		d.log.Warn("p2p listener requires e2ee")
		return
	}
	if d.tailnetD == nil {
		d.tailnetD = tailnet.Local()
	}
	srv := &http.Server{Handler: d.p2pHandler(), ReadHeaderTimeout: directHandshakeTimeout}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
		d.closeAllDirect()
	}()
	for _, a := range addrs {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			d.log.Warn("p2p listen failed; retrying", "addr", a, "err", err)
			go d.retryListenP2P(ctx, srv, a)
			continue
		}
		d.log.Info("p2p listener started", "addr", a)
		go func() { _ = srv.Serve(ln) }()
	}
}

// retryListenP2P binds addr with capped backoff until it succeeds or ctx ends,
// then serves it on srv.
func (d *Node) retryListenP2P(ctx context.Context, srv *http.Server, addr string) {
	backoff := p2pListenRetryBase
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			if ctx.Err() != nil {
				ln.Close()
				return
			}
			d.log.Info("p2p listener started", "addr", addr)
			_ = srv.Serve(ln)
			return
		}
		d.log.Debug("p2p listen failed", "addr", addr, "err", err)
		if backoff *= 2; backoff > p2pListenRetryMax {
			backoff = p2pListenRetryMax
		}
	}
}

func (d *Node) p2pHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/p2p", d.serveP2P)
	return mux
}

// serveP2P admits a tailnet caller to the direct listener and serves its
// connection until it closes.
func (d *Node) serveP2P(w http.ResponseWriter, r *http.Request) {
	if d.bindings() == nil {
		http.Error(w, "no uplink", http.StatusServiceUnavailable)
		return
	}
	if !d.direct.acquirePreAuth() {
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	dc := &directConn{}
	caller := d.tailnetCaller(r.Context(), r.RemoteAddr)
	if caller == "" {
		d.direct.remove(dc)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !d.direct.acquireCaller(dc, caller) {
		d.direct.remove(dc)
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	conn, err := api.AcceptWSLimit(w, r, directPreAuthReadLimit)
	if err != nil {
		d.direct.remove(dc)
		return
	}
	d.serveDirect(dc, conn)
}

// tailnetCaller returns the stable id of the tailnet node tailscaled names for
// remoteAddr, or "" to reject. A daemon error rejects; it is logged once per
// outage.
func (d *Node) tailnetCaller(ctx context.Context, remoteAddr string) string {
	if d.tailnetD == nil {
		return ""
	}
	caller, err := tailnet.PeerIdentity(ctx, d.tailnetD, remoteAddr)
	d.direct.mu.Lock()
	warn := err != nil && !d.direct.warned
	d.direct.warned = err != nil
	d.direct.mu.Unlock()
	if warn {
		d.log.Warn("tailscaled unreachable; rejecting direct connections", "err", err)
	}
	return caller
}

func (d *Node) serveDirect(dc *directConn, conn net.Conn) {
	l := d.direct.limits
	dc.conn = conn
	dc.resp = d.newDirectResponder(dc)
	// Hold frames until the connection is registered, so every re-check that
	// walks the set sees it before its handshake runs.
	ready := make(chan struct{})
	dc.peer = api.NewPeer(conn, api.PeerOptions{
		OnRelayFrame: func(p *api.Peer, f api.RelayFrame) {
			<-ready
			dc.resp.onFrame(p, f)
		},
		KeepaliveInterval:         l.keepalive,
		KeepaliveTimeout:          l.keepaliveTimeout,
		KeepaliveFailureThreshold: l.keepaliveFailures,
	})
	dc.resp.peer.Store(dc.peer)
	d.direct.add(dc)
	close(ready)
	timer := time.AfterFunc(l.handshakeTimeout, func() {
		if !d.direct.isAdmitted(dc) {
			dc.close()
		}
	})
	<-dc.peer.Done()
	timer.Stop()
	d.direct.remove(dc)
	dc.resp.closeAll()
	// A read error (an exhausted pre-auth budget) ends the read loop without
	// closing the connection.
	dc.close()
}

func (d *Node) newDirectResponder(dc *directConn) *relayResponder {
	return &relayResponder{
		d:            d,
		static:       d.identity,
		dispatch:     d.remoteDispatch(),
		peerDispatch: d.peerDispatch(),
		chans:        map[string]*chanState{},
		peerOpened:   map[string]peerOpened{},
		direct:       dc,
	}
}

// directAdmission decides whether a direct channel may serve key: as the peer
// node whose roster key it is (opener), or as a client whose key is bound to a
// valid token (opener ""). Both need a live uplink, and locked mode needs the
// trust log to authorize key. A quarantined node admits nothing.
func (d *Node) directAdmission(key []byte) (opener string, ok bool) {
	if d.rejectsChannels() {
		return "", false
	}
	if st := d.trust.Load(); st != nil && !st.Disabled() && !d.localDisabled() && !st.DeviceAuthorized(key) {
		return "", false
	}
	b := d.bindings()
	if b == nil {
		return "", false
	}
	if id := d.rosterNodeWithKey(key); id != "" {
		return id, true
	}
	return "", b.admits(key)
}

// rosterNodeWithKey returns the other roster node whose identity key is key.
func (d *Node) rosterNodeWithKey(key []byte) string {
	if len(key) == 0 {
		return ""
	}
	for _, n := range d.roster.others() {
		if n.IdentityPubKey == "" {
			continue
		}
		if want, err := base64.StdEncoding.DecodeString(n.IdentityPubKey); err == nil && bytes.Equal(want, key) {
			return n.ID
		}
	}
	return ""
}

// validDirectChanID reports whether id is p followed by 16 lowercase hex digits.
func validDirectChanID(id string) bool {
	if len(id) != 17 || id[0] != 'p' {
		return false
	}
	for _, c := range id[1:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// watchPeerRoster closes cs, a direct peer channel, once its opener leaves
// the roster or its roster key stops matching.
func (r *relayResponder) watchPeerRoster(cs *chanState) {
	events, cancel := r.d.roster.subscribe()
	defer cancel()
	for {
		if !r.peerKeyMatches(cs.opener, cs.clientStatic) {
			r.d.log.Warn("closing direct peer channel: opener left the roster or its key changed", "chan", cs.ch.ID(), "node", cs.opener)
			r.closeChan(cs.ch.ID())
			return
		}
		select {
		case <-cs.ctx.Done():
			return
		case <-events:
		}
	}
}

// recheckDirect re-runs admission for every channel on a direct responder and
// closes the ones that no longer pass or whose role changed.
func (r *relayResponder) recheckDirect() {
	r.mu.Lock()
	var live []*chanState
	for _, cs := range r.chans {
		live = append(live, cs)
	}
	r.mu.Unlock()
	for _, cs := range live {
		if opener, ok := r.d.directAdmission(cs.clientStatic); !ok || opener != cs.opener {
			r.d.log.Warn("closing direct channel: no longer admitted", "chan", cs.ch.ID())
			r.closeChan(cs.ch.ID())
		}
	}
}

func (d *Node) directResponders() []*relayResponder {
	conns := d.direct.snapshot()
	out := make([]*relayResponder, 0, len(conns))
	for _, dc := range conns {
		out = append(out, dc.resp)
	}
	return out
}

// closeDirectFor ends direct channels authenticated by any of keys.
func (d *Node) closeDirectFor(keys [][]byte) {
	if len(keys) == 0 {
		return
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[string(k)] = true
	}
	for _, r := range d.directResponders() {
		r.mu.Lock()
		var ids []string
		for id, cs := range r.chans {
			if set[string(cs.clientStatic)] {
				ids = append(ids, id)
			}
		}
		r.mu.Unlock()
		for _, id := range ids {
			r.d.log.Info("closing direct channel: token revoked", "chan", id)
			r.closeChan(id)
		}
	}
}

// closeAllDirect ends every direct connection. Channels close at once; the
// connections finish closing in the background.
func (d *Node) closeAllDirect() {
	for _, dc := range d.direct.snapshot() {
		dc.resp.closeAll()
		dc.close()
	}
}
