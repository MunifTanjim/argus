// Package adapters is the registry of agent adapters argus ships with.
package adapters

import (
	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/antigravity"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/adapter/codex"
)

// All returns every registered adapter in priority order. The first entry is the default.
func All() []adapter.Adapter {
	return []adapter.Adapter{
		claudecode.New(),
		codex.New(),
		antigravity.New(),
	}
}

func Default() adapter.Adapter { return All()[0] }

// ByAgent returns the adapter for the given agent. Empty falls back to Default;
// unknown returns nil.
func ByAgent(agent string) adapter.Adapter {
	if agent == "" {
		return Default()
	}
	for _, a := range All() {
		if a.Agent() == agent {
			return a
		}
	}
	return nil
}
