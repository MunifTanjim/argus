package node

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
)

// chanState is one live E2E channel this node terminates (one client <-> this node).
type chanState struct {
	ch           *api.Channel
	sendMu       sync.Mutex // serializes Seal+SendRawFrame so enc nonce order == wire order
	ctx          context.Context
	cancel       context.CancelFunc
	clientStatic []byte // Noise static public key of the authenticated client
	stopStream   func() // ends the registry event stream; guarded by relayResponder.mu
	decFails     int    // consecutive decrypt failures; read-loop only, reset on success
	clientCBOR   atomic.Bool
	// opener is the node the gateway named as this channel's opener
	// (relay.peer_opened). Empty for client channels. With E2EE, channelPeer
	// serves the channel as a peer channel only while the opening key matches
	// that node's roster key.
	opener  string
	demoted atomic.Bool // a key mismatch for opener was logged
	// tokenHash is the token a direct client channel was admitted with, and
	// pins the binding store holding its key's pin; both unset otherwise.
	tokenHash string
	pins      *bindingStore
}

// decryptWedgeWarnAfter is the run of consecutive decrypt failures that signals a
// likely wedged channel (dec-nonce desync). Injected garbage cannot reach it — a
// failed decrypt is dropped without advancing the nonce — only a genuine mid-stream
// frame loss sustains it.
const decryptWedgeWarnAfter = 8

// wedgeWarn reports true only on the tick the counter first hits the threshold, so a sustained wedge logs once.
func wedgeWarn(n *int) bool {
	*n++
	return *n == decryptWedgeWarnAfter
}

// relayResponder terminates E2E channels arriving over the gateway uplink: it runs
// the Noise responder handshake per chan_id, decrypts requests, dispatches them
// through the node's handlers, and seals the responses + notifications back.
type relayResponder struct {
	d            *Node
	static       e2e.KeyPair
	dispatch     api.DispatchFunc // client channels
	peerDispatch api.DispatchFunc // channels a verified peer node opened
	peer         atomic.Pointer[api.Peer]
	peers        *peerPool // channels this node opened on the same uplink

	mu         sync.Mutex
	chans      map[string]*chanState
	peerOpened map[string]peerOpened // relay.peer_opened/channel.opened not yet consumed, by chan_id

	binds *bindingStore // client keys bound to their channel's token; cleared by closeAll; nil on a direct link

	// direct is the direct-listener connection this responder serves, nil on
	// the uplink. A direct responder admits by binding or roster key, carries
	// one p… channel, and closes a peer channel instead of demoting it.
	direct *directConn
}

// peerOpened is the gateway's statement of who opened a channel, held until
// that channel's first frame. from is the opening node, "" for a client;
// tokenHash is the opener's token hash, "" from a gateway that only sends
// relay.peer_opened.
type peerOpened struct {
	from      string
	tokenHash string
	at        time.Time
}

// maxPendingPeerOpened bounds unconsumed relay.peer_opened records; past it the
// oldest is dropped. pendingPeerOpenedTTL expires a record whose channel never
// sent a frame. Losing a record only makes that channel a client channel.
const (
	maxPendingPeerOpened = 256
	pendingPeerOpenedTTL = time.Minute
)

func (d *Node) newRelayResponder(peers *peerPool) *relayResponder {
	return &relayResponder{
		d:            d,
		peers:        peers,
		static:       d.identity,
		dispatch:     d.remoteDispatch(),
		peerDispatch: d.peerDispatch(),
		chans:        map[string]*chanState{},
		peerOpened:   map[string]peerOpened{},
		binds:        newBindingStore(d.masterTokenHash()),
	}
}

