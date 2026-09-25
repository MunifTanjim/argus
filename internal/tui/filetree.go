package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

type treeDir struct {
	entries []api.DirEntry
	loading bool
	err     error
}

// treeRow is one visible line: an entry, or a note row (loading, error, or
// empty) under a directory.
type treeRow struct {
	entry api.DirEntry
	depth int
	note  string
}

// fileTree is the right sidebar's nested file tree for one workspace. Each
// directory loads on first unfold and stays cached for the tree's life.
type fileTree struct {
	ws       string
	expanded map[string]bool
	dirs     map[string]*treeDir
	cursor   int
}

// treeRequest asks the screen to load a directory or open a file; at most one
// field is set.
type treeRequest struct {
	loadDir  *string
	openFile *string
}

func newFileTree(ws string) fileTree {
	return fileTree{ws: ws, expanded: map[string]bool{}, dirs: map[string]*treeDir{}}
}

// setDir stores a listing. Rows above the cursor can grow when a directory
// loads, so the cursor follows its entry by path.
func (t *fileTree) setDir(dir string, entries []api.DirEntry, err error) {
	var keep string
	if rows := t.rows(); t.cursor < len(rows) && rows[t.cursor].note == "" {
		keep = rows[t.cursor].entry.Path
	}
	t.dirs[dir] = &treeDir{entries: entries, err: err}
	rows := t.rows()
	for i, r := range rows {
		if keep != "" && r.note == "" && r.entry.Path == keep {
			t.cursor = i
			return
		}
	}
	t.cursor = min(t.cursor, cursorBottom(len(rows)))
}

func (t fileTree) rows() []treeRow {
	var out []treeRow
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		d := t.dirs[dir]
		switch {
		case d == nil || d.loading:
			out = append(out, treeRow{depth: depth, note: "loading…"})
			return
		case d.err != nil:
			out = append(out, treeRow{depth: depth, note: "error: " + d.err.Error()})
			return
		case len(d.entries) == 0:
			out = append(out, treeRow{depth: depth, note: "(empty)"})
			return
		}
		for _, e := range d.entries {
			out = append(out, treeRow{entry: e, depth: depth})
			if e.IsDir && t.expanded[e.Path] {
				walk(e.Path, depth+1)
			}
		}
	}
	walk("", 0)
	return out
}

func (t *fileTree) key(msg tea.KeyPressMsg, page int) treeRequest {
	rows := t.rows()
	n := len(rows)
	k := projectsKeys
	switch {
	case key.Matches(msg, k.Up):
		t.cursor = cursorUp(t.cursor)
	case key.Matches(msg, k.Down):
		t.cursor = cursorDown(t.cursor, n)
	case key.Matches(msg, k.Top):
		t.cursor = 0
	case key.Matches(msg, k.Bottom):
		t.cursor = cursorBottom(n)
	case key.Matches(msg, k.HalfUp):
		t.cursor = max(0, t.cursor-page)
	case key.Matches(msg, k.HalfDown):
		t.cursor = min(cursorBottom(n), t.cursor+page)
	case key.Matches(msg, k.Left):
		t.left(rows)
	case key.Matches(msg, k.Right):
		return t.unfold(rows)
	case key.Matches(msg, k.Enter):
		if t.cursor >= n || rows[t.cursor].note != "" {
			return treeRequest{}
		}
		e := rows[t.cursor].entry
		if !e.IsDir {
			p := e.Path
			return treeRequest{openFile: &p}
		}
		if t.expanded[e.Path] {
			delete(t.expanded, e.Path)
			return treeRequest{}
		}
		return t.unfold(rows)
	}
	return treeRequest{}
}

func (t *fileTree) unfold(rows []treeRow) treeRequest {
	if t.cursor >= len(rows) || rows[t.cursor].note != "" || !rows[t.cursor].entry.IsDir {
		return treeRequest{}
	}
	p := rows[t.cursor].entry.Path
	t.expanded[p] = true
	if _, ok := t.dirs[p]; ok {
		return treeRequest{}
	}
	t.dirs[p] = &treeDir{loading: true}
	return treeRequest{loadDir: &p}
}

func (t *fileTree) left(rows []treeRow) {
	if t.cursor >= len(rows) {
		return
	}
	r := rows[t.cursor]
	if r.note == "" && r.entry.IsDir && t.expanded[r.entry.Path] {
		delete(t.expanded, r.entry.Path)
		return
	}
	for i := t.cursor - 1; i >= 0; i-- {
		if rows[i].depth < r.depth {
			t.cursor = i
			return
		}
	}
}

// Each row has a 2-cell gutter for the cursor bar so names line up under the
// sidebar's tab strip.
func (t fileTree) view(w, h int, focused bool) string {
	rows := t.rows()
	lines := make([]string, len(rows))
	for i, r := range rows {
		indent := strings.Repeat("  ", r.depth)
		var text string
		switch {
		case r.note != "":
			text = indent + dimStyle.Render(r.note)
		case r.entry.IsDir && t.expanded[r.entry.Path]:
			text = indent + "▾ " + r.entry.Name
		case r.entry.IsDir:
			text = indent + "▸ " + r.entry.Name
		default:
			text = indent + "  " + r.entry.Name
		}
		sel := i == t.cursor
		if sel && focused {
			text = cursorStyle.Render(text)
		}
		lines[i] = sideMarker(sel, focused) + truncateLine(text, max(1, w-screenMargin))
	}
	return strings.Join(windowSpan(lines, t.cursor, t.cursor+1, h), "\n")
}

// sideMarker is the right sidebar's 2-cell cursor gutter.
func sideMarker(sel, focused bool) string {
	switch {
	case sel && focused:
		return lipgloss.NewStyle().Foreground(ColorFocus).Render("▌") + " "
	case sel:
		return StyleDim.Render("▌") + " "
	}
	return strings.Repeat(" ", screenMargin)
}
