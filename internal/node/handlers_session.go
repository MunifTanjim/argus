package node

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// Session-facing RPC handlers: reads from the registry and tmux control of panes.

// submitDelay is the pause between injecting text and sending the submitting
// Enter. Claude Code's TUI treats stdin that arrives in one read as a paste and
// inserts it literally; a CR coalesced into that same read is swallowed, so the
// message lands in the prompt but never submits. Holding the Enter back makes it
// a separate read event, which submits reliably. It is a var so tests can tune
// it. See TestSessionInputDelaysEnterAfterText.
var submitDelay = 75 * time.Millisecond

func (d *Node) handleSessionsList(context.Context, json.RawMessage) (any, error) {
	return d.reg.Snapshot(), nil
}

// handleNodeIdentify announces this node's identity over the gateway uplink.
func (d *Node) handleNodeIdentify(context.Context, json.RawMessage) (any, error) {
	return api.IdentifyResult{ID: d.id, Label: d.label, Capabilities: d.caps}, nil
}

// handleNodesList lets a client talking directly to a plain node (no gateway)
// enumerate spawn targets — here, just this node — so it can gate the spawn UI on
// tmux availability instead of discovering it only when the spawn fails. The
// NodeID is empty: a plain node has no routing namespace, so the client addresses
// it implicitly (no node_id on sessions.spawn). The gateway overrides this method
// with its own cross-node aggregation.
func (d *Node) handleNodesList(context.Context, json.RawMessage) (any, error) {
	return []api.NodeInfo{{NodeLabel: d.label, Capabilities: d.caps}}, nil
}

// handleSessionsRefresh rescans on demand, then returns the current snapshot.
func (d *Node) handleSessionsRefresh(ctx context.Context, _ json.RawMessage) (any, error) {
	d.scan(ctx)
	return d.reg.Snapshot(), nil
}

// handleTranscriptView returns the grouped, display-ready chunk view for a session.
func (d *Node) handleTranscriptView(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TranscriptParams](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	if s.TranscriptPath == "" {
		return claudecode.TranscriptView{}, nil
	}
	return claudecode.ReadTranscriptView(s.TranscriptPath)
}

// handleSessionToolDetail returns one tool item's full input/result, fetched on
// demand by tool_use id (transcript chunks ship without these heavy bodies).
func (d *Node) handleSessionToolDetail(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.ToolDetailParams](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	if s.TranscriptPath == "" {
		return nil, fmt.Errorf("session has no transcript: %s", p.SessionID)
	}
	td, found, err := claudecode.FindToolDetail(s.TranscriptPath, p.AgentID, p.ToolID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown tool: " + p.ToolID}
	}
	return toAPIToolDetail(td), nil
}

// toAPIToolDetail maps the adapter's ToolDetail to the wire type.
func toAPIToolDetail(td claudecode.ToolDetail) api.ToolDetail {
	return api.ToolDetail{ToolInput: td.ToolInput, Result: td.Result, ResultIsError: td.ResultIsError}
}

// handleSessionCapture returns a live capture of a session's tmux pane.
func (d *Node) handleSessionCapture(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SessionRef](params)
	if err != nil {
		return nil, err
	}
	s, c, err := d.resolve(p.SessionID)
	if err != nil {
		return nil, err
	}
	screen, err := c.CapturePane(ctx, s.Tmux.PaneID, tmux.CaptureOpts{Escapes: true})
	if err != nil {
		return nil, err
	}
	return api.CaptureResult{Screen: screen}, nil
}

