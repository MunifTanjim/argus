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
// path: a change normally arrives via a trustlog.changed notification within
// milliseconds and triggers a pull. This timer only bounds how long a node can
// lag when no notification arrives, so shortening it is safe and lengthening it
// widens that window.
// Stored as nanoseconds in an atomic so SetTrustSyncIntervalForTest is race-free
// when background goroutines read it concurrently.
var trustSyncInterval atomic.Int64

func init() {
	trustSyncInterval.Store(int64(5 * time.Minute))
	triggeredPullIntervalNs.Store(int64(DefaultTriggeredPullInterval))
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
// against the sync loop.
func (d *Node) EnableTrustLog(genesisHash []byte, path string) error {
	d.pinMu.Lock()
	defer d.pinMu.Unlock()
	return d.enableTrustLogLocked(genesisHash, path)
}

// enableTrustLogLocked is EnableTrustLog for a caller that already holds pinMu.
// Precondition: pinMu is held.
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
	d.retainedEntries = trustlog.NewEntryStore() // fresh store for the new genesis
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

// SetPinSource records where the resolved genesis pin came from, for lock status.
func (d *Node) SetPinSource(s string) { d.pinSource = s }

// knownHashes lists every entry hash this node retains, for a sync offer. The
// entry store is the only source: an entry that was not retained is absent from
// the offer automatically, so the offer can never claim more than is held.
func (d *Node) knownHashes() (hashes [][]byte, truncated bool) {
	d.pinMu.Lock()
	re := d.retainedEntries
	d.pinMu.Unlock()
	if re == nil {
		return nil, false
	}
	return re.Hashes()
}

// syncTrustOnce runs one full sync cycle against peer. It is the loop's per-tick
// unit; it is a no-op while the store is unset.
func (d *Node) syncTrustOnce(peer trustCaller) {
	d.pullTrustOnce(peer)
}

// syncTrustChains exchanges known entry hashes with the gateway and returns the
// assembled chains it served. The gateway computes a set-subtraction delta from
// Known, so the node never needs to infer ancestry: every entry the node holds is
// listed explicitly. A non-empty Want means the gateway is behind; it is answered
// with only the specific entries named, so a node that locked the network while
// the gateway was restarting still publishes.
func (d *Node) syncTrustChains(peer trustCaller) ([][]byte, bool) {
	d.pinMu.Lock()
	if d.retainedEntries == nil {
		d.retainedEntries = trustlog.NewEntryStore()
	}
	re := d.retainedEntries
	d.pinMu.Unlock()
	// Compute own entries once: seed the store before knownHashes so the offer
	// always reflects locally-held entries (including after a restart), then
	// reuse for the merge. PutAll is idempotent for already-present entries.
	var ownEntries [][]byte
	if st := d.trust.Load(); st != nil {
		if mine := st.Bytes(); mine != nil {
			if raw, err := trustlog.ChainEntries(mine); err == nil {
				ownEntries = raw
			}
		}
	}
	re.PutAll(ownEntries)
	known, truncated := d.knownHashes()
	var got api.TrustLogSyncResult
	if err := peer.Call(api.MethodTrustLogSync, api.TrustLogSyncParams{Known: known, Truncated: truncated}, &got); err != nil {
		return nil, false
	}
	d.pinMu.Lock()
	prevDisjoint := d.lastDisjointLogged
	d.lastDisjointLogged = got.Disjoint
	d.pinMu.Unlock()
	if got.Disjoint && !prevDisjoint {
		d.log.Warn("trust log: this device shares no history with the network's; it is likely pinned to a different trust root")
	}

	merged := append([][]byte{}, got.Entries...)
	merged = append(merged, ownEntries...)
	if retained := re.All(); len(retained) > 0 {
		merged = append(merged, retained...)
	}
	chains, unplaced := trustlog.AssembleChainsReport(merged)
	d.pinMu.Lock()
	prevUnplaced := d.lastUnplacedLogged
	d.lastUnplacedLogged = unplaced
	d.pinMu.Unlock()
	if unplaced > 0 && unplaced != prevUnplaced {
		d.log.Warn("trust-log sync has unplaced entries; gateway may hold an incomplete branch", "unplaced", unplaced)
	}

	// Retain assembled chains' entries so the next offer reflects what is held.
	refused := 0
	for _, chain := range chains {
		raw, err := trustlog.ChainEntries(chain)
		if err != nil {
			continue
		}
		_, r := re.PutAll(raw)
		refused += r
	}
	if refused > 0 {
		d.log.Warn("trust-log entry store at ceiling; entries refused", "refused", refused)
	}

	if len(got.Want) > 0 {
		d.pushWanted(peer, got.Want)
	}
	return chains, true
}

// pushWanted publishes the specific entries the gateway asked for.
func (d *Node) pushWanted(peer trustCaller, want [][]byte) {
	d.pinMu.Lock()
	re := d.retainedEntries
	d.pinMu.Unlock()
	if re == nil {
		return
	}
	held := map[string][]byte{}
	for _, raw := range re.All() {
		e, err := trustlog.UnmarshalEntry(raw)
		if err != nil {
			continue
		}
		held[string(trustlog.HashEntry(&e))] = raw
	}
	var out [][]byte
	for _, h := range want {
		if raw, ok := held[string(h)]; ok {
			out = append(out, raw)
		}
	}
	if len(out) == 0 {
		return
	}
	_ = peer.Call(api.MethodTrustLogPush, api.TrustLogPushParams{Entries: out}, nil)
}

// pullTrustOnce runs one sync cycle over peer: exchange heads with the gateway,
// ingest each returned branch, and push our own if the gateway asked for it.
// The genesis-pinned store's fork-choice accepts the best valid branch; invalid
// or rolled-back branches are silently skipped. A single advance triggers persist.
// Returns false when there is no store or the sync failed.
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
		changed, err := st.Ingest(chain)
		if err != nil {
			continue // rollback/fork/tamper/wrong-genesis: skip this branch
		}
		if changed {
			anyChanged = true
		}
	}
	if anyChanged {
		if werr := d.persistTrust(); werr != nil {
			d.log.Warn("persisting trust-log chain failed", "path", d.trustPath, "err", werr)
		}
	}
	d.detectSupersedingChain(chains)
	d.reevaluateTrustChannels()
	return true
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
	// Guard under pinMu: a concurrent AdoptPin that set the store between the sync
	// and this point means the detection loop should not trip the gate for the old
	// genesis.
	d.pinMu.Lock()
	if d.trust.Load() != nil {
		d.pinMu.Unlock()
		return
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

// activateTrust persists the new store atomically, wires the pin, and triggers a
// trust-change announcement. Called from lock.init after building the genesis chain.
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
		d.retainedEntries = trustlog.NewEntryStore()
		d.trust.Store(store)
		d.pinGenesis = append([]byte(nil), genesisHash...)
		d.pinSource = trustpin.SourceFile.String()
		d.trustGate.Clear()
		return nil
	}(); err != nil {
		return err
	}
	d.reevaluateTrustChannels()
	d.announceTrustChange()
	return nil
}

