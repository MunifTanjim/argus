package node

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/atomicfile"
	"github.com/MunifTanjim/argus/internal/trustlog"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

// trustSyncInterval is the BACKSTOP for trust-log convergence, not the primary
// path: a change normally arrives via trustlog.changed (node) or NodeEventBeacon
// (client) within milliseconds. It also bounds how long an UNPINNED device stays
// open on a locked network before quarantining, so shortening it is safe and
// lengthening it widens that window. Do not tune this without reading
// detectUnpinnedChain.
// Stored as nanoseconds in an atomic so SetTrustSyncIntervalForTest is race-free
// when background goroutines read it concurrently.
var trustSyncInterval atomic.Int64

func init() {
	trustSyncInterval.Store(int64(5 * time.Minute))
	rosterSyncIntervalNs.Store(int64(5 * time.Minute))
	triggeredPullIntervalNs.Store(int64(5 * time.Second))
}

// SetTrustSyncIntervalForTest overrides the node's trust-log sync cadence. Test-only.
func SetTrustSyncIntervalForTest(d time.Duration) { trustSyncInterval.Store(int64(d)) }

// trustCaller is the subset of *api.Peer runTrustSync needs; an interface so tests
// can substitute a fake uplink.
type trustCaller interface {
	Call(method string, params, out any) error
}

// EnableTrustLog turns on locked-mode trust-log participation: it pins genesisHash
// and loads any chain already persisted at path (rollback resistance across
// reboots — a restarted node resumes from its last verified tip). A
// malformed/rolled-back on-disk chain is ignored (the store stays empty rather than
// adopting it); genuine corruption surfaces on next sync.
//
// Safe on a live, gateway-connected node: pinMu serializes the pin state it writes
// against the sync loop, which reads seenBranches on every pull.
func (d *Node) EnableTrustLog(genesisHash []byte, path string) error {
	d.pinMu.Lock()
	defer d.pinMu.Unlock()
	return d.enableTrustLogLocked(genesisHash, path)
}

// enableTrustLogLocked is EnableTrustLog for a caller that already holds pinMu,
// because enabling the store is one step of a larger pin decision that must not be
// interleaved (AdoptPin). Precondition: pinMu is held.
func (d *Node) enableTrustLogLocked(genesisHash []byte, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sync := trustlog.NewSyncStore(genesisHash)
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		// A persisted chain we wrote ourselves; ingest is genesis-pinned so a
		// tampered file is rejected rather than trusted.
		if _, ierr := sync.Ingest(b); ierr != nil {
			d.log.Warn("ignoring unusable persisted trust-log chain", "path", path, "err", ierr)
		}
	}
	d.trustPath = path
	d.pinGenesis = append([]byte(nil), genesisHash...)
	d.seenBranches = nil      // new store, new genesis — stale fingerprints are invalid
	d.retainedEntries = trustlog.NewEntryStore() // same lifetime as seenBranches
	d.trust.Store(sync)
	if bytes.Equal(d.trustGate.Genesis(), genesisHash) {
		d.trustGate.Clear()
	}
	return nil
}

// TrustStore returns the node's trust-log store, or nil when locked mode is off.
func (d *Node) TrustStore() *trustlog.SyncStore { return d.trust.Load() }

// SetTrustChainPath records where lock.init should persist the chain, without
// enabling locked mode. Call at boot so a later live lock.init has a target path.
func (d *Node) SetTrustChainPath(path string) { d.trustPath = path }

// branchHead is the hash of a chain's last entry — the identity the gateway keys
// branches by now. It must match the head the gateway derives or every sync
// re-downloads.
func branchHead(chain []byte) ([32]byte, bool) {
	var out [32]byte
	entries, err := trustlog.UnmarshalChain(chain)
	if err != nil || len(entries) == 0 {
		return out, false
	}
	copy(out[:], trustlog.HashEntry(&entries[len(entries)-1]))
	return out, true
}

// knownHeads returns the head hash of every branch already received, for the sync
// request. Guarded by pinMu alongside the rest of the trust decision state.
func (d *Node) knownHeads() [][]byte {
	d.pinMu.Lock()
	defer d.pinMu.Unlock()
	out := make([][]byte, 0, len(d.seenBranches))
	for k := range d.seenBranches {
		h := k
		out = append(out, append([]byte(nil), h[:]...))
	}
	return out
}

