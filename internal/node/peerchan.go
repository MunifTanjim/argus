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

// The link a peer channel travels on.
const (
	viaGateway = "gateway"
	viaTCP     = "tcp"
)

// peerUpgradeIntervalNs is how often a relay peer channel retries a direct
// link; a variable so tests can shorten it.
var peerUpgradeIntervalNs atomic.Int64

func init() { peerUpgradeIntervalNs.Store(int64(60 * time.Second)) }

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

// peerChan is one channel to a peer node, on the relay or on its own direct
// link.
type peerChan struct {
	nodeID string
	uplink uint64         // the roster's uplink count for nodeID when pc was created
	chanID string         // set under pool.mu once relay.open returns
	init   *e2e.Initiator // E2EE relay only; set under pool.mu before msg1 is sent
	out    *api.Peer      // the uplink, or pc's own direct link; set under pool.mu with chanID
	via    string         // viaGateway or viaTCP; set under pool.mu with chanID
	hs     chan error     // handshake result, buffered 1
	ready  chan struct{}  // closed when the open finishes; err is then set
	gone   chan struct{}  // closed once pc leaves the pool
	goneMu sync.Once
	err    error
	ch     atomic.Pointer[api.Channel]

	removed bool // pc left the pool; guarded by pool.mu

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
		err = pc.out.SendRawFrame(api.EncodingJSON, frame)
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
	pc, err := p.start(nodeID, true)
	if err != nil {
		return nil, err
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

// start returns nodeID's channel, beginning its open when there is none. The
// open tries a direct link first when direct is set, then the relay.
func (p *peerPool) start(nodeID string, direct bool) (*peerChan, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, offline(nodeID)
	}
	pc := p.byNode[nodeID]
	owner := pc == nil
	if owner {
		pc = p.newChan(nodeID, p.d.roster.uplink(nodeID))
		p.byNode[nodeID] = pc
	}
	p.mu.Unlock()
	if owner {
		// The open is shared, so it runs on the pool's context, not the caller's,
		// and every caller, this one included, waits for it only as long as its
		// own context allows.
		go func() {
			pc.err = p.open(pc, direct)
			if pc.err != nil {
				p.drop(pc)
			}
			close(pc.ready)
			if pc.err == nil {
				p.watchLink(pc)
			}
		}()
	}
	return pc, nil
}

func (p *peerPool) newChan(nodeID string, uplink uint64) *peerChan {
	return &peerChan{
		nodeID:  nodeID,
		uplink:  uplink,
		hs:      make(chan error, 1),
		ready:   make(chan struct{}),
		gone:    make(chan struct{}),
		pending: map[uint64]chan peerReply{},
	}
}

// target returns nodeID's roster entry and, with E2EE, its identity key, once
// the checks every open runs have passed.
func (p *peerPool) target(nodeID string) (api.NodeDescriptor, []byte, error) {
	nd, ok := p.d.roster.get(nodeID)
	if !ok || !nd.Online || p.peer.Load() == nil {
		return nd, nil, offline(nodeID)
	}
	if !p.d.e2ee {
		return nd, nil, nil
	}
	pub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
	if err != nil || len(pub) == 0 {
		return nd, nil, fmt.Errorf("node %s has no usable identity key", nodeID)
	}
	// The key comes from the gateway's roster. In locked mode the gateway is
	// not trusted, so the key must be one the trust log authorizes.
	if p.d.rejectsChannels() {
		return nd, nil, fmt.Errorf("node %s: this node is quarantined and opens no channels", nodeID)
	}
	if st := p.d.trust.Load(); st != nil && !st.Disabled() && !p.d.localDisabled() && !st.DeviceAuthorized(pub) {
		return nd, nil, fmt.Errorf("node %s: identity key is not authorized by the trust log", nodeID)
	}
	return nd, pub, nil
}

// open opens pc on a direct link to the node when direct is set and one is
// reachable, else on the relay.
func (p *peerPool) open(pc *peerChan, direct bool) error {
	nd, pub, err := p.target(pc.nodeID)
	if err != nil {
		return err
	}
	if direct {
		err := p.dialDirect(pc, nd)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errNoDirect) {
			p.d.log.Debug("direct peer link unavailable; using the relay", "node", pc.nodeID, "err", err)
		}
	}
	return p.openRelay(pc, pub)
}

