package node

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/tailnet"
)

// Direct listener limits. Pre-auth covers a connection from its first request
// until its channel is admitted; a caller is one tailnet node, so callers that
// tailscaled forwards from loopback still get separate caps.
const (
	directPreAuthTotal     = 16
	directPreAuthPerCaller = 4
	directHandshakeTimeout = 10 * time.Second
	directIdentifyTimeout  = 3 * time.Second
	directPreAuthReadLimit = 64 << 10
	maxDirectChannels      = 64
)

type directLimits struct {
	preAuthTotal      int
	preAuthPerCaller  int
	handshakeTimeout  time.Duration
	identifyTimeout   time.Duration // node.identify on a master-token opener; under handshakeTimeout
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
		identifyTimeout:   directIdentifyTimeout,
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

	// plaintext connections admit by token (token.challenge, token.present)
	// instead of a Noise handshake.
	plaintext bool
	plain     plainAuth
}

type plainStage int

const (
	plainNone plainStage = iota
	plainChallenged
	plainPresenting
	plainAdmitted
	plainFailed
)

// plainAuth is a plaintext direct connection's token admission. hash is the
// token hash token.challenge matched; the token itself is never kept.
type plainAuth struct {
	mu     sync.Mutex
	stage  plainStage
	chanID string
	hash   string
	opener string // peer node, set when the master token was presented
	opened bool   // the channel was created
}

func (a *plainAuth) tokenHash() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stage == plainFailed {
		return ""
	}
	return a.hash
}

// admitted returns the admitted token hash and opener, ok false before
// admission.
func (a *plainAuth) admitted() (hash, opener string, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hash, a.opener, a.stage == plainAdmitted
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

// SetTailnet sets the tailscaled used to identify direct callers and to check
// peer nodes before dialing them. Call before ConnectGateway; without one
// every direct caller is rejected and peer nodes are reached on the relay.
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
// again in the background with capped backoff. E2EE nodes admit by Noise
// handshake, plaintext nodes by token.
func (d *Node) ListenP2P(ctx context.Context, addrs []string) {
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
	dc.plaintext = !d.e2ee
	dc.resp = d.newDirectResponder(dc)
	// Hold frames until the connection is registered, so every re-check that
	// walks the set sees it before its handshake runs.
	ready := make(chan struct{})
	opts := api.PeerOptions{
		OnRelayFrame: func(p *api.Peer, f api.RelayFrame) {
			<-ready
			dc.resp.onFrame(p, f)
		},
		KeepaliveInterval:         l.keepalive,
		KeepaliveTimeout:          l.keepaliveTimeout,
		KeepaliveFailureThreshold: l.keepaliveFailures,
	}
	if dc.plaintext {
		dispatch := d.plainDirectDispatch(dc)
		opts.Dispatch = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			<-ready
			return dispatch(ctx, method, params)
		}
	}
	dc.peer = api.NewPeer(conn, opts)
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
// valid token (opener "", hash that token). Both need a live uplink, and locked
// mode needs the trust log to authorize key. A quarantined node admits nothing.
func (d *Node) directAdmission(key []byte) (opener, hash string, ok bool) {
	b := d.directGate(key)
	if b == nil {
		return "", "", false
	}
	if id := d.rosterNodeWithKey(key); id != "" {
		return id, "", true
	}
	hash, ok = b.validHash(key)
	return "", hash, ok
}

// directGate returns the uplink's binding store when nothing refuses key
// outright: quarantine, no uplink, or a locked trust log without key.
func (d *Node) directGate(key []byte) *bindingStore {
	if d.rejectsChannels() {
		return nil
	}
	if st := d.trust.Load(); st != nil && !st.Disabled() && !d.localDisabled() && !st.DeviceAuthorized(key) {
		return nil
	}
	return d.bindings()
}

// directStillAdmitted re-runs admission for cs, a live E2EE direct channel. A
// client channel is judged by the token it was admitted with, not its current
// binding: the key may since have been bound to another token.
func (d *Node) directStillAdmitted(cs *chanState) bool {
	if cs.opener != "" {
		opener, _, ok := d.directAdmission(cs.clientStatic)
		return ok && opener == cs.opener
	}
	b := d.directGate(cs.clientStatic)
	return b != nil && d.rosterNodeWithKey(cs.clientStatic) == "" && b.tokenValid(cs.tokenHash)
}

var errDirectRefused = &api.RPCError{Code: api.CodeInvalidRequest, Message: "not admitted"}

// plainDirectDispatch serves the plaintext admission requests. Any other
// request before admission ends the connection; after it, the connection
// still serves nothing outside its channel. Params are never logged: they
// carry the token.
func (d *Node) plainDirectDispatch(dc *directConn) api.DispatchFunc {
	return func(_ context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case api.MethodTokenChallenge:
			return d.tokenChallenge(dc, params)
		case api.MethodTokenPresent:
			return d.tokenPresent(dc, params)
		}
		if _, _, ok := dc.plain.admitted(); ok {
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
		}
		return d.refuseDirect(dc)
	}
}

