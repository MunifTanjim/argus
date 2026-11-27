package client

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
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tailnet"
)

// P2POptions configures direct links to nodes. Endpoints and SSH are keyed by
// node id or label, matched case-insensitively.
type P2POptions struct {
	Enabled    bool
	Endpoints  map[string][]string
	SSH        map[string]string
	SSHCommand string
	Token      string
	// NoTCP limits the client to SSH links: a one-shot command does not dial
	// tcp, it uses SSH when configured, else the relay.
	NoTCP bool

	// dialable overrides the tailnet reachability check; nil asks tailscaled.
	dialable func(ctx context.Context, host string) bool
}

// NodePath reports the link a node's channel now travels on. Via is
// "gateway", "tcp" or "ssh".
type NodePath struct{ NodeID, Via string }

const (
	viaGateway = "gateway"
	viaTCP     = "tcp"
)

// p2pRetryInterval is how often a node on the relay retries its upgrade.
var p2pRetryInterval = 60 * time.Second

// Keepalive on direct links, so a half-open link is detected.
var (
	p2pKeepaliveInterval = 15 * time.Second
	p2pKeepaliveTimeout  = 5 * time.Second
	p2pKeepaliveFailures = 2
)

// switchSettleTimeout bounds how long a replaced link stays open for its
// pending calls.
var switchSettleTimeout = 5 * time.Second

const dialableTimeout = 3 * time.Second

// noRedirectClient dials direct endpoints straight to the endpoint: no proxy
// from the environment, which would bypass the tailnet peer check (and in
// plaintext carry the token in the clear), and no redirects.
var noRedirectClient = &http.Client{
	Transport:     noProxyTransport(),
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// SetP2POptions sets the direct-link options and starts upgrading the nodes
// already open on the relay.
func (m *E2EClient) SetP2POptions(o P2POptions) {
	m.mu.Lock()
	m.p2p = o
	var open []api.NodeDescriptor
	for id, nd := range m.desc {
		if m.byNode[id] != nil {
			open = append(open, nd)
		}
	}
	m.mu.Unlock()
	for _, nd := range open {
		m.startUpgrade(nd)
	}
}

// Paths reports each node's link: on every open (gateway) and every change,
// including the return to gateway when the gateway connection drops. An empty
// Via means the node lost its link (roster offline/removed, trust revoked,
// fallback failed); it has no path until it is reported again.
func (m *E2EClient) Paths() <-chan NodePath { return m.paths }

// NodeVias returns the link each open node uses, by node id.
func (m *E2EClient) NodeVias() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.byNode))
	for id, nc := range m.byNode {
		out[id] = nc.via
	}
	return out
}

// sendPath must be called with mu held, so every path is ordered with the
// byNode change it reports. It never blocks.
func (m *E2EClient) sendPath(p NodePath) {
	select {
	case m.paths <- p:
	default:
	}
}

// upgradeRun identifies one upgradeLoop. Losing the node removes its run, so
// a loop left over from before the loss stops instead of acting on the node's
// next channel.
type upgradeRun struct{}

// startUpgrade records nd and runs its upgradeLoop unless one is running.
func (m *E2EClient) startUpgrade(nd api.NodeDescriptor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.desc[nd.ID] = nd
	if !m.p2p.Enabled || m.upgrading[nd.ID] != nil || m.byNode[nd.ID] == nil {
		return
	}
	run := &upgradeRun{}
	m.upgrading[nd.ID] = run
	go m.upgradeLoop(nd, run)
}

// upgradeTarget returns the node's current descriptor while run is its
// upgrade and the node is open; otherwise the loop ends.
func (m *E2EClient) upgradeTarget(id string, run *upgradeRun) (api.NodeDescriptor, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.upgrading[id] != run {
		return api.NodeDescriptor{}, false
	}
	if m.byNode[id] == nil || m.trustCtx.Err() != nil {
		delete(m.upgrading, id)
		return api.NodeDescriptor{}, false
	}
	return m.desc[id], true
}

func (m *E2EClient) endUpgrade(id string, run *upgradeRun) {
	m.mu.Lock()
	if m.upgrading[id] == run {
		delete(m.upgrading, id)
	}
	m.mu.Unlock()
}

