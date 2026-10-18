package claudecode

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// QueuesPrompts reports that a busy session takes prompts while its mod is
// connected: Claude queues them until it reads them.
func (a *ccAdapter) QueuesPrompts(s session.Session) bool { return s.Input == session.InputAPI }

// ModBridge is the bridge the node's mod socket serves.
func (a *ccAdapter) ModBridge() *modbridge.Bridge { return a.bridge }

// SendPrompt hands text to the session's mod: a leading "/" runs it as a slash
// command, anything else is submitted as a prompt. While a turn runs, Claude
// takes it once the turn ends.
func (a *ccAdapter) SendPrompt(ctx context.Context, sess session.Session, text string) error {
	if sess.InteractionPending() {
		return fmt.Errorf("%s: %w", sess.ID, api.ErrSessionBusy)
	}
	_, err := a.sendMod(ctx, sess, modbridge.Command{Kind: modbridge.KindPrompt, Text: text})
	return err
}

func (a *ccAdapter) IsShellInput(text string) bool { return strings.HasPrefix(text, "!") }

// SendShellInput hands "!" bash mode input to the session's mod, which types the
// "!" and the Enter in its pane and puts the user's draft back.
func (a *ccAdapter) SendShellInput(ctx context.Context, sess session.Session, text string) (bool, error) {
	if !sess.Controllable() {
		return false, fmt.Errorf("%s: %w", sess.ID, api.ErrShellNeedsPane)
	}
	if sess.InteractionPending() {
		return false, fmt.Errorf("%s: %w", sess.ID, api.ErrSessionBusy)
	}
	_, err := a.sendMod(ctx, sess, modbridge.Command{Kind: modbridge.KindShell, Text: text})
	if errors.Is(err, modbridge.ErrBusy) {
		return false, fmt.Errorf("%s: %w", sess.ID, api.ErrSessionBusy)
	}
	return false, err
}

// FillPrompt puts text in Claude's input box in place of the user's draft, which
// the mod puts back once the text is submitted; the caller sends the Enter. A
// slash command is not filled but sent as a prompt, so Claude runs it after the
// turn.
func (a *ccAdapter) FillPrompt(ctx context.Context, sess session.Session, text string) (bool, error) {
	if strings.HasPrefix(text, "/") {
		return false, a.SendPrompt(ctx, sess, text)
	}
	if sess.InteractionPending() {
		return false, fmt.Errorf("%s: %w", sess.ID, api.ErrSessionBusy)
	}
	ran, err := a.sendMod(ctx, sess, modbridge.Command{Kind: modbridge.KindFill, Text: text})
	if errors.Is(err, modbridge.ErrBusy) {
		return false, fmt.Errorf("%s: %w", sess.ID, api.ErrSessionBusy)
	}
	if err != nil {
		return false, err
	}
	if !ran {
		return false, fmt.Errorf("%s: claude did not fill the input in time", sess.ID)
	}
	return true, nil
}

// Dismiss ends a paneless Claude session through its mod, cutting short any
// running turn.
func (a *ccAdapter) Dismiss(ctx context.Context, sess session.Session) error {
	_, err := a.sendMod(ctx, sess, modbridge.Command{Kind: modbridge.KindExit})
	return err
}

// sendMod sends cmd through the session's mod and reports whether it ran, as
// opposed to being accepted to run later.
func (a *ccAdapter) sendMod(ctx context.Context, sess session.Session, cmd modbridge.Command) (bool, error) {
	if a.bridge == nil || sess.AgentSessionID == "" {
		return false, fmt.Errorf("%s: %w", sess.ID, api.ErrNoTerminalControl)
	}
	ran, err := a.bridge.Send(ctx, sess.AgentSessionID, cmd)
	if errors.Is(err, modbridge.ErrUnreachable) {
		return false, fmt.Errorf("%s: %w", sess.ID, api.ErrNoTerminalControl)
	}
	return ran, err
}