// refuseDirect fails dc's admission and closes it.
func (d *Node) refuseDirect(dc *directConn) (any, error) {
	dc.plain.mu.Lock()
	dc.plain.stage = plainFailed
	dc.plain.mu.Unlock()
	dc.close()
	return nil, errDirectRefused
}

// tokenChallenge finds the valid token hash whose tag the client sent and
// answers with the node's MAC over it. The hash never crosses the wire.
func (d *Node) tokenChallenge(dc *directConn, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TokenChallengeParams](params)
	if err != nil || !validDirectChanID(p.ChanID) || !validNonce(p.Nonce) {
		return d.refuseDirect(dc)
	}
	b := d.bindings()
	if b == nil {
		return d.refuseDirect(dc)
	}
	var matched string
	for _, h := range b.hashes() {
		if tag, err := api.P2PTag(h, p.Nonce); err == nil && hmac.Equal([]byte(tag), []byte(p.Tag)) {
			matched = h
		}
	}
	if matched == "" {
		d.log.Warn("rejected direct connection: unknown token")
		return d.refuseDirect(dc)
	}
	mac, err := api.P2PMAC(matched, d.id, p.ChanID, p.Nonce)
	if err != nil {
		return d.refuseDirect(dc)
	}
	dc.plain.mu.Lock()
	if dc.plain.stage != plainNone {
		dc.plain.mu.Unlock()
		return d.refuseDirect(dc)
	}
	dc.plain.stage, dc.plain.chanID, dc.plain.hash = plainChallenged, p.ChanID, matched
	dc.plain.mu.Unlock()
	return api.TokenChallengeResult{MAC: mac}, nil
}

// tokenPresent admits dc when the token hashes to the challenged hash. The
// master token admits only another roster node, named by node.identify.
func (d *Node) tokenPresent(dc *directConn, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TokenPresentParams](params)
	dc.plain.mu.Lock()
	ok := err == nil && dc.plain.stage == plainChallenged && p.ChanID == dc.plain.chanID &&
		hmac.Equal([]byte(clienttoken.HashToken(p.Token)), []byte(dc.plain.hash))
	if ok {
		dc.plain.stage = plainPresenting
	}
	hash, chanID := dc.plain.hash, dc.plain.chanID
	dc.plain.mu.Unlock()
	if !ok {
		d.log.Warn("rejected direct connection: token does not match its challenge")
		return d.refuseDirect(dc)
	}
	var opener string
	if hash == d.masterTokenHash() {
		var self bool
		if opener, self = d.identifyOpener(dc); self {
			d.log.Warn("rejected direct channel: opener claims this node's id", "chan", chanID)
			return d.refuseDirect(dc)
		}
	}
	if !d.plainAdmissible(hash, opener) {
		d.log.Warn("rejected direct channel: token not admitted", "chan", chanID)
		return d.refuseDirect(dc)
	}
	if !d.direct.admit(dc) {
		d.log.Warn("rejected direct channel: no free channel slot", "chan", chanID)
		return d.refuseDirect(dc)
	}
	dc.plain.mu.Lock()
	dc.plain.stage, dc.plain.opener = plainAdmitted, opener
	dc.plain.mu.Unlock()
	// Re-check now that re-checks see dc admitted: one that ran during the
	// exchange found nothing to close.
	if !d.plainAdmissible(hash, opener) {
		return d.refuseDirect(dc)
	}
	api.ResetReadLimit(dc.conn)
	return nil, nil
}