// openRelay runs relay.open and, with E2EE, the Noise initiator handshake
// with pub.
func (p *peerPool) openRelay(pc *peerChan, pub []byte) error {
	ctx, cancel := context.WithTimeout(p.ctx, peerHandshakeTimeout)
	defer cancel()
	peer := p.peer.Load()
	if peer == nil {
		return offline(pc.nodeID)
	}
	var res api.RelayOpenResult
	if err := peer.CallContext(ctx, api.MethodRelayOpen, api.RelayOpenParams{NodeID: pc.nodeID}, &res); err != nil {
		var rpcErr *api.RPCError
		if errors.As(err, &rpcErr) && strings.HasPrefix(rpcErr.Message, "unknown node") {
			return offline(pc.nodeID)
		}
		if nd, ok := p.d.roster.get(pc.nodeID); !ok || !nd.Online {
			return offline(pc.nodeID)
		}
		return fmt.Errorf("node %s: relay.open: %w", pc.nodeID, err)
	}
	if !p.d.e2ee {
		pc.ch.Store(api.NewPlainChannel(res.ChanID))
		return p.registerRelay(pc, peer, res.ChanID, nil)
	}
	init, msg1, err := e2e.NewInitiator(p.d.identity, pub, api.ChannelPrologue(pc.nodeID, res.ChanID))
	if err != nil {
		p.releaseChan(res.ChanID)
		return fmt.Errorf("node %s: handshake: %w", pc.nodeID, err)
	}
	if err := p.registerRelay(pc, peer, res.ChanID, init); err != nil {
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

// registerRelay registers pc as a relay channel on the uplink. A channel the
// pool dropped meanwhile is released on the gateway.
func (p *peerPool) registerRelay(pc *peerChan, uplink *api.Peer, chanID string, init *e2e.Initiator) error {
	err := p.register(pc, chanID, init, uplink, viaGateway)
	if errors.Is(err, errPeerChanClosed) {
		p.releaseChan(chanID)
	}
	return err
}

// register records pc under its chan_id, with the link it travels on, so
// onFrame can route to it.
func (p *peerPool) register(pc *peerChan, chanID string, init *e2e.Initiator, out *api.Peer, via string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return offline(pc.nodeID)
	}
	if pc.removed {
		return fmt.Errorf("node %s: %w", pc.nodeID, errPeerChanClosed)
	}
	pc.chanID = chanID
	pc.init = init
	pc.out = out
	pc.via = via
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
// uplink read loop so decrypt order matches wire order. A direct channel takes
// frames only from its own link.
func (p *peerPool) onFrame(f api.RelayFrame) {
	p.mu.Lock()
	pc := p.byChan[f.Route.ChanID]
	direct := pc != nil && pc.via == viaTCP
	p.mu.Unlock()
	if pc == nil || direct {
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
	p.frame(pc, f)
}

// frame opens a frame on pc's link and hands a response to its waiting call.
// It runs on that link's read loop.
func (p *peerPool) frame(pc *peerChan, f api.RelayFrame) {
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

// closeChan forgets a relay channel the gateway reported closed. Its pending
// calls fail at once; the next call reopens. The gateway cannot close a direct
// channel.
func (p *peerPool) closeChan(chanID string) {
	p.mu.Lock()
	pc := p.byChan[chanID]
	direct := pc != nil && pc.via == viaTCP
	p.mu.Unlock()
	if pc != nil && !direct {
		p.remove(pc)
	}
}

// drop forgets pc and releases its channel: on the gateway for a relay
// channel; remove closes a direct one.
func (p *peerPool) drop(pc *peerChan) {
	if found, chanID, via := p.remove(pc); found && chanID != "" && via == viaGateway {
		p.releaseChan(chanID)
	}
}

// remove deletes pc from the pool, fails its pending calls and closes its
// direct link. It reports whether pc was still registered, its chan_id and
// its link kind.
func (p *peerPool) remove(pc *peerChan) (bool, string, string) {
	p.mu.Lock()
	found := false
	pc.removed = true
	chanID, via, out := pc.chanID, pc.via, pc.out
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
	if via == viaTCP {
		// A WebSocket close waits for the remote; callers include read loops.
		go out.Close()
	}
	return found, chanID, via
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

// peerRetireTimeout bounds how long a replaced relay channel stays open for
// its pending calls.
const peerRetireTimeout = 5 * time.Second

// watchLink keeps an open channel on the best link until it leaves the pool:
// a direct channel falls back to the relay when its link drops, and a relay
// channel retries a direct link every peerUpgradeIntervalNs.
func (p *peerPool) watchLink(pc *peerChan) {
	if pc.via == viaTCP {
		p.holdDirect(pc)
	} else {
		p.upgradeLoop(pc)
	}
}

// holdDirect waits for pc's direct link to end. A link that drops while pc is
// still the node's channel fails its calls and falls back to the relay at
// once; a channel the pool removed (roster, trust, uplink close) does not.
func (p *peerPool) holdDirect(pc *peerChan) {
	select {
	case <-pc.out.Done():
	case <-pc.gone:
		return
	}
	p.mu.Lock()
	current := p.byNode[pc.nodeID] == pc && !pc.removed
	p.mu.Unlock()
	if found, _, _ := p.remove(pc); !found || !current {
		return
	}
	p.d.log.Info("direct peer link dropped; falling back to the relay", "node", pc.nodeID)
	_, _ = p.start(pc.nodeID, false)
}

// upgradeLoop retries a direct link for pc, a relay channel, while it stays
// the node's channel. On success the direct channel replaces pc, which closes
// once its pending calls settle.
func (p *peerPool) upgradeLoop(pc *peerChan) {
	for {
		timer := time.NewTimer(time.Duration(peerUpgradeIntervalNs.Load()))
		select {
		case <-timer.C:
		case <-pc.gone:
			timer.Stop()
			return
		case <-p.ctx.Done():
			timer.Stop()
			return
		}
		if !p.isCurrent(pc) {
			return
		}
		nd, _, err := p.target(pc.nodeID)
		if err != nil {
			continue
		}
		next := p.newChan(pc.nodeID, pc.uplink)
		if err := p.dialDirect(next, nd); err != nil {
			if !errors.Is(err, errNoDirect) {
				p.d.log.Debug("direct peer link unavailable; staying on the relay", "node", pc.nodeID, "err", err)
			}
			continue
		}
		close(next.ready)
		if !p.replace(pc, next) {
			p.remove(next)
			return
		}
		p.d.log.Info("peer channel moved to a direct link", "node", pc.nodeID)
		go p.retire(pc)
		p.holdDirect(next)
		return
	}
}

func (p *peerPool) isCurrent(pc *peerChan) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && p.byNode[pc.nodeID] == pc
}

// replace makes next the node's channel in place of old, only while old still
// is.
func (p *peerPool) replace(old, next *peerChan) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || next.removed || p.byNode[old.nodeID] != old {
		return false
	}
	p.byNode[old.nodeID] = next
	return true
}

// retire releases pc, a replaced channel, once its pending calls settle or
// peerRetireTimeout passes. Their replies still route to it meanwhile.
func (p *peerPool) retire(pc *peerChan) {
	deadline := time.NewTimer(peerRetireTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		p.mu.Lock()
		n := len(pc.pending)
		p.mu.Unlock()
		if n == 0 {
			break
		}
		select {
		case <-deadline.C:
			p.drop(pc)
			return
		case <-pc.gone:
			return
		case <-tick.C:
		}
	}
	p.drop(pc)
}

// reevaluate closes direct channels whose node no longer passes the checks an
// open runs, after a trust-store or quarantine change.
func (p *peerPool) reevaluate() {
	if p == nil {
		return
	}
	p.mu.Lock()
	var direct []*peerChan
	for _, pc := range p.byChan {
		if pc.via == viaTCP {
			direct = append(direct, pc)
		}
	}
	p.mu.Unlock()
	for _, pc := range direct {
		if _, _, err := p.target(pc.nodeID); err != nil {
			p.d.log.Warn("closing direct peer channel", "node", pc.nodeID, "err", err)
			p.drop(pc)
		}
	}
}
