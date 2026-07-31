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

	"golang.org/x/crypto/blake2s"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/atomicfile"
	"github.com/MunifTanjim/argus/internal/trustlog"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

// trustSyncInterval is how often a connected node re-runs the offer/pull cycle.
// Chain updates are rare, so this is a lazy convergence tick, not a hot loop.
// Stored as nanoseconds in an atomic so SetTrustSyncIntervalForTest is race-free
// when background goroutines read it concurrently.
var trustSyncInterval atomic.Int64

func init() { trustSyncInterval.Store(int64(30 * time.Second)) }

// SetTrustSyncIntervalForTest overrides the node's trust-log sync cadence. Test-only.
func SetTrustSyncIntervalForTest(d time.Duration) { trustSyncInterval.Store(int64(d)) }

// trustCaller is the subset of *api.Peer runTrustSync needs; an interface so tests
// can substitute a fake uplink.
type trustCaller interface {
	Call(method string, params, out any) error
}

// EnableTrustLog turns on locked-mode trust-log participation: it pins genesisHash
// and loads any chain already persisted at path (rollback resistance across
// reboots — a restarted node resumes from its last verified tip). Call before
// ConnectGateway. A malformed/rolled-back on-disk chain is ignored (the store
// stays empty rather than adopting it); genuine corruption surfaces on next sync.
func (d *Node) EnableTrustLog(genesisHash []byte, path string) error {
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
	d.seenBranches = nil // new store, new genesis — stale fingerprints are invalid
	d.trust.Store(sync)
	return nil
}

// TrustStore returns the node's trust-log store, or nil when locked mode is off.
func (d *Node) TrustStore() *trustlog.SyncStore { return d.trust.Load() }

// SetTrustChainPath records where lock.init should persist the chain, without
// enabling locked mode. Call at boot so a later live lock.init has a target path.
func (d *Node) SetTrustChainPath(path string) { d.trustPath = path }

// branchFingerprint is the content hash the gateway keys branches by. It must match
// gateway.chainKey exactly or every pull re-downloads.
func branchFingerprint(chain []byte) [32]byte { return blake2s.Sum256(chain) }

// knownFingerprints returns the fingerprints of branches already received, for the
// pull request. Guarded by pinMu alongside the rest of the trust decision state.
func (d *Node) knownFingerprints() [][]byte {
	d.pinMu.Lock()
	defer d.pinMu.Unlock()
	out := make([][]byte, 0, len(d.seenBranches))
	for k := range d.seenBranches {
		fp := k
		out = append(out, append([]byte(nil), fp[:]...))
	}
	return out
}

// rememberBranch records a fingerprint as received. Branches that fail to verify are
// recorded too: for the current pin, identical bytes can never become valid later,
// and re-fetching them forever is the waste this removes.
func (d *Node) rememberBranch(chain []byte) {
	fp := branchFingerprint(chain)
	d.pinMu.Lock()
	defer d.pinMu.Unlock()
	if d.seenBranches == nil {
		d.seenBranches = map[[32]byte]bool{}
	}
	d.seenBranches[fp] = true
}

func containsFingerprint(list [][]byte, want [32]byte) bool {
	for _, f := range list {
		if len(f) == 32 && bytes.Equal(f, want[:]) {
			return true
		}
	}
	return false
}

