package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"sync/atomic"

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
}

// relayResponder terminates E2E channels arriving over the gateway uplink: it runs
// the Noise responder handshake per chan_id, decrypts requests, dispatches them
// through the node's handlers, and seals the responses + notifications back.
type relayResponder struct {
	d        *Node
	static   e2e.KeyPair
	dispatch api.DispatchFunc
	peer     atomic.Pointer[api.Peer]

	mu    sync.Mutex
	chans map[string]*chanState
}

func (d *Node) newRelayResponder() *relayResponder {
	return &relayResponder{
		d:        d,
		static:   d.identity,
		dispatch: d.remoteDispatch(),
		chans:    map[string]*chanState{},
	}
}

func (r *relayResponder) lookup(chanID string) *chanState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chans[chanID]
}

// onFrame handles a relay frame from a client (via the gateway) on the uplink read
// loop. It runs synchronously so per-channel decrypt order matches wire order.
func (r *relayResponder) onFrame(f api.RelayFrame) {
	peer := r.peer.Load()
	if peer == nil {
		return // setup race: peer not stored yet; the client re-establishes
	}
	cs := r.lookup(f.Route.ChanID)
	if cs == nil {
		if f.Method == api.MethodE2EHandshake {
			r.handshake(peer, f)
		}
		return // no session: drop non-handshake frames (client must handshake first)
	}
	if f.ID != nil && f.Method != "" { // a request
		params, err := cs.ch.OpenParams(f)
		if err != nil {
			return // undecryptable (tamper/desync): drop
		}
		go r.serve(cs, f.ID, f.Method, params)
	}
}

// handshake runs the Noise responder for a new channel and replies with msg2.
func (r *relayResponder) handshake(peer *api.Peer, f api.RelayFrame) {
	msg1, err := api.HandshakeFromFrame(f)
	if err != nil {
		return
	}
	sess, clientStatic, msg2, err := e2e.Respond(r.static, api.ChannelPrologue(r.d.id, f.Route.ChanID), msg1)
	if err != nil {
		return // wrong key/prologue: drop; client retries on a new chan
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
		return
	}
	base, cancel := context.WithCancel(context.Background())
	cs := &chanState{ch: api.NewChannel(f.Route.ChanID, sess), cancel: cancel, clientStatic: append([]byte(nil), clientStatic...)}
	cs.ctx = api.WithNotifier(base, &channelNotifier{r: r, cs: cs})
	r.mu.Lock()
	r.chans[f.Route.ChanID] = cs
	r.mu.Unlock()
	frame, err := api.MarshalHandshakeFrame(f.Route.ChanID, msg2)
	if err != nil {
		return
	}
	_ = peer.SendRawFrame(frame)
}

// serve dispatches a decrypted request and seals the response back on the channel.
func (r *relayResponder) serve(cs *chanState, id *json.RawMessage, method string, params json.RawMessage) {
	result, err := r.dispatch(cs.ctx, method, params)
	var raw json.RawMessage
	var rpcErr *api.RPCError
	if err != nil {
		if e, ok := err.(*api.RPCError); ok {
			rpcErr = e
		} else {
			rpcErr = &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
		}
	} else if b, mErr := json.Marshal(result); mErr != nil {
		rpcErr = &api.RPCError{Code: api.CodeInternalError, Message: mErr.Error()}
	} else {
		raw = b
	}
	peer := r.peer.Load()
	if peer == nil {
		return
	}
	cs.sendMu.Lock()
	defer cs.sendMu.Unlock()
	frame, err := cs.ch.SealResponseFrame(id, raw, rpcErr)
	if err != nil {
		return
	}
	_ = peer.SendRawFrame(frame)
}

// closeAll cancels every channel's context (dropping its pollers/terminals) and
// clears the table. Called when the uplink ends.
func (r *relayResponder) closeAll() {
	r.mu.Lock()
	for id, cs := range r.chans {
		cs.cancel()
		delete(r.chans, id)
	}
	r.mu.Unlock()
}

// closeChan cancels and removes a live channel by id.
func (r *relayResponder) closeChan(chanID string) {
	r.mu.Lock()
	cs := r.chans[chanID]
	delete(r.chans, chanID)
	r.mu.Unlock()
	if cs != nil {
		cs.cancel()
	}
}

// reevaluate drops live channels whose client is no longer authorized by the
// current trust store. A nil/Disabled/local-disabled store closes nothing.
func (r *relayResponder) reevaluate() {
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

// channelNotifier seals node->client notifications (session.event, transcript.delta,
// terminal.output, ...) onto its channel. Handlers obtain it via NotifierFrom(cs.ctx),
// so existing push paths stream over E2E with no handler changes.
type channelNotifier struct {
	r  *relayResponder
	cs *chanState
}

func (cn *channelNotifier) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	peer := cn.r.peer.Load()
	if peer == nil {
		return nil
	}
	cn.cs.sendMu.Lock()
	defer cn.cs.sendMu.Unlock()
	frame, err := cn.cs.ch.SealNotificationFrame(method, api.RouteHeader{}, raw)
	if err != nil {
		return err
	}
	return peer.SendRawFrame(frame)
}
