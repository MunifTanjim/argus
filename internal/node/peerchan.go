package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
)

const peerHandshakeTimeout = 10 * time.Second

// peerCallTimeoutNs bounds one call on a peer channel; a variable so tests can
// shorten it.
var peerCallTimeoutNs atomic.Int64

func init() { peerCallTimeoutNs.Store(int64(30 * time.Second)) }

// peerSpawnTimeoutFactor scales the call timeout for orch.session.spawn: a spawn
// can create a worktree and start an agent, which takes longer than a call.
const peerSpawnTimeoutFactor = 4

var (
	errPeerOffline     = errors.New("peer node offline")
	errPeerChanClosed  = errors.New("peer channel closed")
	errPeerCallTimeout = errors.New("call timeout")
)

func offline(nodeID string) error {
	return fmt.Errorf("node %s is offline: %w", nodeID, errPeerOffline)
}

// peerPool holds the relay channels this node opened to other nodes over one
// gateway uplink: one channel per peer node, opened on first use. It is the
// initiator side; channels other nodes open to this node belong to the
// relayResponder.
type peerPool struct {
	d      *Node
	peer   atomic.Pointer[api.Peer]
	ctx    context.Context // cancelled by closeAll
	cancel context.CancelFunc
	nextID atomic.Uint64

	mu     sync.Mutex
	byNode map[string]*peerChan // opening or open
	byChan map[string]*peerChan // chan_id assigned
	closed bool
}

// peerChan is one channel to a peer node.
type peerChan struct {
	nodeID string
	uplink uint64         // the roster's uplink count for nodeID when pc was created
	chanID string         // set under pool.mu once relay.open returns
	init   *e2e.Initiator // E2EE only; set under pool.mu before msg1 is sent
	hs     chan error     // handshake result, buffered 1
	ready  chan struct{}  // closed when the open finishes; err is then set
	gone   chan struct{}  // closed once pc leaves the pool
	goneMu sync.Once
	err    error
	ch     atomic.Pointer[api.Channel]

	sendMu   sync.Mutex                // serializes Seal+SendRawFrame so enc nonce order == wire order
	decFails int                       // read loop only
	pending  map[uint64]chan peerReply // guarded by pool.mu
}

type peerReply struct {
	result []byte
	rpcErr *api.RPCError
	enc    api.Encoding
	err    error
}

func (d *Node) newPeerPool() *peerPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &peerPool{
		d:      d,
		ctx:    ctx,
		cancel: cancel,
		byNode: map[string]*peerChan{},
		byChan: map[string]*peerChan{},
	}
}

// call sends method to nodeID on this node's channel to it, opening the channel
// first if needed, and decodes the result into out (which may be nil). A nil
// pool (no uplink) reports the node offline.
func (p *peerPool) call(ctx context.Context, nodeID, method string, params, out any) error {
	if p == nil {
		return offline(nodeID)
	}
	peer := p.peer.Load()
	if nd, ok := p.d.roster.get(nodeID); !ok || !nd.Online || peer == nil {
		return offline(nodeID)
	}
	pc, err := p.channel(ctx, nodeID)
	if err != nil {
		return err
	}
	ch := pc.ch.Load()

	_, raw, err := api.EncodeParams(method, params, false)
	if err != nil {
		return err
	}
	id := p.nextID.Add(1)
	idRaw := json.RawMessage(strconv.FormatUint(id, 10))
	replyCh := make(chan peerReply, 1)
	p.mu.Lock()
	if p.byChan[pc.chanID] != pc {
		p.mu.Unlock()
		return fmt.Errorf("node %s: %w", nodeID, errPeerChanClosed)
	}
	pc.pending[id] = replyCh
	p.mu.Unlock()

	pc.sendMu.Lock()
	frame, err := ch.SealRequestFrame(api.EncodingJSON, &idRaw, method, nodeID, raw)
	if err == nil {
		err = peer.SendRawFrame(api.EncodingJSON, frame)
	}
	pc.sendMu.Unlock()
	if err != nil {
		p.drop(pc)
		return fmt.Errorf("node %s: send %s: %w", nodeID, method, err)
	}

	timeout := time.Duration(peerCallTimeoutNs.Load())
	if method == api.MethodOrchSessionSpawn {
		timeout *= peerSpawnTimeoutFactor
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case reply := <-replyCh:
		if reply.err != nil {
			return fmt.Errorf("node %s: %s: %w", nodeID, method, reply.err)
		}
		if reply.rpcErr != nil {
			return reply.rpcErr
		}
		if out == nil || len(reply.result) == 0 {
			return nil
		}
		return reply.enc.Unmarshal(reply.result, out)
	case <-ctx.Done():
		p.forget(pc, id)
		return ctx.Err()
	case <-timer.C:
		// Only this call fails: a slow handler says nothing about the channel,
		// which other calls may be sharing.
		p.forget(pc, id)
		return fmt.Errorf("node %s: %s: %w", nodeID, method, errPeerCallTimeout)
	}
}

