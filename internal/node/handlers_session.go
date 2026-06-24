package node

import (
	"context"
	"encoding/json"
	"fmt"
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
	return api.IdentifyResult{ID: d.id, Label: d.label}, nil
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

// handleSessionSpawn launches a new Claude Code session on argus's private server.
func (d *Node) handleSessionSpawn(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SpawnParams](params)
	if err != nil {
		return nil, err
	}
	if p.Name == "" {
		return nil, fmt.Errorf("spawn: name is required")
	}
	command := p.Command
	if command == "" {
		command = "claude"
	}
	c := d.clients[session.TmuxServerArgus]
	paneID, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: p.Name, Cwd: p.Cwd, Command: command})
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
