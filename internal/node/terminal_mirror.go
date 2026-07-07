package node

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

type mirrorState struct {
	name       string
	window     string // "<mirror>:<window_index>"
	prevActive string
	prevZoomed bool
	didZoom    bool
}

// reapMirrors kills orphaned mirror sessions (name matches the marker + affixes)
// on every configured tmux server. Origin window zoom/pane-selection lost to an
// unclean exit is not recovered — accepted edge.
func (d *Node) reapMirrors(ctx context.Context) {
	for _, c := range d.clients {
		names, err := c.ListSessions(ctx)
		if err != nil {
			continue
		}
		for _, name := range names {
			if strings.HasPrefix(name, d.mirrorPrefix) &&
				strings.HasSuffix(name, d.mirrorSuffix) &&
				strings.Contains(name, mirrorMarker) {
				if err := c.KillSession(ctx, name); err != nil {
					d.log.Warn("reap mirror", "name", name, "err", err)
				}
			}
		}
	}
}

// setupMirror creates and locks down a grouped mirror session zoomed to the
// agent pane, recording shared window state to restore. Grouped sessions share
// one window (and size); window-size latest makes it follow the attach, so the
// shared origin window is resized to the viewer's dimensions — accepted.
func (d *Node) setupMirror(ctx context.Context, c *tmux.Client, s session.Session, termID string) (*mirrorState, error) {
	name := d.mirrorName(termID)
	if err := c.NewGroupedSession(ctx, name, s.Tmux.SessionName); err != nil {
		return nil, fmt.Errorf("create mirror: %w", err)
	}
	m := &mirrorState{name: name}

	// Lockdown (session-scoped; empty key-table neutralizes custom bind -n).
	// window-size latest + aggressive-resize make the shared window follow the
	// attach's PTY size.
	for _, kv := range [][2]string{
		{"prefix", "None"}, {"prefix2", "None"}, {"mouse", "off"},
		{"key-table", "argus-locked"}, {"status", "off"},
		{"window-size", "latest"}, {"aggressive-resize", "on"},
	} {
		if err := c.SetOption(ctx, name, kv[0], kv[1]); err != nil {
			d.restoreMirror(c, m)
			return nil, fmt.Errorf("set %s: %w", kv[0], err)
		}
	}

	// Target the agent pane's own window (grouped sessions share window indices),
	// which is correct even when that window is not the origin's active one.
	idx, err := c.WindowIndexForPane(ctx, s.Tmux.PaneID)
	if err != nil {
		d.restoreMirror(c, m)
		return nil, err
	}
	m.window = fmt.Sprintf("%s:%d", name, idx)

	// Make the agent's window current in the mirror so attached clients see it.
	if err := c.SelectWindow(ctx, m.window); err != nil {
		d.restoreMirror(c, m)
		return nil, err
	}

	// Record shared window state before mutating it.
	info, err := c.WindowInfo(ctx, m.window)
	if err != nil {
		d.restoreMirror(c, m)
		return nil, err
	}
	m.prevActive, m.prevZoomed = info.ActivePane, info.Zoomed

	if err := c.SelectPane(ctx, s.Tmux.PaneID); err != nil {
		d.restoreMirror(c, m)
		return nil, err
	}
	if info.Panes > 1 {
		if err := c.SetPaneZoom(ctx, m.window, s.Tmux.PaneID, true); err != nil {
			d.restoreMirror(c, m)
			return nil, err
		}
		m.didZoom = true
	}
	// No resize-window: it would pin window-size to manual on the shared window.
	return m, nil
}

// restoreMirror reverts shared window state and kills the mirror session. Best
// effort: logs failures, never blocks teardown. Uses its own bounded context so
// it still runs when the caller's context is already cancelled (disconnect mid-open).
func (d *Node) restoreMirror(c *tmux.Client, m *mirrorState) {
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if m.didZoom {
		if err := c.SetPaneZoom(ctx, m.window, m.prevActive, m.prevZoomed); err != nil {
			d.log.Warn("mirror restore zoom", "err", err)
		}
	}
	if m.prevActive != "" {
		_ = c.SelectPane(ctx, m.prevActive)
	}
	if err := c.KillSession(ctx, m.name); err != nil {
		d.log.Warn("mirror kill", "name", m.name, "err", err)
	}
}