// notePeerOpened records who the gateway says opened chanID: node from, or a
// client when from is "", with tokenHash. It runs on the uplink read loop,
// before the channel's first frame.
func (r *relayResponder) notePeerOpened(chanID, from, tokenHash string) {
	if chanID == "" || (from == "" && tokenHash == "") {
		return
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	var oldestID string
	var oldest time.Time
	for id, p := range r.peerOpened {
		if now.Sub(p.at) > pendingPeerOpenedTTL {
			delete(r.peerOpened, id)
			continue
		}
		if oldestID == "" || p.at.Before(oldest) {
			oldestID, oldest = id, p.at
		}
	}
	if len(r.peerOpened) >= maxPendingPeerOpened {
		delete(r.peerOpened, oldestID)
	}
	r.peerOpened[chanID] = peerOpened{from: from, tokenHash: tokenHash, at: now}
}

// takePeerOpened removes and returns the gateway's record of chanID's opener,
// or the zero record when there is no live one.
func (r *relayResponder) takePeerOpened(chanID string) peerOpened {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.peerOpened[chanID]
	if !ok {
		return peerOpened{}
	}
	delete(r.peerOpened, chanID)
	if time.Since(p.at) > pendingPeerOpenedTTL {
		return peerOpened{}
	}
	return p
}

// peerKeyMatches reports whether key is nodeID's identity key in the roster.
func (r *relayResponder) peerKeyMatches(nodeID string, key []byte) bool {
	nd, ok := r.d.roster.get(nodeID)
	if !ok || nd.IdentityPubKey == "" {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
	return err == nil && len(want) > 0 && bytes.Equal(want, key)
}

// channelPeer returns the peer node cs serves, or "" for a client channel. An
// E2EE channel's key is checked against the roster on every request: a roster
// key change for the opener demotes the live relay channel to client dispatch,
// and a roster entry that arrives after the handshake promotes it. A direct
// peer channel is closed instead (a demoted one would get client access
// without a token).
func (r *relayResponder) channelPeer(cs *chanState) string {
	if cs.opener == "" {
		return ""
	}
	if r.direct != nil {
		if !r.directPeerValid(cs) {
			r.d.log.Warn("closing direct peer channel: opener left the roster or its key changed", "chan", cs.ch.ID(), "node", cs.opener)
			r.closeChan(cs.ch.ID())
			return ""
		}
		return cs.opener
	}
	if cs.clientStatic != nil && !r.peerKeyMatches(cs.opener, cs.clientStatic) {
		if !cs.demoted.Swap(true) {
			r.d.log.Warn("peer channel key does not match roster; serving as client channel", "chan", cs.ch.ID(), "node", cs.opener)
		}
		return ""
	}
	cs.demoted.Store(false)
	return cs.opener
}

func (r *relayResponder) lookup(chanID string) *chanState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chans[chanID]
}

// onFrame handles a relay frame from a client (via the gateway) on the uplink read
// loop. It runs synchronously so per-channel decrypt order matches wire order.
func (r *relayResponder) onFrame(_ *api.Peer, f api.RelayFrame) {
	peer := r.peer.Load()
	if peer == nil {
		return // setup race: peer not stored yet; the client re-establishes
	}
	// A channel this node opened carries the peer's msg2 and responses, which
	// must not be taken for a new msg1 or a plaintext request.
	if r.peers != nil && r.peers.owns(f.Route.ChanID) {
		r.peers.onFrame(f)
		return
	}
	// Direct links carry only p… ids; the relay never does.
	if r.direct != nil {
		if !validDirectChanID(f.Route.ChanID) {
			r.direct.close()
			return
		}
	} else if strings.HasPrefix(f.Route.ChanID, "p") {
		return
	}
	if f.Method == api.MethodE2EHandshake {
		if r.direct != nil && r.direct.plaintext {
			r.direct.close()
			return
		}
		r.handshake(peer, f)
		return
	}
	cs := r.lookup(f.Route.ChanID)
	if cs == nil {
		switch {
		case r.direct != nil && r.direct.plaintext:
			if cs = r.openDirectPlain(f.Route.ChanID); cs == nil {
				return
			}
		case r.d.e2ee || r.direct != nil:
			return
		default:
			cs = r.createPlain(f.Route.ChanID)
		}
	}
	if f.ID != nil && f.Method != "" {
		params, err := cs.ch.OpenParams(f)
		if err != nil {
			// undecryptable (tamper/desync): drop. A sustained run means the channel
			// is likely wedged; the client re-handshakes a fresh channel.
			if wedgeWarn(&cs.decFails) {
				r.d.log.Warn("channel decrypt failures, likely wedged (nonce desync)", "chan", f.Route.ChanID, "count", cs.decFails)
			}
			return
		}
		cs.decFails = 0
		go r.serve(cs, f.Enc, f.ID, f.Method, params)
	}
}

// handshake runs the Noise responder for a new channel and replies with msg2.
func (r *relayResponder) handshake(peer *api.Peer, f api.RelayFrame) {
	// Consume the gateway's opener record whatever the outcome, so a failed
	// handshake cannot leave it behind.
	opened := r.takePeerOpened(f.Route.ChanID)
	// An unadmitted direct handshake gets no reply and loses its connection.
	reject := func() {
		if r.direct != nil {
			r.direct.close()
		}
	}
	msg1, err := api.HandshakeFromFrame(f)
	if err != nil {
		reject()
		return
	}
	sess, clientStatic, msg2, err := e2e.Respond(r.static, api.ChannelPrologue(r.d.id, f.Route.ChanID), msg1)
	if err != nil {
		reject()
		return // wrong key/prologue: drop; client retries on a new chan
	}
	if r.d.rejectsChannels() {
		r.d.log.Warn("rejected client channel: node is unpinned on a locked network", "chan", f.Route.ChanID)
		reject()
		return
	}
	// Locked-mode enforcement (fail-closed): a node with a trust store accepts a
	// channel only from an authorized client identity. Open mode (nil store) skips
	// this. An empty/unsynced store authorizes no one, so it rejects all until the
	// chain arrives — the correct fail-closed posture. Enforcement is bypassed when
	// the store is Disabled() or the node's local-disable flag is set.
	if st := r.d.trust.Load(); st != nil && !st.Disabled() && !r.d.localDisabled() && !st.DeviceAuthorized(clientStatic) {
		var fp string
		if len(clientStatic) >= 8 {
			fp = base64.StdEncoding.EncodeToString(clientStatic[:8])
		}
		r.d.log.Warn("rejected unauthorized client channel", "chan", f.Route.ChanID, "client", fp)
		reject()
		return
	}
	// A direct channel is a peer channel by roster key, else a client channel
	// by a binding to a valid token; it then needs a free channel slot.
	var directHash string
	if r.direct != nil {
		opener, hash, ok := r.d.directAdmission(clientStatic)
		if !ok {
			r.d.log.Warn("rejected direct channel: key not admitted", "chan", f.Route.ChanID)
			reject()
			return
		}
		if !r.d.direct.admit(r.direct) {
			r.d.log.Warn("rejected direct channel: no free channel slot", "chan", f.Route.ChanID)
			reject()
			return
		}
		opened, directHash = peerOpened{from: opener}, hash
	}
	// The gateway names the opener. channelPeer also requires the opener's Noise
	// key to be that node's roster key, and serves the channel as a client
	// channel while it is not.
	base, cancel := context.WithCancel(context.Background())
	cs := &chanState{ch: api.NewChannel(f.Route.ChanID, sess), cancel: cancel, clientStatic: append([]byte(nil), clientStatic...), opener: opened.from, tokenHash: directHash}
	cn := &channelNotifier{r: r, cs: cs}
	cs.ctx = api.WithNotifier(base, cn)
	r.mu.Lock()
	if _, exists := r.chans[f.Route.ChanID]; exists {
		// A channel for this id already exists (onFrame's lookup and this insert are
		// not atomic). Drop the duplicate and free its context rather than overwriting
		// — an overwrite would orphan the live channel's context until the uplink closes.
		r.mu.Unlock()
		cancel()
		return
	}
	if directHash != "" {
		if b := r.d.bindings(); b != nil {
			b.pin(cs.clientStatic)
			cs.pins = b
		}
	}
	r.chans[f.Route.ChanID] = cs
	r.mu.Unlock()
	// Re-check now that the channel is visible to revocations: one that ran
	// during the handshake found nothing to close.
	if r.direct != nil {
		if !r.d.directStillAdmitted(cs) {
			r.closeChan(f.Route.ChanID)
			return
		}
	}
	frame, err := api.MarshalHandshakeFrame(f.Route.ChanID, msg2)
	if err != nil {
		r.closeChan(f.Route.ChanID)
		return
	}
	if err := peer.SendRawFrame(api.EncodingJSON, frame); err != nil {
		r.closeChan(f.Route.ChanID)
		return
	}
	// Only now, with msg2 on the wire, may sealed frames follow: the client cannot
	// open one until it has finished the handshake. A gateway-relayed client has no
	// other way to learn the session list — there is no accept hook on this path.
	// The handshake completed and passed the locked-mode checks: bind the client's
	// key to its token for direct connections. A node-opened channel never binds.
	if r.direct != nil {
		api.ResetReadLimit(r.direct.conn)
		if cs.opener != "" {
			go r.watchPeerRoster(cs)
		}
	} else if opened.from == "" && opened.tokenHash != "" {
		r.binds.bind(cs.clientStatic, opened.tokenHash)
	}
	r.startStream(f.Route.ChanID, cs, cn)
}

// startStream streams the registry and connection broadcasts to a client
// channel. A channel a node opened gets neither, even while channelPeer serves
// it as a client channel: its opener only makes calls.
func (r *relayResponder) startStream(chanID string, cs *chanState, cn *channelNotifier) {
	if cs.opener != "" {
		return
	}
	stop := r.d.streamRegistry(cn)
	r.mu.Lock()
	live := r.chans[chanID] == cs
	if live {
		cs.stopStream = stop
	}
	r.mu.Unlock()
	if !live {
		stop() // channel closed while the stream was starting
	}
}

// createPlain lazily creates a plain (identity-cipher) channel for plaintext mode.
// It mirrors the tail of handshake but skips Noise and locked-mode checks. With
// no key to check, a gateway peer_opened statement alone makes it a peer channel.
func (r *relayResponder) createPlain(chanID string) *chanState {
	cs, cn := r.insertPlain(chanID, r.takePeerOpened(chanID).from)
	r.startStream(chanID, cs, cn)
	return cs
}

// insertPlain adds a plain channel opened by opener ("" for a client).
func (r *relayResponder) insertPlain(chanID, opener string) (*chanState, *channelNotifier) {
	base, cancel := context.WithCancel(context.Background())
	cs := &chanState{
		ch:     api.NewPlainChannel(chanID),
		cancel: cancel,
		opener: opener,
	}
	cn := &channelNotifier{r: r, cs: cs}
	cs.ctx = api.WithNotifier(base, cn)
	r.mu.Lock()
	r.chans[chanID] = cs
	r.mu.Unlock()
	return cs, cn
}

// serve dispatches a decrypted request and seals the response back on the
// channel in the request's encoding.
func (r *relayResponder) serve(cs *chanState, enc api.Encoding, id *json.RawMessage, method string, params []byte) {
	ctx, dispatch := cs.ctx, r.dispatch
	if p := r.channelPeer(cs); p != "" {
		ctx, dispatch = withPeerNode(ctx, p), r.peerDispatch
	} else if r.direct != nil && cs.opener != "" {
		return // closed by channelPeer, never served as a client channel
	}
	raw, rpcErr := api.Serve(ctx, dispatch, enc, method, params)
	peer := r.peer.Load()
	if peer == nil {
		return
	}
	cs.sendMu.Lock()
	defer cs.sendMu.Unlock()
	frame, err := cs.ch.SealResponseFrame(enc, id, raw, rpcErr)
	if err != nil {
		return
	}
	_ = peer.SendRawFrame(enc, frame)
}

// closeAll cancels every channel's context (dropping its pollers/terminals) and
// clears the table. Called when the uplink ends.
func (r *relayResponder) closeAll() {
	r.mu.Lock()
	var stops []func()
	for id, cs := range r.chans {
		cs.cancel()
		if cs.stopStream != nil {
			stops = append(stops, cs.stopStream)
		}
		if cs.pins != nil {
			cs.pins.unpin(cs.clientStatic)
		}
		delete(r.chans, id)
	}
	clear(r.peerOpened)
	r.mu.Unlock()
	if r.binds != nil {
		r.binds.clear()
	}
	for _, stop := range stops {
		stop()
	}
}

func (r *relayResponder) closeChan(chanID string) {
	r.mu.Lock()
	cs := r.chans[chanID]
	delete(r.chans, chanID)
	delete(r.peerOpened, chanID) // relay.closed before the first frame
	var stop func()
	if cs != nil {
		stop = cs.stopStream
		if cs.pins != nil {
			cs.pins.unpin(cs.clientStatic)
		}
	}
	r.mu.Unlock()
	if cs != nil {
		cs.cancel()
	}
	if stop != nil {
		stop()
	}
	// A direct connection carries one channel; it ends with it.
	if cs != nil && r.direct != nil {
		r.direct.close()
	}
}

// reevaluate drops live channels whose client is no longer authorized by the
// current trust store. A nil/Disabled/local-disabled store closes nothing. It
// leaves opener alone: channelPeer re-checks a peer channel's key against the
// roster on every request, which covers a roster key change whenever it happens.
func (r *relayResponder) reevaluate() {
	if r.direct != nil {
		r.recheckDirect()
		return
	}
	if r.d.rejectsChannels() {
		r.mu.Lock()
		ids := make([]string, 0, len(r.chans))
		for id := range r.chans {
			ids = append(ids, id)
		}
		r.mu.Unlock()
		for _, id := range ids {
			r.d.log.Warn("closing channel: node is unpinned on a locked network", "chan", id)
			r.closeChan(id)
		}
		return
	}
	st := r.d.trust.Load()
	if st == nil || st.Disabled() || r.d.localDisabled() {
		return
	}
	r.mu.Lock()
	type ent struct {
		id     string
		client []byte
	}
	var live []ent
	for id, cs := range r.chans {
		live = append(live, ent{id, cs.clientStatic})
	}
	r.mu.Unlock()
	for _, e := range live {
		if !st.DeviceAuthorized(e.client) {
			var fp string
			if len(e.client) >= 8 {
				fp = base64.StdEncoding.EncodeToString(e.client[:8])
			}
			r.d.log.Warn("closing channel to now-unauthorized client", "chan", e.id, "client", fp)
			r.closeChan(e.id)
		}
	}
}

type peerNodeKey struct{}

func withPeerNode(ctx context.Context, nodeID string) context.Context {
	return context.WithValue(ctx, peerNodeKey{}, nodeID)
}

// peerNodeFrom returns the node that opened the channel a request arrived on,
// when that channel is a verified peer channel.
func peerNodeFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(peerNodeKey{}).(string)
	return id, ok && id != ""
}