// upgradeLoop moves a relay node to a direct link, retrying every
// p2pRetryInterval while it stays on the relay, and falls back to the relay
// when the direct link drops. It ends once the node is lost.
func (m *E2EClient) upgradeLoop(nd api.NodeDescriptor, run *upgradeRun) {
	id := nd.ID
	for {
		var ok bool
		if nd, ok = m.upgradeTarget(id, run); !ok {
			return
		}
		if direct := m.tryUpgrade(nd); direct != nil {
			if !m.holdDirect(nd, direct) {
				m.endUpgrade(id, run)
				return
			}
		}
		select {
		case <-time.After(p2pRetryInterval):
		case <-m.trustCtx.Done():
			m.endUpgrade(id, run)
			return
		case <-m.peer.Done():
			m.endUpgrade(id, run)
			return
		}
	}
}

// p2pEligible reports whether nd may be dialed directly, its endpoints, and
// the relay link a direct one would replace.
func (m *E2EClient) p2pEligible(nd api.NodeDescriptor) (P2POptions, []string, *nodeChan, bool) {
	m.mu.Lock()
	o := m.p2p
	cur := m.byNode[nd.ID]
	m.mu.Unlock()
	if !o.Enabled || o.NoTCP || !m.gatewayP2P.Load() || !nd.Capabilities.P2P {
		return o, nil, nil, false
	}
	// A plaintext node has no identity key; its direct link is admitted by the
	// token instead.
	if m.plaintext && o.Token == "" || !m.plaintext && nd.IdentityPubKey == "" {
		return o, nil, nil, false
	}
	if cur == nil || cur.via != viaGateway {
		return o, nil, nil, false
	}
	eps := endpointsFor(nd, o.Endpoints)
	return o, eps, cur, len(eps) > 0
}

// endpointsFor returns the configured override for nd, by id then by label
// (config keys are lowercased by the loader), else the roster's endpoints.
func endpointsFor(nd api.NodeDescriptor, overrides map[string][]string) []string {
	for k, v := range overrides {
		if strings.EqualFold(k, nd.ID) {
			return v
		}
	}
	if nd.Label != "" {
		for k, v := range overrides {
			if strings.EqualFold(k, nd.Label) {
				return v
			}
		}
	}
	return nd.P2PEndpoints
}

// tryUpgrade dials nd directly and switches the node onto the new link. It
// returns the direct link, or nil when the node stays on the relay.
func (m *E2EClient) tryUpgrade(nd api.NodeDescriptor) *nodeChan {
	if _, _, _, ok := m.p2pEligible(nd); !ok {
		return nil
	}
	var direct *nodeChan
	var err error
	if m.plaintext {
		direct, err = m.dialPlainTCP(nd)
	} else {
		var pub []byte
		if pub, err = base64.StdEncoding.DecodeString(nd.IdentityPubKey); err != nil {
			return nil
		}
		direct, err = m.dialTCP(nd, pub)
	}
	if err != nil {
		m.logOnce(nd.ID+"\x00"+err.Error(), "client: node %s direct link unavailable: %v", nd.ID, err)
		return nil
	}
	if err := m.swapLink(nd.ID, direct); err != nil {
		return nil
	}
	return direct
}

// holdDirect waits for the direct link to end. A remote drop falls back to
// the relay; a link closed by the client (roster, trust, Close) does not. It
// reports whether the upgrade loop should go on.
func (m *E2EClient) holdDirect(nd api.NodeDescriptor, direct *nodeChan) bool {
	select {
	case <-direct.out.Done():
	case <-direct.done:
	case <-m.trustCtx.Done():
		return false
	case <-m.peer.Done():
		// Gateway loss drops every link; the node has none until a reconnect
		// reopens it.
		m.mu.Lock()
		if m.byNode[nd.ID] == direct {
			m.sendPath(NodePath{NodeID: nd.ID})
		}
		m.mu.Unlock()
		direct.close()
		return false
	}
	if !m.isLink(nd.ID, direct) {
		return true // closed by the client; upgradeTarget decides
	}
	if err := m.fallBack(nd, direct); err != nil {
		// Only a node still on the dead link is lost here: a roster event that
		// landed meanwhile has already accounted for it.
		log.Printf("client: node %s direct link dropped and the relay failed: %v", nd.ID, err)
		m.loseNodeIf(nd.ID, direct, false)
	}
	return true
}