// channel returns the open channel to nodeID. Concurrent callers share one
// in-flight open.
func (p *peerPool) channel(ctx context.Context, nodeID string) (*peerChan, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, offline(nodeID)
	}
	pc := p.byNode[nodeID]
	owner := pc == nil
	if owner {
		pc = &peerChan{
			nodeID:  nodeID,
			uplink:  p.d.roster.uplink(nodeID),
			hs:      make(chan error, 1),
			ready:   make(chan struct{}),
			gone:    make(chan struct{}),
			pending: map[uint64]chan peerReply{},
		}
		p.byNode[nodeID] = pc
	}
	p.mu.Unlock()
	if owner {
		// The open is shared, so it runs on the pool's context, not the caller's,
		// and every caller, this one included, waits for it only as long as its
		// own context allows.
		go func() {
			pc.err = p.open(pc)
			if pc.err != nil {
				p.drop(pc)
			}
			close(pc.ready)
		}()
	}
	select {
	case <-pc.ready:
		if pc.err != nil {
			return nil, pc.err
		}
		return pc, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// open runs relay.open and, with E2EE, the Noise initiator handshake.
func (p *peerPool) open(pc *peerChan) error {
	ctx, cancel := context.WithTimeout(p.ctx, peerHandshakeTimeout)
	defer cancel()
	peer := p.peer.Load()
	nd, ok := p.d.roster.get(pc.nodeID)
	if !ok || !nd.Online || peer == nil {
		return offline(pc.nodeID)
	}
	var pub []byte
	if p.d.e2ee {
		var err error
		if pub, err = base64.StdEncoding.DecodeString(nd.IdentityPubKey); err != nil || len(pub) == 0 {
			return fmt.Errorf("node %s has no usable identity key", pc.nodeID)
		}
		// The key comes from the gateway's roster. In locked mode the gateway is
		// not trusted, so the key must be one the trust log authorizes.
		if p.d.rejectsChannels() {
			return fmt.Errorf("node %s: this node is quarantined and opens no channels", pc.nodeID)
		}
		if st := p.d.trust.Load(); st != nil && !st.Disabled() && !p.d.localDisabled() && !st.DeviceAuthorized(pub) {
			return fmt.Errorf("node %s: identity key is not authorized by the trust log", pc.nodeID)
		}
	}
	var res api.RelayOpenResult
	if err := peer.CallContext(ctx, api.MethodRelayOpen, api.RelayOpenParams{NodeID: pc.nodeID}, &res); err != nil {
		var rpcErr *api.RPCError
		if errors.As(err, &rpcErr) && strings.HasPrefix(rpcErr.Message, api.ErrMsgUnknownNode) {
			return offline(pc.nodeID)
		}
		if nd, ok := p.d.roster.get(pc.nodeID); !ok || !nd.Online {
			return offline(pc.nodeID)
		}
		return fmt.Errorf("node %s: relay.open: %w", pc.nodeID, err)
	}
	if !p.d.e2ee {
		pc.ch.Store(api.NewPlainChannel(res.ChanID))
		return p.register(pc, res.ChanID, nil)
	}
	init, msg1, err := e2e.NewInitiator(p.d.identity, pub, api.ChannelPrologue(pc.nodeID, res.ChanID))
	if err != nil {
		p.releaseChan(res.ChanID)
		return fmt.Errorf("node %s: handshake: %w", pc.nodeID, err)
	}
	if err := p.register(pc, res.ChanID, init); err != nil {
		return err
	}
	frame, err := api.MarshalHandshakeFrame(res.ChanID, msg1)
	if err == nil {
		err = peer.SendRawFrame(api.EncodingJSON, frame)
	}
	if err != nil {
		return fmt.Errorf("node %s: handshake: %w", pc.nodeID, err)
	}
	select {
	case err := <-pc.hs:
		if err != nil {
			return fmt.Errorf("node %s: handshake: %w", pc.nodeID, err)
		}
		return nil
	case <-pc.gone:
		return fmt.Errorf("node %s: handshake: %w", pc.nodeID, errPeerChanClosed)
	case <-peer.Done():
		return fmt.Errorf("node %s: handshake: uplink closed", pc.nodeID)
	case <-ctx.Done():
		return fmt.Errorf("node %s: handshake timeout", pc.nodeID)
	}
}

// register records pc under its chan_id so onFrame can route to it.
func (p *peerPool) register(pc *peerChan, chanID string, init *e2e.Initiator) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return offline(pc.nodeID)
	}
	pc.chanID = chanID
	pc.init = init
	p.byChan[chanID] = pc
	return nil
}

func (p *peerPool) owns(chanID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.byChan[chanID]
	return ok
}