// rememberHead records a branch as received. Branches that fail to verify are
// recorded too: for the current pin, identical bytes can never become valid later,
// and re-fetching them forever is the waste this removes.
func (d *Node) rememberHead(chain []byte) {
	d.pinMu.Lock()
	defer d.pinMu.Unlock()
	d.rememberHeadLocked(chain)
}

// rememberHeadLocked is rememberHead for a caller already holding pinMu, because
// recording a head is only correct together with the store state it was read
// against. Precondition: pinMu is held.
func (d *Node) rememberHeadLocked(chain []byte) {
	h, ok := branchHead(chain)
	if !ok {
		return
	}
	if d.seenBranches == nil {
		d.seenBranches = map[[32]byte]bool{}
	}
	d.seenBranches[h] = true
	if d.retainedEntries != nil {
		if raw, err := trustlog.ChainEntries(chain); err == nil {
			d.retainedEntries.PutAll(raw)
		}
	}
}

// syncTrustOnce is pullTrustOnce plus the periodic peer-beacon cross-check. Only
// the node's own timer may call it: the N=2 equivocation guard counts consecutive
// ticks, so anything an untrusted party can provoke must use pullTrustOnce instead.
func (d *Node) syncTrustOnce(peer trustCaller) {
	if !d.pullTrustOnce(peer) {
		return
	}
	// Cross-check stored peer beacons against the resolved chain on every tick
	// (regardless of whether the chain advanced) so the N=2 persistence guard
	// accumulates correctly.
	d.checkPeerBeaconConsistency()
}

// syncTrustChains exchanges heads with the gateway and returns the branches it
// served, assembled back into chains. The gateway sends only entries this node
// cannot reach from its heads, so its delta is merged with the entries of the
// branches already held before assembling — a chain missing its ancestors cannot
// be verified. A non-empty Want means the gateway is behind: it is answered with
// the ancestry it asked for, so a node that locked the network while the gateway
// was restarting still publishes.
func (d *Node) syncTrustChains(peer trustCaller) ([][]byte, bool) {
	heads := d.knownHeads()
	var got api.TrustLogSyncResult
	if err := peer.Call(api.MethodTrustLogSync, api.TrustLogSyncParams{Heads: heads}, &got); err != nil {
		return nil, false
	}

	merged := append([][]byte{}, got.Entries...)
	if st := d.trust.Load(); st != nil {
		if mine := st.Bytes(); mine != nil {
			if raw, err := trustlog.ChainEntries(mine); err == nil {
				merged = append(merged, raw...)
			}
		}
	}
	// Merge retained entries from non-winning branches so we can assemble
	// chains whose ancestors the gateway withheld (it assumes we hold them
	// because we advertised the head).
	d.pinMu.Lock()
	re := d.retainedEntries
	d.pinMu.Unlock()
	if re != nil {
		if retained, _ := re.Delta(nil); len(retained) > 0 {
			merged = append(merged, retained...)
		}
	}
	chains, unplaced := trustlog.AssembleChainsReport(merged)
	if unplaced > 0 {
		d.log.Warn("trust-log sync has unplaced entries; gateway may hold an incomplete branch", "unplaced", unplaced)
	}

	if len(got.Want) > 0 {
		d.pushHeldEntries(peer)
	}
	return chains, true
}

// pushHeldEntries publishes this node's own branch to a gateway that asked for it.
func (d *Node) pushHeldEntries(peer trustCaller) {
	st := d.trust.Load()
	if st == nil {
		return
	}
	mine := st.Bytes()
	if mine == nil {
		return
	}
	raw, err := trustlog.ChainEntries(mine)
	if err != nil {
		return
	}
	if err := peer.Call(api.MethodTrustLogPush, api.TrustLogPushParams{Entries: raw}, nil); err != nil {
		return
	}
	d.rememberHead(mine)
}

// pullTrustOnce runs one sync cycle over peer: exchange heads with the gateway,
// ingest each returned branch, and push our own if the gateway asked for it.
// The genesis-pinned store's fork-choice accepts the best valid branch; invalid
// or rolled-back branches are silently skipped. A single advance triggers persist.
// Returns false when there was nothing to reconcile against — no store (the
// unpinned detection path ran instead) or a failed sync.
func (d *Node) pullTrustOnce(peer trustCaller) bool {
	st := d.trust.Load()
	if st == nil {
		d.detectUnpinnedChain(peer)
		return false
	}
	chains, ok := d.syncTrustChains(peer)
	if !ok {
		return false
	}
	anyChanged := false
	for _, chain := range chains {
		d.rememberHead(chain)
		changed, err := st.Ingest(chain)
		if err != nil {
			continue // rollback/fork/tamper/wrong-genesis: skip this branch
		}
		if changed {
			anyChanged = true
		}
	}
	d.detectSupersedingChain(chains)
	if anyChanged {
		if werr := d.persistTrust(); werr != nil {
			d.log.Warn("persisting trust-log chain failed", "path", d.trustPath, "err", werr)
		}
		d.reevaluateTrustChannels()
		if len(d.beacon.Private) > 0 {
			if b, err := d.makeBeacon(); err == nil {
				_ = peer.Call(api.MethodBeaconOffer, b, nil)
			}
		}
	}
	return true
}

