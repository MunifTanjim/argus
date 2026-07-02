// Package adapters is the registry of tool adapters argus ships with. It is the
// single place that names concrete adapter implementations; the node and the CLI
// consume the set through here so adding a tool is a one-line change.
package adapters

import (
	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/adapter/codex"
)

// All returns every registered tool adapter in priority order. The first entry
// is the default used for tool-agnostic paths (e.g. on-disk history reads). Add
// a tool by appending its adapter here.
func All() []adapter.Adapter {
	return []adapter.Adapter{
		claudecode.New(),
		codex.New(),
	}
}

// Default returns the default adapter (All()[0]).
func Default() adapter.Adapter { return All()[0] }

// ByTool returns the registered adapter with the given Tool() id, or the default
// adapter when tool is empty or unknown.
func ByTool(tool string) adapter.Adapter {
	for _, a := range All() {
		if a.Tool() == tool {
			return a
		}
	}
	return Default()
}
