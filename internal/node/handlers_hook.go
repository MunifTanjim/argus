package node

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
)

// handleHook applies a Claude Code hook event to the registry. PermissionRequest
// parks the hook and blocks until the user answers in argus, returning the
// decision JSON. Claude's own prompt stays live in parallel — whoever answers
// first wins. Answering in Claude's pane closes the connection (ctx cancel), so
// blocking never hangs a session even with no TUI attached.
func (d *Node) handleHook(ctx context.Context, params json.RawMessage) (any, error) {
	ev, err := api.Decode[claudecode.HookEvent](params)
	if err != nil {
		return nil, err
	}
	s, alive := claudecode.ProcessHook(d.reg, ev)
	event := claudecode.EventName(ev)
	api.LogAttr(ctx, "event", event)
	if tool, _ := claudecode.PermissionPayload(ev); tool != "" {
		api.LogAttr(ctx, "tool", tool)
	}
	// ProcessHook already updated the firing session, so rescan only for lifecycle
	// events: SessionStart surfaces a new pane (and enriches Name), SessionEnd prunes
	// a vanished one. Per-tool-call events come from known sessions — scanning them is
	// pure ps+tmux churn.
	switch event {
	case "SessionStart", "SessionEnd":
		go d.scan(context.Background())
	}

	if alive && event == "PermissionRequest" {
		return api.HookResult{Output: d.awaitDecision(ctx, s.ID, ev)}, nil
	}
	return api.HookResult{}, nil
}

// handleSessionRespond resolves the session's parked PermissionRequest with the
// hook decision JSON. Anything not parked is a no-op (raw screen view is the
// manual fallback).
func (d *Node) handleSessionRespond(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.RespondParams](params)
	if err != nil {
		return nil, err
	}
	if pd := d.takePending(p.SessionID); pd != nil {
		pd.ch <- buildDecision(pd, p)
	}
	return nil, nil
}