// persistChain writes chain bytes to trustPath atomically via atomicfile.Write.
// A dedicated mutex ensures two goroutines (e.g. lingering + new uplink) never
// race the rename.
func (d *Node) persistChain(chain []byte) error {
	d.trustPersistMu.Lock()
	defer d.trustPersistMu.Unlock()
	return atomicfile.Write(d.trustPath, chain)
}

// persistTrust writes the current chain to disk. It is a no-op when the store
// is unset. For the atomic write mechanics see persistChain.
func (d *Node) persistTrust() error {
	st := d.trust.Load()
	if st == nil {
		return nil
	}
	return d.persistChain(st.Bytes())
}

// announceTrustChange publishes a chain this node just advanced locally (lock.init,
// sign, revoke, disable): the chain first, then the beacon, so a device reacting to
// the beacon's new tip finds the chain that explains it already retained by the
// gateway. Without the offer, nothing leaves this node until the next sync tick —
// trustSyncInterval away — and the rest of the network cannot even see that the
// network is locked, let alone pin to it.
func (d *Node) announceTrustChange() {
	d.offerTrustNow()
	d.emitBeacon()
}

// offerTrustNow pushes the current branch's entries to the gateway out of band.
// Entries the gateway already holds dedupe by hash, so this costs only what is
// genuinely new. Best effort: the sync loop remains the backstop when there is no
// uplink.
func (d *Node) offerTrustNow() {
	st := d.trust.Load()
	if st == nil {
		return
	}
	peer := d.triggerPeer()
	if peer == nil {
		return
	}
	d.pushHeldEntries(peer)
}

// runTrustSync drives the offer/pull loop for the uplink's lifetime. It
// cancels the loop when the peer drops or ctx ends.
func (d *Node) runTrustSync(ctx context.Context, peer *api.Peer) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-peer.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	d.runTrustSyncLoop(ctx, peer)
}

// runTrustSyncLoop offers+pulls on connect and every trustSyncInterval until ctx
// ends or the uplink drops. It polls the (atomic) trust store each tick, so a node
// enabled live via lock.init begins syncing without a reconnect. syncTrustOnce is a
// no-op while the store is unset.
//
// Roster sync (for peer beacon attribution) runs on its own rosterSyncInterval
// clock, not as a multiple of the trust tick: an unattributed peer's beacons are
// rejected outright, so that latency must not follow whatever the trust backstop
// is tuned to, and it must not scale down with a tight-interval test tick either.
func (d *Node) runTrustSyncLoop(ctx context.Context, peer trustCaller) {
	// Populate roster-known beacon pubs before the first trust sync so that
	// any beacon.deliver calls arriving immediately after connect are attributed.
	d.syncRoster(peer)
	d.syncTrustOnce(peer)
	t := time.NewTicker(time.Duration(trustSyncInterval.Load()))
	defer t.Stop()
	rt := time.NewTicker(rosterSyncInterval())
	defer rt.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.syncTrustOnce(peer)
		case <-rt.C:
			d.syncRoster(peer)
		}
	}
}

// rosterSyncIntervalNs is how often peer beacon attribution is refreshed. A node
// that joined after the last sync has every beacon rejected as "unknown beacon
// pub" until the next one, so this bounds how long a new node stays outside the
// anti-equivocation cross-check. A reconnect always refreshes.
// Stored as nanoseconds in an atomic so setRosterSyncIntervalForTest is race-free
// when the sync loop reads it concurrently.
var rosterSyncIntervalNs atomic.Int64

func rosterSyncInterval() time.Duration { return time.Duration(rosterSyncIntervalNs.Load()) }

// setRosterSyncIntervalForTest overrides the roster refresh cadence. Test-only.
func setRosterSyncIntervalForTest(d time.Duration) { rosterSyncIntervalNs.Store(int64(d)) }

