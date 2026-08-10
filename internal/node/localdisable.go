package node

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

// localDisableMarkerPath is the per-node local-disable marker, beside the chain.
func (d *Node) localDisableMarkerPath() string {
	return filepath.Join(filepath.Dir(d.trustPath), "trustlog-local-disabled")
}

// localDisabled reports whether this node's locked-mode enforcement is locally disabled.
func (d *Node) localDisabled() bool { return d.localDisabledFlag.Load() }

// LocalDisable turns off locked-mode enforcement for THIS node only — the guaranteed
// escape hatch when the gateway censors a network-wide `lock disable`. Persisted so it
// survives reboot; re-locking requires removing the marker / re-init.
func (d *Node) LocalDisable() error {
	if d.trustPath == "" {
		return errors.New("node: trust state path not configured")
	}
	if err := atomicfile.Write(d.localDisableMarkerPath(), []byte("1")); err != nil {
		return err
	}
	d.localDisabledFlag.Store(true)
	return nil
}

// LoadLocalDisabled sets the local-disable flag if the marker exists (call at boot).
func (d *Node) LoadLocalDisabled() {
	if d.trustPath == "" {
		return
	}
	if _, err := os.Stat(d.localDisableMarkerPath()); err == nil {
		d.localDisabledFlag.Store(true)
	}
}