// handleSessionInput sends text (and optionally Enter) to a session's pane, after
// optionally normalizing the pane for input (exit copy mode; ensure vim insert).
func (d *Node) handleSessionInput(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.InputParams](params)
	if err != nil {
		return nil, err
	}
	s, c, err := d.resolve(p.SessionID)
	if err != nil {
		return nil, err
	}
	sentText := false
	if p.Text != "" {
		if p.Prepare {
			if err := claudecode.PrepareTextInput(ctx, c, s.Tmux.PaneID); err != nil {
				return nil, err
			}
		}
		// Multi-line text must be pasted: sent as literal keystrokes, a raw LF
		// is dropped and a raw CR submits the line, so newlines are lost either
		// way. A bracketed paste preserves them. Single-line stays literal so
		// interactive triggers (slash menus, @-mentions) still fire as typed.
		if strings.Contains(p.Text, "\n") {
			if err := c.PasteText(ctx, s.Tmux.PaneID, p.Text); err != nil {
				return nil, err
			}
		} else if err := c.SendText(ctx, s.Tmux.PaneID, p.Text); err != nil {
			return nil, err
		}
		sentText = true
	}
	if p.Submit {
		// When text was just injected, hold the Enter back so Claude's TUI
		// reads it separately from the text (a coalesced CR is swallowed and
		// never submits). See submitDelay.
		if sentText {
			if err := sleepCtx(ctx, submitDelay); err != nil {
				return nil, err
			}
		}
		if err := c.SendKeys(ctx, s.Tmux.PaneID, "Enter"); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// sleepCtx waits for d, returning early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// handleSessionKey sends one or more named keys (Escape, C-c, ...) to a pane.
func (d *Node) handleSessionKey(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.KeyParams](params)
	if err != nil {
		return nil, err
	}
	s, c, err := d.resolve(p.SessionID)
	if err != nil {
		return nil, err
	}
	if len(p.Keys) == 0 {
		return nil, nil
	}
	return nil, c.SendKeys(ctx, s.Tmux.PaneID, p.Keys...)
}

// defaultSessionName derives a tmux session name from a working directory when
// the client leaves the name blank. The directory's base name is meaningful
// (e.g. the repo folder); it falls back to "claude" for empty/root paths.
func defaultSessionName(cwd string) string {
	base := filepath.Base(strings.TrimSpace(cwd))
	switch base {
	case "", ".", string(filepath.Separator):
		return "claude"
	}
	return base
}

// uniqueName returns base if it is free, otherwise base-2, base-3, … so the new
// tmux session name does not collide with an existing one on the server.
func uniqueName(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !taken[cand] {
			return cand
		}
	}
}

// buildSpawnOpts assembles the tmux launch options for a spawn: the command
// defaults to "claude", and a non-empty prompt is passed as the command's
// argument so the new session starts working on it.
func buildSpawnOpts(name, cwd, command, prompt string) tmux.NewSessionOpts {
	if command == "" {
		command = "claude"
	}
	opts := tmux.NewSessionOpts{Name: name, Cwd: cwd, Command: command}
	if prompt != "" {
		opts.Args = []string{prompt}
	}
	return opts
}

// handleSessionSpawn launches a new Claude Code session on argus's private server.
func (d *Node) handleSessionSpawn(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SpawnParams](params)
	if err != nil {
		return nil, err
	}
	if !d.caps.SpawnSession {
		return nil, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: "spawn unavailable: tmux not found on node " + d.label,
		}
	}
	c := d.clients[session.TmuxServerArgus]
	if p.Name == "" {
		taken := map[string]bool{}
		// ListPanes error is intentionally ignored: on failure the dedup set is
		// empty and any name collision will surface as a tmux new-session error.
		if panes, err := c.ListPanes(ctx); err == nil {
			for _, pn := range panes {
				taken[pn.SessionName] = true
			}
		}
		p.Name = uniqueName(defaultSessionName(p.Cwd), taken)
	}
	paneID, err := c.NewSession(ctx, buildSpawnOpts(p.Name, p.Cwd, p.Command, p.Prompt))
	if err != nil {
		return nil, err
	}
	// Discovery will register it shortly; trigger a scan for immediacy.
	go d.scan(context.Background())
	return api.SpawnResult{SessionID: string(session.TmuxServerArgus) + ":" + paneID, PaneID: paneID}, nil
}

// handleSessionKill kills a session's pane.
func (d *Node) handleSessionKill(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SessionRef](params)
	if err != nil {
		return nil, err
	}
	s, c, err := d.resolve(p.SessionID)
	if err != nil {
		return nil, err
	}
	if err := c.KillPane(ctx, s.Tmux.PaneID); err != nil {
		return nil, err
	}
	go d.scan(context.Background())
	return nil, nil
}
