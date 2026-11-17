package node

import (
	"context"
	"sync"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/procwalk"
)

// The hooks below let tests outside this package drive agent orchestration
// without tmux or real agents. Call them before ConnectGateway.

// SetAdapterForTest makes a serve the sessions of agent. Test-only.
func (d *Node) SetAdapterForTest(agent string, a adapter.Adapter) { d.adapters[agent] = a }

// SetSpawnForTest replaces how orch.session.spawn starts a session. Test-only.
func (d *Node) SetSpawnForTest(fn func(ctx context.Context, p api.SpawnParams, presetID string) (api.SpawnResult, error)) {
	d.spawnFn = fn
}

// SetProcTableForTest replaces the process table that caller resolution walks. Test-only.
func (d *Node) SetProcTableForTest(fn func(context.Context) (procwalk.Table, error)) {
	d.procTable = fn
}

// LocalDispatchForTest dispatches a call as the local socket serves it, with
// orch.* and lock.* allowed. Test-only.
func (d *Node) LocalDispatchForTest() api.DispatchFunc { return d.server.DispatchFunc() }

// StartOrchForTest starts the message and outbox loops that Run starts, without
// Run's socket or session scans. It needs an orch store. The returned wait
// blocks until both loops have returned after ctx ends. Test-only.
func (d *Node) StartOrchForTest(ctx context.Context) (wait func()) {
	d.markOrchSettled()
	var wg sync.WaitGroup
	wg.Go(func() { d.orchLoop(ctx) })
	wg.Go(func() { d.outboxLoop(ctx) })
	return wg.Wait
}