// isLink reports whether nc is still nodeID's link.
func (m *E2EClient) isLink(nodeID string, nc *nodeChan) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byNode[nodeID] == nc
}

// fallBack opens a relay channel to nd and switches the node onto it from
// the dead link direct.
func (m *E2EClient) fallBack(nd api.NodeDescriptor, direct *nodeChan) error {
	pub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
	if err != nil {
		return err
	}
	relay, err := m.openRelay(nd, pub, true)
	if err != nil {
		return err
	}
	relay.replaces = direct
	var res api.IdentifyResult
	if err := m.callChan(relay, api.MethodNodeIdentify, api.IdentifyParams{Capabilities: &api.PeerCapabilities{CBOR: true}}, &res, nil); err != nil {
		relay.close()
		return err
	}
	relay.cbor.Store(m.gatewayCBOR.Load() && res.Capabilities.CBOR)
	if !m.plaintext {
		m.mu.Lock()
		m.authTip[string(pub)] = res.Tip
		m.mu.Unlock()
	}
	return m.swapLink(nd.ID, relay)
}

// dialTCP opens and identifies a direct link to nd on the first reachable
// ws/wss endpoint that is a tailnet peer.
func (m *E2EClient) dialTCP(nd api.NodeDescriptor, pub []byte) (*nodeChan, error) {
	o, eps, cur, ok := m.p2pEligible(nd)
	if !ok {
		return nil, errors.New("no direct endpoint")
	}
	dialable := o.dialable
	if dialable == nil {
		dialable = func(ctx context.Context, host string) bool {
			return tailnet.Dialable(ctx, tailnet.Local(), host)
		}
	}
	err := errors.New("no ws or wss endpoint")
	for _, raw := range eps {
		u, perr := url.Parse(raw)
		if perr != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" {
			m.logOnce("endpoint\x00"+raw, "client: node %s: ignoring direct endpoint %q: not a ws or wss URL", nd.ID, raw)
			continue
		}
		ctx, cancel := context.WithTimeout(m.trustCtx, dialableTimeout)
		reachable := dialable(ctx, u.Hostname())
		cancel()
		if !reachable {
			err = fmt.Errorf("%s is not a reachable tailnet peer", u.Hostname())
			continue
		}
		var nc *nodeChan
		var derr error
		if m.plaintext {
			nc, derr = m.dialPlainEndpoint(nd, o.Token, raw)
		} else {
			nc, derr = m.dialEndpoint(nd, pub, raw)
		}
		if derr == nil {
			nc.replaces = cur
			return nc, nil
		}
		err = derr
	}
	return nil, err
}

// dialEndpoint connects to rawURL, runs the Noise handshake on a fresh direct
// channel and identifies the node. The node is not switched onto it.
func (m *E2EClient) dialEndpoint(nd api.NodeDescriptor, pub []byte, rawURL string) (_ *nodeChan, err error) {
	chanID, err := newDirectChanID()
	if err != nil {
		return nil, err
	}
	init, msg1, err := e2e.NewInitiator(m.static, pub, api.ChannelPrologue(nd.ID, chanID))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.trustCtx, time.Duration(handshakeTimeoutNs.Load()))
	peer, err := m.dialDirectPeer(ctx, rawURL)
	cancel()
	if err != nil {
		return nil, err
	}
	nc := &nodeChan{nodeID: nd.ID, label: nd.Label, identityPub: append([]byte(nil), pub...), init: init, hs: make(chan error, 1), standby: true}
	m.useDirectLink(nc, peer, chanID)
	m.mu.Lock()
	m.byChanID[chanID] = nc
	m.mu.Unlock()
	defer func() {
		if err != nil {
			nc.close()
		}
	}()
	if m.trustCtx.Err() != nil {
		return nil, errors.New("client closed")
	}
	if err := m.handshake(nc, chanID, msg1); err != nil {
		return nil, err
	}
	var res api.IdentifyResult
	if err := m.callChan(nc, api.MethodNodeIdentify, api.IdentifyParams{Capabilities: &api.PeerCapabilities{CBOR: true}}, &res, nil); err != nil {
		return nil, fmt.Errorf("identify: %w", err)
	}
	// No gateway on this link: the node's declaration alone decides CBOR.
	nc.cbor.Store(res.Capabilities.CBOR)
	m.mu.Lock()
	m.authTip[string(pub)] = res.Tip
	m.mu.Unlock()
	return nc, nil
}