// genesisHashPath is the state file holding the pinned genesis hash, kept beside
// the chain so a node's locked state is self-contained in its state dir.
func genesisHashPath(chainPath string) string {
	return filepath.Join(filepath.Dir(chainPath), "trustlog-genesis")
}

// writeGenesisHash atomically persists the pinned genesis hash beside the chain.
func (d *Node) writeGenesisHash(hash []byte) error {
	d.trustPersistMu.Lock()
	defer d.trustPersistMu.Unlock()
	return trustpin.New(genesisHashPath(d.trustPath)).Save(hash)
}

// activateTrust enables locked mode at runtime (lock.init): pin path, persist the
// chain + genesis hash, then publish the store atomically. The per-uplink sync loop
// (polling the atomic store) then offers it to the gateway without a reconnect.
// Persisting before Store ensures the node is either fully persisted+enabled or
// error+not-enabled; it is never enabled-but-unpersisted.
func (d *Node) activateTrust(store *trustlog.SyncStore, genesisHash []byte, chainPath string) error {
	if err := func() error {
		d.pinMu.Lock()
		defer d.pinMu.Unlock()
		d.trustPath = chainPath
		if err := os.MkdirAll(filepath.Dir(chainPath), 0o700); err != nil {
			return err
		}
		if err := d.persistChain(store.Bytes()); err != nil {
			return err
		}
		if err := d.writeGenesisHash(genesisHash); err != nil {
			return err
		}
		d.seenBranches = nil      // new store, new genesis — stale fingerprints are invalid
		d.retainedEntries = trustlog.NewEntryStore() // same lifetime as seenBranches
		d.trust.Store(store) // publish only after both persists succeed
		// The node that runs lock.init is the network's first trust anchor: its own
		// `lock status` is what every other device compares its fingerprint against,
		// so the pin has to be visible immediately, not after a restart.
		d.pinGenesis = append([]byte(nil), genesisHash...)
		d.pinSource = trustpin.SourceFile.String()
		d.trustGate.Clear()
		return nil
	}(); err != nil {
		return err
	}
	d.resetPeerBeaconState()
	d.reevaluateTrustChannels()
	d.announceTrustChange()
	return nil
}

// detectUnpinnedChain quarantines this node when the network has a trust log but
// this node holds no pin to verify it against. The chain is only decoded, never
// verified: anyone can mint a keypair and build a self-consistent chain, so
// verification would prove nothing about who authored it.
//
// A hostile gateway can therefore quarantine unpinned nodes with a fabricated
// chain. It can already refuse to relay at all, so this grants it no new power.
func (d *Node) detectUnpinnedChain(peer trustCaller) {
	if d.trustGate.Tripped() {
		return
	}
	chains, ok := d.syncTrustChains(peer)
	if !ok {
		return
	}
	// Record all heads first: the detection loop returns after the first decodable
	// chain, so branches after that one would otherwise be missed.
	// Under pinMu, and only while the store is still unset: a pin that landed during
	// the sync just cleared seenBranches for its new store, and marking these chains
	// seen would tell the gateway to withhold the very branches that store still
	// needs — stranding it empty until some other branch appears.
	d.pinMu.Lock()
	if d.trust.Load() != nil {
		d.pinMu.Unlock()
		return
	}
	for _, chain := range chains {
		d.rememberHeadLocked(chain)
	}
	d.pinMu.Unlock()
	for _, chain := range chains {
		entries, err := trustlog.UnmarshalChain(chain)
		if err != nil || len(entries) == 0 {
			continue
		}
		genesis := trustlog.HashEntry(&entries[0])
		// pinMu serializes the "store is nil → trip" decision against AdoptPin's
		// "EnableTrustLog → Clear" sequence, closing the window where a concurrent
		// adopt could have set the store and cleared the gate between our store-read
		// above and this Trip call.
		d.pinMu.Lock()
		if d.trust.Load() == nil {
			d.trustGate.Trip(genesis)
			d.pinMu.Unlock()
			d.log.Warn("unpinned node saw a trust log; refusing all channels until pinned",
				"genesis", base64.StdEncoding.EncodeToString(genesis),
				"fix", "argus lock pin")
			d.reevaluateTrustChannels()
		} else {
			d.pinMu.Unlock()
		}
		return
	}
}

