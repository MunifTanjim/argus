package claudecode

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/modbridge"
	"github.com/MunifTanjim/argus/internal/session"
)

// modGrace is how long a Claude session stays mod-reachable after its poll
// closes: the mod re-polls at once, so a longer gap means it is gone. A var so
// tests can shorten it.
var modGrace = 5 * time.Second

// modAckTimeout is how long a command handed to the mod may go unacked. The mod
// acks within about a second, even for a command Claude runs only later.
var modAckTimeout = 10 * time.Second

// inputMode is the Claude input policy: prompts go through the mod while it is
// connected. A session without one (Claude started before the mod was
// installed, or too old to load it) falls back to pane keystrokes.
func (a *ccAdapter) inputMode(s session.Session) session.InputMode {
	if a.bridge != nil && s.AgentSessionID != "" && a.bridge.Reachable(s.AgentSessionID) {
		return session.InputAPI
	}
	if s.Tmux.PaneID != "" {
		return session.InputPane
	}
	return session.InputNone
}

// ModBridge is the bridge the node's mod socket serves.
func (a *ccAdapter) ModBridge() *modbridge.Bridge { return a.bridge }

// SendPrompt hands text to the session's mod: a leading "/" runs it as a slash
// command, anything else is submitted as a prompt.
func (a *ccAdapter) SendPrompt(ctx context.Context, sess session.Session, text string) error {
	return a.sendMod(ctx, sess, modbridge.Command{Kind: modbridge.KindPrompt, Text: text})
}

// Dismiss ends a paneless Claude session through its mod, cutting short any
// running turn.
func (a *ccAdapter) Dismiss(ctx context.Context, sess session.Session) error {
	return a.sendMod(ctx, sess, modbridge.Command{Kind: modbridge.KindExit})
}

func (a *ccAdapter) sendMod(ctx context.Context, sess session.Session, cmd modbridge.Command) error {
	if a.bridge == nil || sess.AgentSessionID == "" {
		return fmt.Errorf("%s: %w", sess.ID, api.ErrNoTerminalControl)
	}
	err := a.bridge.Send(ctx, sess.AgentSessionID, cmd)
	if errors.Is(err, modbridge.ErrUnreachable) {
		return fmt.Errorf("%s: %w", sess.ID, api.ErrNoTerminalControl)
	}
	return err
}