// identifyOpener asks a master-token opener for its node id, as the gateway
// does on /node. It returns the id when it names another roster node, and ""
// otherwise: an admin client holds the master token too, and is served as a
// client. self reports an opener claiming this node's own id.
func (d *Node) identifyOpener(dc *directConn) (opener string, self bool) {
	ctx, cancel := context.WithTimeout(context.Background(), d.direct.limits.identifyTimeout)
	defer cancel()
	var res api.IdentifyResult
	if err := dc.peer.CallContext(ctx, api.MethodNodeIdentify, nil, &res); err != nil {
		return "", false
	}
	if res.ID == d.id {
		return "", true
	}
	if !d.rosterHasPeer(res.ID) {
		return "", false
	}
	return res.ID, false
}

// plainAdmissible decides whether a plaintext direct channel admitted with
// token hash may still be served: a client by a paired or the master token, or
// opener by the master token while it is another roster node. Both need a live uplink; a
// quarantined node admits nothing.
func (d *Node) plainAdmissible(hash, opener string) bool {
	if d.rejectsChannels() {
		return false
	}
	b := d.bindings()
	if b == nil || !b.tokenValid(hash) {
		return false
	}
	if opener != "" {
		return hash == d.masterTokenHash() && d.rosterHasPeer(opener)
	}
	return true
}

// rosterHasPeer reports whether id is a roster node other than this one.
func (d *Node) rosterHasPeer(id string) bool {
	if id == "" || id == d.id {
		return false
	}
	_, ok := d.roster.get(id)
	return ok
}

// validNonce reports whether n is 32 bytes, standard padded base64.
func validNonce(n string) bool {
	b, err := base64.StdEncoding.DecodeString(n)
	return err == nil && len(b) == 32
}

// openDirectPlain creates the channel of an admitted plaintext direct
// connection on its first frame, like a plaintext relay channel. A frame
// before admission or for another id ends the connection.
func (r *relayResponder) openDirectPlain(chanID string) *chanState {
	dc := r.direct
	dc.plain.mu.Lock()
	ok := dc.plain.stage == plainAdmitted && !dc.plain.opened && chanID == dc.plain.chanID
	if ok {
		dc.plain.opened = true
	}
	hash, opener := dc.plain.hash, dc.plain.opener
	dc.plain.mu.Unlock()
	if !ok {
		dc.close()
		return nil
	}
	cs, cn := r.insertPlain(chanID, opener)
	if !r.d.plainAdmissible(hash, opener) {
		r.closeChan(chanID)
		return nil
	}
	if opener != "" {
		go r.watchPeerRoster(cs)
	}
	r.startStream(chanID, cs, cn)
	return cs
}

// directPeerValid reports whether cs, a direct peer channel, may still serve
// its opener: by roster key with E2EE, by roster membership in plaintext.
func (r *relayResponder) directPeerValid(cs *chanState) bool {
	if r.direct.plaintext {
		return r.d.rosterHasPeer(cs.opener)
	}
	return r.peerKeyMatches(cs.opener, cs.clientStatic)
}