// announceTrustChange pushes the current chain to the gateway after a local
// write (lock.init/sign/revoke/add-signer/remove-signer/disable).
func (d *Node) announceTrustChange() {
	d.offerTrustNow()
}

// offerTrustNow retains locally-appended chain entries and pushes them to the
// gateway out of band. Best effort: the sync loop is the backstop when there is
// no uplink.
func (d *Node) offerTrustNow() {
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
	d.pinMu.Lock()
	re := d.retainedEntries
	d.pinMu.Unlock()
	if re != nil {
		re.PutAll(raw)
	}
	peer := d.triggerPeer()
	if peer == nil {
		return
	}
	_ = peer.Call(api.MethodTrustLogPush, api.TrustLogPushParams{Entries: raw}, nil)
}

// runTrustSync drives the pull loop for the uplink's lifetime. It cancels the
// loop when the peer drops or ctx ends.
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

// runTrustSyncLoop pulls on connect and every trustSyncInterval until ctx ends or
// the uplink drops. It polls the (atomic) trust store each tick, so a node enabled
// live via lock.init begins syncing without a reconnect. syncTrustOnce is a no-op
// while the store is unset.
func (d *Node) runTrustSyncLoop(ctx context.Context, peer trustCaller) {
	d.syncTrustOnce(peer)
	t := time.NewTicker(time.Duration(trustSyncInterval.Load()))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.syncTrustOnce(peer)
		}
	}
}

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

