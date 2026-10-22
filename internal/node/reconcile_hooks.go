package node

import (
	"errors"
	"log/slog"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/adapter"
)

// ReconcileHooks installs or updates each agent's integration through the
// node's own adapters, so a change can reach a live discoverer (Codex reloads
// its MCP servers). Agents that were never run are skipped. Best-effort, and
// slow when it runs the claude CLI, so callers run it in the background.
func (d *Node) ReconcileHooks(log *slog.Logger, opts adapter.InstallOptions) {
	for _, a := range d.adapterList {
		path, err := a.SettingsPath()
		if err != nil {
			log.Warn("reconcile hooks failed", "agent", a.Agent(), "err", err)
			continue
		}
		if path != "" && !dirExists(filepath.Dir(path)) {
			continue
		}
		added, err := a.Reconcile(opts)
		switch {
		case errors.Is(err, adapter.ErrAgentNotInstalled):
		case err != nil:
			log.Warn("reconcile hooks failed", "agent", a.Agent(), "err", err)
		case len(added) > 0:
			log.Info("reconciled argus hooks", "agent", a.Agent(), "added", added)
		}
	}
}
