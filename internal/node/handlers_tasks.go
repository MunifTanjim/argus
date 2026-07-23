package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
)

// handleTasks returns a session's current Claude Code task list. Agents without
// a task store (or a session with no transcript yet) return an empty list.
func (d *Node) handleTasks(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SessionRef](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	ts, ok := d.adapterFor(s.Agent).(adapter.TaskSource)
	if !ok || s.TranscriptPath == "" {
		return api.TasksResult{Tasks: []api.Task{}}, nil
	}
	tasks, err := ts.ReadTasks(s.TranscriptPath)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	if tasks == nil {
		tasks = []api.Task{}
	}
	return api.TasksResult{Tasks: tasks}, nil
}
