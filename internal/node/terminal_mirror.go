package node

import (
	"context"
	"fmt"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

const (
	lockedKeyTable = "argus-locked"
	// lockedTermKeyTable is lockedKeyTable for a persistent terminal, whose
	// wheel scrolls the shell's history.
	lockedTermKeyTable = "argus-locked-term"
)

// mouseButtonKeys pass button events to a program that asked for the mouse;
// tmux drops them otherwise.
var mouseButtonKeys = func() []string {
	var keys []string
	for _, b := range []string{"1", "2", "3"} {
		for _, ev := range []string{"MouseDown", "MouseUp", "MouseDrag", "MouseDragEnd"} {
			keys = append(keys, ev+b+"Pane")
		}
	}
	return keys
}()

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
			if d.isMirror(name) {
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

	if err := d.lockdownMirror(ctx, c, m, lockedKeyTable); err != nil {
		return nil, err
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

// lockdownMirror removes the mirror on failure.
func (d *Node) lockdownMirror(ctx context.Context, c *tmux.Client, m *mirrorState, table string) error {
	// Session-scoped; a key-table with only the mouse bound neutralizes custom
	// bind -n. window-size latest + aggressive-resize make the shared window
	// follow the attach's PTY size.
	for _, kv := range [][2]string{
		{"prefix", "None"}, {"prefix2", "None"}, {"mouse", "on"},
		{"key-table", table}, {"status", "off"},
		{"window-size", "latest"}, {"aggressive-resize", "on"},
	} {
		if err := c.SetOption(ctx, m.name, kv[0], kv[1]); err != nil {
			d.restoreMirror(c, m)
			return fmt.Errorf("set %s: %w", kv[0], err)
		}
	}
	binds := [][]string{{"WheelDownPane", "send-keys", "-M"}}
	if table == lockedTermKeyTable {
		// tmux's own default: a shell's wheel enters copy mode to scroll its
		// history; a program that asked for the mouse gets the wheel.
		binds = append(binds, []string{"WheelUpPane", "if-shell", "-F", "#{||:#{pane_in_mode},#{mouse_any_flag}}", "send-keys -M", "copy-mode -e"})
	} else {
		// An agent's wheel goes only to a program that asked for the mouse, so
		// the shared pane never enters copy mode.
		binds = append(binds, []string{"WheelUpPane", "send-keys", "-M"})
	}
	for _, key := range mouseButtonKeys {
		binds = append(binds, []string{key, "send-keys", "-M"})
	}
	for _, b := range binds {
		if err := c.BindKey(ctx, table, b[0], b[1:]...); err != nil {
			d.restoreMirror(c, m)
			return fmt.Errorf("bind %s: %w", b[0], err)
		}
	}
	return nil
}

// setupTerminalMirror mirrors one terminal window in a session that holds only
// that window. A grouped mirror would share every terminal, so after a shell
// exits it would show another one; with one linked window, tmux removes the
// mirror when the window dies and the attach ends.
func (d *Node) setupTerminalMirror(ctx context.Context, c *tmux.Client, windowID, termID string) (*mirrorState, error) {
	name := d.mirrorName(termID)
	first, err := c.NewEmptySession(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("create mirror: %w", err)
	}
	m := &mirrorState{name: name}
	if err := c.LinkWindow(ctx, windowID, name); err != nil {
		d.restoreMirror(c, m)
		return nil, fmt.Errorf("link terminal: %w", err)
	}
	if err := c.KillWindow(ctx, first); err != nil {
		d.restoreMirror(c, m)
		return nil, err
	}
	if err := d.lockdownMirror(ctx, c, m, lockedTermKeyTable); err != nil {
		return nil, err
	}
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