// onFrame handles a relay frame on a channel this node opened. It runs on the
// uplink read loop so decrypt order matches wire order.
func (p *peerPool) onFrame(f api.RelayFrame) {
	p.mu.Lock()
	pc := p.byChan[f.Route.ChanID]
	p.mu.Unlock()
	if pc == nil {
		return
	}
	if f.Method == api.MethodE2EHandshake {
		if pc.init == nil || pc.ch.Load() != nil {
			return
		}
		err := p.finishHandshake(pc, f)
		select {
		case pc.hs <- err:
		default:
		}
		return
	}
	ch := pc.ch.Load()
	if ch == nil {
		return // frame before the handshake completed
	}
	if f.ID == nil || f.Method != "" {
		// Notifications and stray requests are not
		// consumed here, but are still opened to keep the dec nonce in step.
		if _, err := ch.OpenParams(f); err != nil {
			p.decryptFailed(pc)
			return
		}
		pc.decFails = 0
		return
	}
	result, rpcErr, err := ch.OpenResponse(f)
	if err != nil {
		p.decryptFailed(pc)
		return
	}
	pc.decFails = 0
	id, err := strconv.ParseUint(string(*f.ID), 10, 64)
	if err != nil {
		return
	}
	p.mu.Lock()
	waiter := pc.pending[id]
	delete(pc.pending, id)
	p.mu.Unlock()
	if waiter != nil {
		waiter <- peerReply{result: result, rpcErr: rpcErr, enc: f.Enc}
	}
}

func (p *peerPool) finishHandshake(pc *peerChan, f api.RelayFrame) error {
	msg2, err := api.HandshakeFromFrame(f)
	if err != nil {
		return err
	}
	sess, err := pc.init.Finish(msg2)
	if err != nil {
		return err
	}
	pc.ch.Store(api.NewChannel(f.Route.ChanID, sess))
	return nil
}

func (p *peerPool) decryptFailed(pc *peerChan) {
	if wedgeWarn(&pc.decFails) {
		p.d.log.Warn("peer channel decrypt failures, likely wedged (nonce desync)", "node", pc.nodeID, "chan", pc.chanID, "count", pc.decFails)
	}
}

// watchRoster closes the channel to a node as soon as the roster reports it
// offline or removed, until the pool is closed. It also closes a channel
// created before the node came back online, because that channel is bound to
// the node's old uplink.
func (p *peerPool) watchRoster() {
	events, cancel := p.d.roster.subscribe()
	go func() {
		defer cancel()
		for {
			select {
			case <-p.ctx.Done():
				return
			case ev := <-events:
				if ev.Type != api.NodeEventOffline && ev.Type != api.NodeEventRemoved && ev.Type != api.NodeEventOnline {
					continue
				}
				p.mu.Lock()
				pc := p.byNode[ev.Node.ID]
				p.mu.Unlock()
				if pc != nil && (ev.Type != api.NodeEventOnline || pc.uplink != p.d.roster.uplink(ev.Node.ID)) {
					p.drop(pc)
				}
			}
		}
	}()
}

// closeChan forgets a channel the gateway reported closed. Its pending calls
// fail at once; the next call reopens.
func (p *peerPool) closeChan(chanID string) {
	p.mu.Lock()
	pc := p.byChan[chanID]
	p.mu.Unlock()
	if pc != nil {
		p.remove(pc)
	}
}

// drop forgets pc and tells the gateway to release its channel.
func (p *peerPool) drop(pc *peerChan) {
	if found, chanID := p.remove(pc); found && chanID != "" {
		p.releaseChan(chanID)
	}
}

// remove deletes pc from the pool and fails its pending calls. It reports
// whether pc was still registered, and its chan_id.
func (p *peerPool) remove(pc *peerChan) (bool, string) {
	p.mu.Lock()
	found := false
	chanID := pc.chanID
	if p.byNode[pc.nodeID] == pc {
		delete(p.byNode, pc.nodeID)
		found = true
	}
	if chanID != "" && p.byChan[chanID] == pc {
		delete(p.byChan, chanID)
		found = true
	}
	pending := pc.pending
	pc.pending = map[uint64]chan peerReply{}
	p.mu.Unlock()
	pc.goneMu.Do(func() { close(pc.gone) })
	for _, w := range pending {
		w <- peerReply{err: errPeerChanClosed}
	}
	return found, chanID
}

// releaseChan sends a best-effort relay.close for chanID.
func (p *peerPool) releaseChan(chanID string) {
	peer := p.peer.Load()
	if peer == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), peerHandshakeTimeout)
		defer cancel()
		_ = peer.CallContext(ctx, api.MethodRelayClose, api.RelayCloseParams{ChanID: chanID}, nil)
	}()
}

func (p *peerPool) forget(pc *peerChan, id uint64) {
	p.mu.Lock()
	delete(pc.pending, id)
	p.mu.Unlock()
}

// closeAll fails every pending call and empties the pool. Called when the
// uplink ends; the pool is not reused.
func (p *peerPool) closeAll() {
	p.mu.Lock()
	p.closed = true
	chans := make([]*peerChan, 0, len(p.byNode)+len(p.byChan))
	for _, pc := range p.byNode {
		chans = append(chans, pc)
	}
	for _, pc := range p.byChan {
		chans = append(chans, pc)
	}
	p.mu.Unlock()
	p.cancel()
	for _, pc := range chans {
		p.remove(pc)
	}
}
