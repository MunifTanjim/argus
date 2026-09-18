package opencode

import (
	"context"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

type discoverer struct {
	reg *registry.Registry
}

func newDiscoverer(reg *registry.Registry, _ map[session.TmuxServer]*tmux.Client) adapter.Discoverer {
	return &discoverer{reg: reg}
}

func (d *discoverer) ScanOnce(context.Context) error { return nil }