func (m *E2EClient) dialDirectPeer(ctx context.Context, rawURL string) (*api.Peer, error) {
	return api.DialWSPeer(ctx, rawURL, "", noRedirectClient, api.PeerOptions{
		OnRelayFrame:              m.onRelayFrame,
		KeepaliveInterval:         p2pKeepaliveInterval,
		KeepaliveTimeout:          p2pKeepaliveTimeout,
		KeepaliveFailureThreshold: p2pKeepaliveFailures,
	})
}

// dialPlainTCP opens and identifies a plaintext direct link to nd on the first
// reachable ws/wss endpoint that is a tailnet peer.
func (m *E2EClient) dialPlainTCP(nd api.NodeDescriptor) (*nodeChan, error) {
	return m.dialTCP(nd, nil)
}

// dialPlainEndpoint connects to rawURL and admits the client with its token,
// which is written only after the node proved it holds the token's hash. The
// hash itself is never sent. The node is not switched onto the new link.
func (m *E2EClient) dialPlainEndpoint(nd api.NodeDescriptor, token, rawURL string) (_ *nodeChan, err error) {
	chanID, err := newDirectChanID()
	if err != nil {
		return nil, err
	}
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
	want, err := api.P2PMAC(hash, nd.ID, chanID, nonce)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(handshakeTimeoutNs.Load())
	ctx, cancel := context.WithTimeout(m.trustCtx, timeout)
	peer, err := m.dialDirectPeer(ctx, rawURL)
	cancel()
	if err != nil {
		return nil, err
	}
	// Admission calls take no context; closing the peer ends them.
	guard := time.AfterFunc(timeout, func() { peer.Close() })
	defer func() {
		guard.Stop()
		if err != nil {
			go peer.Close()
		}
	}()
	var chal api.TokenChallengeResult
	if err := peer.Call(api.MethodTokenChallenge, api.TokenChallengeParams{ChanID: chanID, Nonce: nonce, Tag: tag}, &chal); err != nil {
		return nil, fmt.Errorf("token challenge: %w", err)
	}
	if !hmac.Equal([]byte(chal.MAC), []byte(want)) {
		return nil, errors.New("node did not prove the token")
	}
	if err := peer.Call(api.MethodTokenPresent, api.TokenPresentParams{ChanID: chanID, Token: token}, nil); err != nil {
		return nil, fmt.Errorf("token present: %w", err)
	}
	guard.Stop()

	nc := &nodeChan{nodeID: nd.ID, label: nd.Label, hs: make(chan error, 1), standby: true}
	m.useDirectLink(nc, peer, chanID)
	nc.ch.Store(api.NewPlainChannel(chanID))
	m.mu.Lock()
	m.byChanID[chanID] = nc
	m.mu.Unlock()
	defer func() {
		if err != nil {
			nc.close()
		}
	}()
	if m.trustCtx.Err() != nil {
		return nil, errors.New("client closed")
	}
	// The node creates its side of the channel on the first channel frame.
	var res api.IdentifyResult
	if err := m.callChan(nc, api.MethodNodeIdentify, api.IdentifyParams{Capabilities: &api.PeerCapabilities{CBOR: true}}, &res, nil); err != nil {
		return nil, fmt.Errorf("identify: %w", err)
	}
	nc.cbor.Store(res.Capabilities.CBOR)
	return nc, nil
}