// DefaultTriggeredPullInterval bounds how often a gateway notification can cause a
// pull, and so bounds how far one untrusted notification can amplify. It is
// deliberately small because the cost of being slow is an operator watching a peer
// lag behind a lock ceremony. Suppressed notifications coalesce into one deferred
// pull, so a peer reaches the current tip within this window regardless of how many
// writes landed inside it.
const DefaultTriggeredPullInterval = time.Second

// triggeredPullIntervalNs holds DefaultTriggeredPullInterval as nanoseconds in an
// atomic so setTriggeredPullIntervalForTest is race-free when background goroutines
// read it concurrently.
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

// ResetTriggeredPullIntervalForTest restores the production window, so callers do
// not each repeat the default. Test-only.
func ResetTriggeredPullIntervalForTest() {
	setTriggeredPullIntervalForTest(DefaultTriggeredPullInterval)
}

// onGatewayNotify handles gateway→node notifications. The only one is a hint that
// the trust log moved; everything else is ignored.
//
// The notification only schedules work — a forged one changes when the node pulls,
// not what it accepts (every branch is verified against the pinned genesis).
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
		hashes, _ := d.knownHashes()
		known := make(map[string]bool, len(hashes))
		for _, h := range hashes {
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
		d.pullTrustOnce(peer)
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

// setTriggerPeerForTest installs a trustCaller used by the event-driven pull in
// place of the live uplink. Test-only.
func (d *Node) setTriggerPeerForTest(p trustCaller) { d.testTriggerPeer.Store(&p) }

// AdoptPin pins this node to genesis at runtime: persist the pin, enable the trust
// store, release the quarantine gate, and pull the chain, so an operator recovers a
// quarantined node without a restart.
//
// Re-pinning the same genesis is a no-op; a different one is refused unless the
// current chain is disabled (stale pin that no longer guards anything).
func (d *Node) AdoptPin(genesis []byte) error {
	if len(genesis) != trustpin.GenesisLen {
		return fmt.Errorf("node: genesis is %d bytes, want %d", len(genesis), trustpin.GenesisLen)
	}
	if err := func() error {
		d.pinMu.Lock()
		defer d.pinMu.Unlock()
		if len(d.pinGenesis) > 0 {
			if bytes.Equal(d.pinGenesis, genesis) {
				return nil
			}
			// A disabled chain enforces nothing and can never be re-enabled, so the pin
			// holding it is stale rather than conflicting — replacing it is safe.
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
		return nil
	}(); err != nil {
		return err
	}
	d.reevaluateTrustChannels()
	if peer := d.triggerPeer(); peer != nil {
		d.pullTrustOnce(peer)
	}
	return nil
}

// DropPin clears the pin, the persisted chain, and the trust store. A node that
// held a chain quarantines immediately. DropPin never releases a quarantine and
// deliberately does not touch the local-disable marker.
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
		d.retainedEntries = nil
		if sawChain {
			// Only a chain we actually held proves this network is locked. Tripping
			// without that proof would strand a node whose network has no trust log.
			d.trustGate.Trip(lastGenesis)
		}
		return nil
	}(); err != nil {
		return err
	}
	d.reevaluateTrustChannels()
	return nil
}