// detectSupersedingChain quarantines a node whose own chain is disabled once the
// network serves a different trust root. A disabled log authorizes nobody and can
// never be re-enabled, so the pin holding it protects nothing while still refusing
// the live root — the same rootless state detectUnpinnedChain exists for, reached
// from the other direction. Decode only, for the reason given there.
func (d *Node) detectSupersedingChain(chains [][]byte) {
	if st := d.trust.Load(); st == nil || !st.Disabled() {
		return
	}
	d.pinMu.Lock()
	st := d.trust.Load()
	if st == nil || !st.Disabled() {
		d.pinMu.Unlock()
		return
	}
	genesis := trustlog.SupersedingGenesis(chains, d.pinGenesis)
	if genesis == nil || bytes.Equal(genesis, d.trustGate.Genesis()) {
		d.pinMu.Unlock()
		return
	}
	// Observe, not Trip: the network can relock more than once, and each time the
	// root this device must be told to adopt changes. A first-sighting-wins gate
	// would keep naming a root that no longer exists.
	d.trustGate.Observe(genesis)
	d.pinMu.Unlock()
	d.log.Warn("this device's trust log is disabled and the network moved to a different root; refusing all channels until pinned",
		"genesis", base64.StdEncoding.EncodeToString(genesis),
		"fix", "argus lock pin")
	d.reevaluateTrustChannels()
}

// AdoptPin pins this node to genesis at runtime: persist the pin, enable the
// trust store, release the quarantine gate, and pull the chain, so an operator
// recovers a quarantined node without a restart. The pull is not deferred to the
// next tick: until the chain lands the store is empty, which authorizes nobody, so
// a deferred pull would trade the quarantine for a blackout of the same length.
// Re-pinning the same genesis is a no-op; a different one is refused, because
// silently switching trust roots is exactly what the pin exists to prevent — unless
// the chain we hold is disabled, which makes the pin stale rather than a trust root
// worth defending.
func (d *Node) AdoptPin(genesis []byte) error {
	if len(genesis) != trustpin.GenesisLen {
		return fmt.Errorf("node: genesis is %d bytes, want %d", len(genesis), trustpin.GenesisLen)
	}
	adopted := false
	if err := func() error {
		d.pinMu.Lock()
		defer d.pinMu.Unlock()
		if len(d.pinGenesis) > 0 {
			if bytes.Equal(d.pinGenesis, genesis) {
				return nil
			}
			// A disabled chain enforces nothing and can never be re-enabled, so the pin
			// holding it is stale rather than conflicting — replacing it is the same
			// decision `lock pin` makes on a device that never had one. The dead chain
			// goes with it: it can never ingest under the new root.
			if st := d.trust.Load(); st == nil || !st.Disabled() {
				return errors.New("node: already pinned to a different genesis; run `argus lock unpin` first")
			}
			if err := os.Remove(d.trustPath); err != nil && !os.IsNotExist(err) {
				return err
			}
			d.pinGenesis = nil
		}
		if d.trustPath == "" {
			return errors.New("node: trust state path not configured")
		}
		if err := trustpin.New(genesisHashPath(d.trustPath)).Save(genesis); err != nil {
			return err
		}
		if err := d.enableTrustLogLocked(genesis, d.trustPath); err != nil {
			return err
		}
		d.pinSource = trustpin.SourceFile.String()
		d.trustGate.Clear()
		adopted = true
		return nil
	}(); err != nil {
		return err
	}
	if adopted {
		d.resetPeerBeaconState()
	}
	d.reevaluateTrustChannels()
	if peer := d.triggerPeer(); peer != nil {
		d.pullTrustOnce(peer)
	}
	return nil
}

// triggeredPullIntervalNs bounds how often a gateway notification can cause a pull.
// Without it, a hostile gateway turns one notification into unbounded work — the
// notification is untrusted, so it must not be able to amplify. Suppressed
// notifications are coalesced into one deferred pull, never queued, so the bound
// holds under a flood.
// Stored as nanoseconds in an atomic so setTriggeredPullIntervalForTest is race-free
// when background goroutines read it concurrently.
var triggeredPullIntervalNs atomic.Int64

func minTriggeredPullInterval() time.Duration {
	return time.Duration(triggeredPullIntervalNs.Load())
}

// setTriggeredPullIntervalForTest overrides the notification rate-limit window so a
// test can observe deferral without waiting out the production window. Test-only.
func setTriggeredPullIntervalForTest(d time.Duration) { triggeredPullIntervalNs.Store(int64(d)) }

