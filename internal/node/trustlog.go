package node

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// trustSyncInterval is how often a connected node re-runs the offer/pull cycle.
// Chain updates are rare, so this is a lazy convergence tick, not a hot loop. Var
// so tests can shorten it.
var trustSyncInterval = 30 * time.Second

// SetTrustSyncIntervalForTest overrides the node's trust-log sync cadence. Test-only.
func SetTrustSyncIntervalForTest(d time.Duration) { trustSyncInterval = d }

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
	d.trust = sync
	d.trustPath = path
	return nil
}

// TrustStore returns the node's trust-log store, or nil when locked mode is off.
func (d *Node) TrustStore() *trustlog.SyncStore { return d.trust }

// syncTrustOnce runs one offer/pull cycle over peer: publish our current chain
// (if any), then pull the gateway's and ingest it, persisting on any advance.
func (d *Node) syncTrustOnce(peer trustCaller) {
	if d.trust == nil {
		return
	}
	if mine := d.trust.Bytes(); mine != nil {
		_ = peer.Call(api.MethodTrustLogOffer, api.TrustLogChain{Chain: mine}, nil)
	}
	var got api.TrustLogChain
	if err := peer.Call(api.MethodTrustLogPull, nil, &got); err != nil || len(got.Chain) == 0 {
		return
	}
	changed, err := d.trust.Ingest(got.Chain)
	if err != nil {
		return // rollback/fork/tamper/wrong-genesis: keep our chain
	}
	if changed {
		if werr := d.persistTrust(); werr != nil {
			d.log.Warn("persisting trust-log chain failed", "path", d.trustPath, "err", werr)
		}
	}
}

// persistTrust writes the current chain to disk atomically: it creates a temp
// file in the same directory, writes the chain, then renames it over trustPath.
// Rename within a directory is atomic on POSIX, so readers and boot always see
// either the old or the new file, never a truncated one. A dedicated mutex
// ensures two goroutines (e.g. lingering + new uplink) never race the temp file
// or the rename.
func (d *Node) persistTrust() error {
	d.trustPersistMu.Lock()
	defer d.trustPersistMu.Unlock()

	dir := filepath.Dir(d.trustPath)
	tmp, err := os.CreateTemp(dir, ".trustlog-chain-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, werr := tmp.Write(d.trust.Bytes()); werr != nil {
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

// runTrustSync drives syncTrustOnce on connect and every trustSyncInterval until
// ctx ends or the uplink drops. No-op when locked mode is off.
func (d *Node) runTrustSync(ctx context.Context, peer *api.Peer) {
	if d.trust == nil {
		return
	}
	d.syncTrustOnce(peer)
	t := time.NewTicker(trustSyncInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-peer.Done():
			return
		case <-t.C:
			d.syncTrustOnce(peer)
		}
	}
}