// syncTrustOnce runs one offer/pull cycle over peer: publish our current chain
// (if any), then pull all retained gateway branches and ingest each in order.
// The genesis-pinned store's fork-choice accepts the best valid branch; invalid
// or rolled-back branches are silently skipped. A single advance triggers persist.
func (d *Node) syncTrustOnce(peer trustCaller) {
	st := d.trust.Load()
	if st == nil {
		d.detectUnpinnedChain(peer)
		return
	}
	var got api.TrustLogPullResult
	if err := peer.Call(api.MethodTrustLogPull, api.TrustLogPullParams{Known: d.knownFingerprints()}, &got); err != nil {
		return
	}
	anyChanged := false
	for _, chain := range got.Chains {
		d.rememberBranch(chain)
		changed, err := st.Ingest(chain)
		if err != nil {
			continue // rollback/fork/tamper/wrong-genesis: skip this branch
		}
		if changed {
			anyChanged = true
		}
	}
	// Offer only when the gateway does not list our chain's fingerprint.
	// containsFingerprint returns false for a nil slice, so a legacy gateway
	// (Fingerprints == nil) is treated as "does not hold our branch" and receives
	// an unconditional offer. Record our own fingerprint after offering so the
	// gateway cannot echo it back on the next pull.
	if mine := st.Bytes(); mine != nil && !containsFingerprint(got.Fingerprints, branchFingerprint(mine)) {
		_ = peer.Call(api.MethodTrustLogOffer, api.TrustLogChain{Chain: mine}, nil)
		d.rememberBranch(mine)
	}
	if anyChanged {
		if werr := d.persistTrust(); werr != nil {
			d.log.Warn("persisting trust-log chain failed", "path", d.trustPath, "err", werr)
		}
		d.reevaluateTrustChannels()
		// Emit a fresh beacon directly over the sync peer so the gateway sees the
		// updated tip immediately (without waiting for a reconnect/identify).
		if len(d.beacon.Private) > 0 {
			if b, err := d.makeBeacon(); err == nil {
				_ = peer.Call(api.MethodBeaconOffer, b, nil)
			}
		}
	}
	// Cross-check stored peer beacons against the resolved chain on every tick
	// (regardless of whether the chain advanced) so the N=2 persistence guard
	// accumulates correctly.
	d.checkPeerBeaconConsistency()
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
// Roster sync (for peer beacon attribution) runs once at startup and every
// rosterSyncEvery trust ticks, so peerBeaconPubs stays current without adding
// an extra RPC to every tight-interval test tick.
func (d *Node) runTrustSyncLoop(ctx context.Context, peer trustCaller) {
	// Populate roster-known beacon pubs before the first trust sync so that
	// any beacon.deliver calls arriving immediately after connect are attributed.
	d.syncRoster(peer)
	d.syncTrustOnce(peer)
	t := time.NewTicker(time.Duration(trustSyncInterval.Load()))
	defer t.Stop()
	ticks := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ticks++
			d.syncTrustOnce(peer)
			if ticks%rosterSyncEvery == 0 {
				d.syncRoster(peer)
			}
		}
	}
}

// rosterSyncEvery controls how often the roster is re-fetched relative to the
// trust-sync tick. 10 means one nodes.list per 10 trust ticks (e.g. once per
// 5 minutes at the default 30 s interval). A reconnect always refreshes.
const rosterSyncEvery = 10

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
	d.reevaluateTrustChannels()
	d.emitBeacon() // announce the new chain tip to the gateway
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
	var got api.TrustLogPullResult
	if err := peer.Call(api.MethodTrustLogPull, api.TrustLogPullParams{Known: d.knownFingerprints()}, &got); err != nil {
		return
	}
	// Record all fingerprints first: the detection loop returns after the first
	// decodable chain, so branches after that one would otherwise be missed.
	for _, chain := range got.Chains {
		d.rememberBranch(chain)
	}
	for _, chain := range got.Chains {
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

// AdoptPin pins this node to genesis at runtime: persist the pin, enable the
// trust store, and release the quarantine gate. The next sync tick ingests the
// chain, so an operator recovers a quarantined node without a restart.
// Re-pinning the same genesis is a no-op; a different one is refused, because
// silently switching trust roots is exactly what the pin exists to prevent.
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
			return errors.New("node: already pinned to a different genesis; run `argus lock unpin` first")
		}
		if d.trustPath == "" {
			return errors.New("node: trust state path not configured")
		}
		if err := trustpin.New(genesisHashPath(d.trustPath)).Save(genesis); err != nil {
			return err
		}
		if err := d.EnableTrustLog(genesis, d.trustPath); err != nil {
			return err
		}
		d.pinSource = trustpin.SourceFile.String()
		d.trustGate.Clear()
		return nil
	}(); err != nil {
		return err
	}
	d.reevaluateTrustChannels()
	return nil
}

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
		d.seenBranches = nil // stale fingerprints must not suppress the re-fill after re-pin
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
