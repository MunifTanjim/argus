package node

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
)

// handleHook applies a Claude Code hook event to the registry. PermissionRequest is
// the decision point: park the hook and block until the user answers in argus, then
// return the decision JSON for the CLI to print. Claude's own prompt stays live in
// parallel — whoever answers first wins. Answering in Claude's pane closes the hook
// connection, which cancels ctx and unblocks awaitDecision, so blocking never hangs a
// session even when no TUI is attached.
func (d *Node) handleHook(ctx context.Context, params json.RawMessage) (any, error) {
	ev, err := api.Decode[claudecode.HookEvent](params)
	if err != nil {
		return nil, err
	}
	s, alive := claudecode.ProcessHook(d.reg, ev)
	// ProcessHook already created/updated the firing session in the registry, so a
	// rescan only adds value for lifecycle events: SessionStart surfaces a brand-new
	// pane (and enriches its Name) and SessionEnd prunes a vanished one. The frequent
	// per-tool-call events (Pre/PostToolUse, etc.) come from already-known sessions, so
	// scanning on them is pure ps+tmux churn.
	switch claudecode.EventName(ev) {
	case "SessionStart", "SessionEnd":
		go d.scan(context.Background())
	}

	if alive && claudecode.EventName(ev) == "PermissionRequest" {
		return api.HookResult{Output: d.awaitDecision(ctx, s.ID, ev)}, nil
	}
	return api.HookResult{}, nil
}

// handleSessionRespond answers a pending interaction by resolving the session's
// parked PermissionRequest with the hook decision JSON (idle prompts use
// sessions.input; the raw screen view covers anything not parked).
func (d *Node) handleSessionRespond(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.RespondParams](params)
	if err != nil {
		return nil, err
	}
	if pd := d.takePending(p.SessionID); pd != nil {
		pd.ch <- buildDecision(pd, p)
	}
	// No parked decision: nothing to do (the raw screen view is the manual fallback).
	return nil, nil
}
