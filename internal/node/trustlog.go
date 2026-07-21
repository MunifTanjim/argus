package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
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

// EnableTrustLog turns on locked-mode trust-log participation: it pins genesisHead
// and loads any chain already persisted at path (rollback resistance across
// reboots — a restarted node resumes from its last verified HEAD). Call before
// ConnectGateway. A malformed/rolled-back on-disk chain is ignored (the store
// stays empty rather than adopting it); genuine corruption surfaces on next sync.
func (d *Node) EnableTrustLog(genesisHead []byte, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sync := trustlog.NewSyncStore(genesisHead)
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		// A persisted chain we wrote ourselves; ingest is genesis-pinned so a
		// tampered file is rejected rather than trusted.
		if _, ierr := sync.Ingest(b); ierr != nil {
			d.log.Warn("ignoring unusable persisted trust-log chain", "path", path, "err", ierr)
		}
	}
	d.trustPath = path
	d.trust.Store(sync)
	return nil
}

// TrustStore returns the node's trust-log store, or nil when locked mode is off.
func (d *Node) TrustStore() *trustlog.SyncStore { return d.trust.Load() }

// SetTrustChainPath records where lock.init should persist the chain, without
// enabling locked mode. Call at boot so a later live lock.init has a target path.
func (d *Node) SetTrustChainPath(path string) { d.trustPath = path }

// syncTrustOnce runs one offer/pull cycle over peer: publish our current chain
// (if any), then pull the gateway's and ingest it, persisting on any advance.
func (d *Node) syncTrustOnce(peer trustCaller) {
	st := d.trust.Load()
	if st == nil {
		return
	}
	if mine := st.Bytes(); mine != nil {
		_ = peer.Call(api.MethodTrustLogOffer, api.TrustLogChain{Chain: mine}, nil)
	}
	var got api.TrustLogChain
	if err := peer.Call(api.MethodTrustLogPull, nil, &got); err != nil || len(got.Chain) == 0 {
		return
	}
	changed, err := st.Ingest(got.Chain)
	if err != nil {
		return // rollback/fork/tamper/wrong-genesis: keep our chain
	}
	if changed {
		if werr := d.persistTrust(); werr != nil {
			d.log.Warn("persisting trust-log chain failed", "path", d.trustPath, "err", werr)
		}
	}
}

// persistChain writes chain bytes to trustPath atomically: it creates a temp
// file in the same directory, writes the bytes, then renames it over trustPath.
// Rename within a directory is atomic on POSIX, so readers and boot always see
// either the old or the new file, never a truncated one. A dedicated mutex
// ensures two goroutines (e.g. lingering + new uplink) never race the temp file
// or the rename.
func (d *Node) persistChain(chain []byte) error {
	d.trustPersistMu.Lock()
	defer d.trustPersistMu.Unlock()

	dir := filepath.Dir(d.trustPath)
	tmp, err := os.CreateTemp(dir, ".trustlog-chain-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, werr := tmp.Write(chain); werr != nil {
		tmp.Close()
		os.Remove(tmpName)
		return werr
	}
	if serr := tmp.Sync(); serr != nil {
		tmp.Close()
		os.Remove(tmpName)
		return serr
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpName)
		return cerr
	}
	if rerr := os.Rename(tmpName, d.trustPath); rerr != nil {
		os.Remove(tmpName)
		return rerr
	}
	// Best-effort: fsync the directory so the rename is durable.
	if dh, derr := os.Open(filepath.Dir(d.trustPath)); derr == nil {
		_ = dh.Sync()
		dh.Close()
	}
	return nil
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

// genesisHeadPath is the state file holding the pinned genesis head, kept beside
// the chain so a node's locked state is self-contained in its state dir.
func genesisHeadPath(chainPath string) string {
	return filepath.Join(filepath.Dir(chainPath), "trustlog-genesis")
}

// LoadPinnedGenesis reads a persisted genesis head. Returns (nil, nil) when the file
// is ABSENT (open mode is legitimate). Returns an error when the file EXISTS but is
// unreadable or not a 32-byte hash — a corrupt persisted genesis must fail closed at
// boot, never silently revert a locked node to open mode.
func LoadPinnedGenesis(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading persisted genesis %s: %w", path, err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("persisted genesis %s is %d bytes, want 32 (corrupt)", path, len(b))
	}
	return b, nil
}

// writeGenesisHead atomically persists the pinned genesis head beside the chain.
func (d *Node) writeGenesisHead(head []byte) error {
	d.trustPersistMu.Lock()
	defer d.trustPersistMu.Unlock()
	path := genesisHeadPath(d.trustPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".trustlog-genesis-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, werr := tmp.Write(head); werr != nil {
		tmp.Close()
		os.Remove(tmpName)
		return werr
	}
	if serr := tmp.Sync(); serr != nil {
		tmp.Close()
		os.Remove(tmpName)
		return serr
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpName)
		return cerr
	}
	if rerr := os.Rename(tmpName, path); rerr != nil {
		os.Remove(tmpName)
		return rerr
	}
	// Best-effort: fsync the directory so the rename is durable.
	if dh, derr := os.Open(filepath.Dir(path)); derr == nil {
		_ = dh.Sync()
		dh.Close()
	}
	return nil
}

// activateTrust enables locked mode at runtime (lock.init): pin path, persist the
// chain + genesis head, then publish the store atomically. The per-uplink sync loop
// (polling the atomic store) then offers it to the gateway without a reconnect.
// Persisting before Store ensures the node is either fully persisted+enabled or
// error+not-enabled; it is never enabled-but-unpersisted.
func (d *Node) activateTrust(store *trustlog.SyncStore, genesisHead []byte, chainPath string) error {
	d.trustPath = chainPath
	if err := os.MkdirAll(filepath.Dir(chainPath), 0o700); err != nil {
		return err
	}
	if err := d.persistChain(store.Bytes()); err != nil {
		return err
	}
	if err := d.writeGenesisHead(genesisHead); err != nil {
		return err
	}
	d.trust.Store(store) // publish only after both persists succeed
	return nil
}