// closeRevokedDirect ends direct client connections whose admitting token is
// no longer valid in b.
func (d *Node) closeRevokedDirect(b *bindingStore) {
	for _, dc := range d.direct.snapshot() {
		if dc.plaintext {
			if h := dc.plain.tokenHash(); h != "" && !b.tokenValid(h) {
				d.log.Info("closing direct connection: token revoked")
				dc.resp.closeAll()
				dc.close()
			}
			continue
		}
		r := dc.resp
		r.mu.Lock()
		var ids []string
		for id, cs := range r.chans {
			if cs.opener == "" && !b.tokenValid(cs.tokenHash) {
				ids = append(ids, id)
			}
		}
		r.mu.Unlock()
		for _, id := range ids {
			d.log.Info("closing direct channel: token revoked", "chan", id)
			r.closeChan(id)
		}
	}
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
		if !r.directPeerValid(cs) {
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
	if r.direct.plaintext {
		if hash, opener, ok := r.direct.plain.admitted(); ok && !r.d.plainAdmissible(hash, opener) {
			r.d.log.Warn("closing direct connection: no longer admitted")
			r.closeAll()
			r.direct.close()
		}
		return
	}
	r.mu.Lock()
	var live []*chanState
	for _, cs := range r.chans {
		live = append(live, cs)
	}
	r.mu.Unlock()
	for _, cs := range live {
		if !r.d.directStillAdmitted(cs) {
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

// closeAllDirect ends every direct connection. Channels close at once; the
// connections finish closing in the background.
func (d *Node) closeAllDirect() {
	for _, dc := range d.direct.snapshot() {
		dc.resp.closeAll()
		dc.close()
	}
}

// peerDirectDialTimeout bounds a direct link attempt to a peer node, from the
// dial through admission.
const peerDirectDialTimeout = 3 * time.Second

// errNoDirect reports a peer node that cannot be dialed directly: no
// endpoints, no p2p capability, or nothing to admit this node with.
var errNoDirect = errors.New("no direct endpoint")

// directHTTPClient dials direct endpoints straight to the endpoint: no proxy
// from the environment, which would bypass the tailnet peer check (and in
// plaintext carry the token in the clear), and no redirects.
var directHTTPClient = &http.Client{
	Transport:     noProxyTransport(),
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// dialDirect opens pc on a direct link to nd: the first ws/wss endpoint whose
// host is a tailnet peer, admitted by Noise with E2EE, or by the master token
// in plaintext. On success pc is registered on its link.
func (p *peerPool) dialDirect(pc *peerChan, nd api.NodeDescriptor) error {
	if !nd.Capabilities.P2P || len(nd.P2PEndpoints) == 0 || p.d.tailnetD == nil {
		return errNoDirect
	}
	var pub []byte
	if p.d.e2ee {
		var err error
		if pub, err = base64.StdEncoding.DecodeString(nd.IdentityPubKey); err != nil || len(pub) == 0 {
			return errNoDirect
		}
	} else if p.d.masterToken() == "" {
		return errNoDirect
	}
	ctx, cancel := context.WithTimeout(p.ctx, peerDirectDialTimeout)
	defer cancel()
	err := errNoDirect
	for _, raw := range nd.P2PEndpoints {
		u, perr := url.Parse(raw)
		if perr != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" {
			continue
		}
		if !tailnet.Dialable(ctx, p.d.tailnetD, u.Hostname()) {
			err = fmt.Errorf("%s is not a reachable tailnet peer", u.Hostname())
			continue
		}
		if err = p.dialEndpoint(ctx, pc, nd.ID, pub, raw); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return err
}

// dialEndpoint connects to rawURL and admits a fresh direct channel to
// nodeID on it. Frames on the link reach pc only once it is registered; the
// handshake reply before that is taken here.
func (p *peerPool) dialEndpoint(ctx context.Context, pc *peerChan, nodeID string, pub []byte, rawURL string) (err error) {
	chanID, err := newDirectChanID()
	if err != nil {
		return err
	}
	var live atomic.Bool
	hsFrames := make(chan api.RelayFrame, 1)
	opts := api.PeerOptions{
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) {
			if f.Route.ChanID != chanID {
				return
			}
			if live.Load() {
				p.frame(pc, f)
				return
			}
			if f.Method == api.MethodE2EHandshake {
				select {
				case hsFrames <- f:
				default:
				}
			}
		},
		KeepaliveInterval:         api.DefaultKeepaliveInterval,
		KeepaliveTimeout:          api.DefaultKeepaliveTimeout,
		KeepaliveFailureThreshold: api.DefaultKeepaliveFailures,
	}
	if !p.d.e2ee {
		// The target names a master-token opener by asking it.
		opts.Dispatch = p.d.identifyOnlyDispatch()
	}
	out, err := api.DialWSPeer(ctx, rawURL, "", directHTTPClient, opts)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			go out.Close()
		}
	}()
	var ch *api.Channel
	if p.d.e2ee {
		ch, err = directHandshake(ctx, out, p.d.identity, pub, nodeID, chanID, hsFrames)
	} else {
		ch, err = presentMasterToken(ctx, out, p.d.masterToken(), nodeID, chanID)
	}
	if err != nil {
		return fmt.Errorf("node %s: direct link: %w", nodeID, err)
	}
	pc.ch.Store(ch)
	if err := p.register(pc, chanID, nil, out, viaTCP); err != nil {
		return err
	}
	live.Store(true)
	return nil
}

// directHandshake runs the Noise initiator for chanID on out against the
// target's key pub.
func directHandshake(ctx context.Context, out *api.Peer, static e2e.KeyPair, pub []byte, nodeID, chanID string, frames <-chan api.RelayFrame) (*api.Channel, error) {
	init, msg1, err := e2e.NewInitiator(static, pub, api.ChannelPrologue(nodeID, chanID))
	if err != nil {
		return nil, err
	}
	frame, err := api.MarshalHandshakeFrame(chanID, msg1)
	if err != nil {
		return nil, err
	}
	if err := out.SendRawFrame(api.EncodingJSON, frame); err != nil {
		return nil, err
	}
	select {
	case f := <-frames:
		msg2, err := api.HandshakeFromFrame(f)
		if err != nil {
			return nil, err
		}
		sess, err := init.Finish(msg2)
		if err != nil {
			return nil, err
		}
		return api.NewChannel(chanID, sess), nil
	case <-out.Done():
		return nil, errors.New("not admitted")
	case <-ctx.Done():
		return nil, errors.New("handshake timeout")
	}
}

// presentMasterToken admits chanID on out with the master token, which is
// written only after the target proved it holds the token's hash. The hash
// itself is never sent.
func presentMasterToken(ctx context.Context, out *api.Peer, token, nodeID, chanID string) (*api.Channel, error) {
	var nonceRaw [32]byte
	if _, err := rand.Read(nonceRaw[:]); err != nil {
		return nil, err
	}
	nonce := base64.StdEncoding.EncodeToString(nonceRaw[:])
	hash := clienttoken.HashToken(token)
	tag, err := api.P2PTag(hash, nonce)
	if err != nil {
		return nil, err
	}
	want, err := api.P2PMAC(hash, nodeID, chanID, nonce)
	if err != nil {
		return nil, err
	}
	var chal api.TokenChallengeResult
	if err := out.CallContext(ctx, api.MethodTokenChallenge, api.TokenChallengeParams{ChanID: chanID, Nonce: nonce, Tag: tag}, &chal); err != nil {
		return nil, fmt.Errorf("token challenge: %w", err)
	}
	if !hmac.Equal([]byte(chal.MAC), []byte(want)) {
		return nil, errors.New("node did not prove the token")
	}
	if err := out.CallContext(ctx, api.MethodTokenPresent, api.TokenPresentParams{ChanID: chanID, Token: token}, nil); err != nil {
		return nil, fmt.Errorf("token present: %w", err)
	}
	return api.NewPlainChannel(chanID), nil
}

// identifyOnlyDispatch answers node.identify, which a plaintext target sends
// a master-token opener, and nothing else.
func (d *Node) identifyOnlyDispatch() api.DispatchFunc {
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method == api.MethodNodeIdentify {
			return d.handleNodeIdentify(ctx, params)
		}
		return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
	}
}

// newDirectChanID returns "p" and 16 lowercase hex digits.
func newDirectChanID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "p" + hex.EncodeToString(b[:]), nil
}

// noProxyTransport is the default transport without a proxy.
func noProxyTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return t
}