// SetTriggeredPullIntervalForTest is setTriggeredPullIntervalForTest for integration
// tests in other packages, which drive several notification-triggered pulls in a row
// and would otherwise spend the production window between each. Test-only.
func SetTriggeredPullIntervalForTest(d time.Duration) { setTriggeredPullIntervalForTest(d) }

// onGatewayNotify handles gateway→node notifications. The only one is a hint that
// the trust log moved; everything else is ignored.
//
// The notification only schedules work — it never advances the equivocation state
// machine, so a forged one changes when the node pulls, not what it concludes.
func (d *Node) onGatewayNotify(n api.Notification) {
	if n.Method != api.MethodTrustLogChanged {
		return
	}
	// Resolve the peer first: nothing to schedule when there is nothing to pull
	// against (e.g. during a reconnect gap).
	if d.triggerPeer() == nil {
		return
	}
	if p, err := api.Decode[api.TrustLogChangedParams](n.Params); err == nil && len(p.Heads) > 0 {
		known := map[string]bool{}
		for _, h := range d.knownHeads() {
			known[string(h)] = true
		}
		fresh := false
		for _, h := range p.Heads {
			if !known[string(h)] {
				fresh = true
				break
			}
		}
		if !fresh {
			return
		}
	}
	d.trustPullTrigger.request(minTriggeredPullInterval(), func() {
		// The peer is resolved at run time, not at notification time: a deferred pull
		// must use whichever uplink is live when it fires.
		peer := d.triggerPeer()
		if peer == nil {
			return
		}
		// pullTrustOnce, not syncTrustOnce: a gateway-driven pull must not clock the
		// consistency check, or the gateway could drive peerBeaconMiss to its
		// threshold at whatever rate it chooses to notify.
		d.pullTrustOnce(peer)
	})
}

// requestRosterSync refreshes peer beacon attribution out of band, for when a
// courier delivers a beacon signed by a key this node cannot place — normally a
// peer that joined since the last roster tick. Rate-limited because the trigger
// comes from an untrusted client.
func (d *Node) requestRosterSync() {
	if d.triggerPeer() == nil {
		return
	}
	d.rosterTrigger.request(minTriggeredPullInterval(), func() {
		if peer := d.triggerPeer(); peer != nil {
			d.syncRoster(peer)
		}
	})
}

// triggerPeer returns the peer an event-triggered RPC should use: the test
// override when set, otherwise the live gateway uplink. Returns nil (not a typed
// nil) when no peer is available, so callers can guard with peer != nil.
func (d *Node) triggerPeer() trustCaller {
	if tp := d.testTriggerPeer.Load(); tp != nil {
		return *tp
	}
	if p := d.activeUplink.Load(); p != nil {
		return p
	}
	return nil
}

// setTriggerPeerForTest installs a trustCaller used by the event-driven pull and
// roster refresh in place of the live uplink. Test-only.
func (d *Node) setTriggerPeerForTest(p trustCaller) { d.testTriggerPeer.Store(&p) }

// DropPin clears the pin, the persisted chain, and the trust store. A node that
// held a chain quarantines immediately — waiting for the next detection tick would
// leave it open to any key the gateway introduces for up to one sync interval,
// which is exactly the window the documented `unpin` + `pin` rotation walks
// through. Live channels are dropped for the same reason. DropPin never releases a
// quarantine and deliberately does not touch the local-disable marker.
func (d *Node) DropPin() error {
	if err := func() error {
		d.pinMu.Lock()
		defer d.pinMu.Unlock()
		if d.trustPath == "" {
			return errors.New("node: trust state path not configured")
		}
		if err := trustpin.New(genesisHashPath(d.trustPath)).Clear(); err != nil {
			return err
		}
		if err := os.Remove(d.trustPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		st := d.trust.Load()
		sawChain := st != nil && st.Bytes() != nil
		lastGenesis := d.pinGenesis
		d.trust.Store(nil)
		d.pinGenesis = nil
		d.pinSource = ""
		d.seenBranches = nil    // stale fingerprints must not suppress the re-fill after re-pin
		d.retainedEntries = nil // same lifetime as seenBranches
		if sawChain {
			// Only a chain we actually held proves this network is locked. Tripping
			// without that proof would strand a node whose network has no trust log:
			// `lock pin` would find nothing to pin and the gate would never clear.
			d.trustGate.Trip(lastGenesis)
		}
		return nil
	}(); err != nil {
		return err
	}
	d.reevaluateTrustChannels()
	return nil
}