// channelNotifier seals node->client notifications (session.event, transcript.delta,
// terminal.output, ...) onto its channel. Handlers obtain it via NotifierFrom(cs.ctx),
// so existing push paths stream over E2E with no handler changes.
type channelNotifier struct {
	r  *relayResponder
	cs *chanState
}

// SetRemoteCBOR records whether the client declared cbor on this channel.
func (cn *channelNotifier) SetRemoteCBOR(ok bool) { cn.cs.clientCBOR.Store(ok) }

var _ api.CBORNegotiator = (*channelNotifier)(nil)

// Notify uses CBOR for a CBOR method only when both the client (on this channel)
// and the gateway (on this uplink) declared cbor. A direct link has no gateway:
// the client's declaration alone decides.
func (cn *channelNotifier) Notify(method string, params any) error {
	peer := cn.r.peer.Load()
	if peer == nil {
		return nil
	}
	cbor := cn.cs.clientCBOR.Load() && (cn.r.direct != nil || peer.RemoteCBOR())
	enc, raw, err := api.EncodeParams(method, params, cbor)
	if err != nil {
		return err
	}
	cn.cs.sendMu.Lock()
	defer cn.cs.sendMu.Unlock()
	frame, err := cn.cs.ch.SealNotificationFrame(enc, method, api.RouteHeader{}, raw)
	if err != nil {
		return err
	}
	return peer.SendRawFrame(enc, frame)
}

// RelayChannelCountForTest reports how many client channels the active uplink holds.
func (d *Node) RelayChannelCountForTest() int {
	r := d.activeResponder.Load()
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.chans)
}

func (r *relayResponder) pendingPeerCountForTest() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.peerOpened)
}