// useDirectLink binds nc to its own direct connection. Its close drops the
// connection and removes nc from the client's maps.
func (m *E2EClient) useDirectLink(nc *nodeChan, peer *api.Peer, chanID string) {
	nc.out = peer
	nc.via = viaTCP
	nc.done = make(chan struct{})
	var once sync.Once
	nc.close = func() {
		once.Do(func() {
			m.unregister(nc, chanID)
			go peer.Close()
		})
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

// swapLink makes next the node's link, only while next.replaces still is:
// a node lost and reopened meanwhile keeps its new channel. The old link stops delivering
// notifications at once (replies to its pending calls still route) and closes
// once those calls settle or switchSettleTimeout passes. The session mirror is
// reconciled from sessions.list on next before the path change is reported.
func (m *E2EClient) swapLink(nodeID string, next *nodeChan) error {
	m.mu.Lock()
	old := m.byNode[nodeID]
	if old == nil || old != next.replaces || m.gate.Tripped() || m.trustCtx.Err() != nil {
		m.mu.Unlock()
		next.close()
		return fmt.Errorf("client: node %s is no longer open", nodeID)
	}
	m.byNode[nodeID] = next
	next.replaces = nil // retired links must not stay reachable
	old.routeNotes.Store(false)
	m.mu.Unlock()
	go retire(old)

	// The reply is reconciled on the read loop: notifications before it are
	// covered by the list and stay dropped, the ones after it are delivered.
	reconcile := func(r pendingReply) {
		defer next.routeNotes.Store(true)
		if r.rpcErr != nil {
			return
		}
		var list []session.Session
		if len(r.result) > 0 {
			if err := r.enc.Unmarshal(r.result, &list); err != nil {
				return
			}
		}
		m.reconcileSessions(next, list)
	}
	if err := m.callChan(next, api.MethodSessionsList, nil, nil, reconcile); err != nil {
		next.routeNotes.Store(true)
		log.Printf("client: node %s: sessions.list after switch failed: %v", nodeID, err)
	}
	// Only while next is still the link: a node lost during the list has
	// already been reported with an empty Via.
	m.mu.Lock()
	if m.byNode[nodeID] == next {
		m.sendPath(NodePath{NodeID: nodeID, Via: next.via})
	}
	m.mu.Unlock()
	return nil
}

// retire closes a replaced link once its pending calls settle, or after
// switchSettleTimeout.
func retire(nc *nodeChan) {
	deadline := time.NewTimer(switchSettleTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for nc.calls.Load() > 0 {
		select {
		case <-deadline.C:
			nc.close()
			return
		case <-nc.done:
			return
		case <-tick.C:
		}
	}
	nc.close()
}

// reconcileSessions brings the node's session mirror in line with list,
// emitting added/updated for new or changed sessions and removed for those
// missing. A live switch never reports sessions offline.
func (m *E2EClient) reconcileSessions(nc *nodeChan, list []session.Session) {
	m.mu.Lock()
	mirror := m.nodeSessions[nc.nodeID]
	if mirror == nil {
		mirror = map[string]session.Session{}
		m.nodeSessions[nc.nodeID] = mirror
	}
	var events []registry.Event
	seen := make(map[string]bool, len(list))
	for _, s := range list {
		s = withOrigin(s, nc.nodeID, nc.label)
		seen[s.ID] = true
		prev, ok := mirror[s.ID]
		switch {
		case !ok:
			events = append(events, registry.Event{Type: registry.EventAdded, Session: s})
		case !sameSession(prev, s):
			events = append(events, registry.Event{Type: registry.EventUpdated, Session: s})
		}
		mirror[s.ID] = s
	}
	for id, s := range mirror {
		if !seen[id] {
			delete(mirror, id)
			events = append(events, registry.Event{Type: registry.EventRemoved, Session: s})
		}
	}
	if len(mirror) == 0 {
		delete(m.nodeSessions, nc.nodeID)
	}
	m.mu.Unlock()
	m.emitSessionEvents(events)
}

func sameSession(a, b session.Session) bool {
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(ja, jb)
}

// logOnce logs a message the first time key is seen.
func (m *E2EClient) logOnce(key, format string, args ...any) {
	m.mu.Lock()
	seen := m.p2pWarned[key]
	m.p2pWarned[key] = true
	m.mu.Unlock()
	if !seen {
		log.Printf(format, args...)
	}
}

// noProxyTransport is the default transport without a proxy.
func noProxyTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return t
}
