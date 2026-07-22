package node

import (
	"context"

	"github.com/MunifTanjim/argus/internal/push"
)

// desktopSink adapts a Node into a push.Sink so the standalone push.Watch loop
// renders through the same focus-aware path as gateway pushes.
type desktopSink struct{ d *Node }

func (s desktopSink) Notify(ctx context.Context, n push.Notification) { s.d.renderDesktop(ctx, n) }

// DesktopSink returns a Sink that renders desktop notifications on this node,
// suppressing any whose session the user is already looking at.
func (d *Node) DesktopSink() push.Sink { return desktopSink{d} }

// renderDesktop shows n unless its session is already focused (the active pane of
// an attached tmux client), where a banner would only be noise. A session that
// doesn't resolve locally is never focused here, so it renders.
func (d *Node) renderDesktop(ctx context.Context, n push.Notification) {
	if d.notifier == nil {
		return
	}
	if id := n.SessionID(); id != "" {
		if focused, err := d.sessionFocused(ctx, id); err != nil {
			d.log.Warn("desktop: focus check failed, notifying anyway", "session", id, "err", err)
		} else if focused {
			d.log.Debug("desktop: suppressed; session already focused", "session", id)
			return
		}
	}
	d.notifier.Notify(ctx, n)
}

// sessionFocused reports whether id (a local or composite session id) maps to a
// local pane an attached tmux client is currently showing. Unknown or foreign
// sessions are not focused on this machine.
func (d *Node) sessionFocused(ctx context.Context, id string) (bool, error) {
	s, c, err := d.resolveLocal(id)
	if err != nil {
		return false, nil // not local / unknown -> can't be focused on this machine
	}
	return d.focusedFn(ctx, c, s.Tmux.PaneID)
}
