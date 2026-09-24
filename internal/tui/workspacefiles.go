package tui

import (
	"path"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

// --- fetch commands & messages ------------------------------------------------

func (m model) fetchChangedFiles(ws, against string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.ChangedFilesResult
		err := client.Call(api.MethodWorkspaceChangedFiles, api.WorkspaceRef{WorkspaceID: ws, Against: against}, &r)
		return changedFilesMsg{ws: ws, against: against, files: r.Files, err: err}
	}
}

func (m model) fetchWorkspaceDiff(ws string, f api.ChangedFile, against string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.WorkspaceDiffResult
		params := api.WorkspaceFileParams{WorkspaceID: ws, Path: f.Path, OrigPath: f.OrigPath, Against: against}
		err := client.Call(api.MethodWorkspaceDiff, params, &r)
		return wsDiffMsg{ws: ws, path: f.Path, against: against, diff: r.Diff, notShown: r.NotShown, err: err}
	}
}

func (m model) fetchListDir(ws, dir string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.ListDirResult
		err := client.Call(api.MethodWorkspaceListDir, api.WorkspaceFileParams{WorkspaceID: ws, Path: dir}, &r)
		return listDirMsg{ws: ws, dir: dir, entries: r.Entries, err: err}
	}
}

func (m model) fetchReadFile(ws, p string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.ReadFileResult
		err := client.Call(api.MethodWorkspaceReadFile, api.WorkspaceFileParams{WorkspaceID: ws, Path: p}, &r)
		return readFileMsg{ws: ws, path: p, content: r.Content, notShown: r.NotShown, err: err}
	}
}

// ensureTabData syncs the pane to the selected workspace: it resets state left
// from a previous workspace and fetches the current tab's data when missing.
func (m model) ensureTabData() (tea.Model, tea.Cmd) {
	ws := m.selectedWorkspaceID()
	if ws == "" {
		return m, nil
	}
	if m.projects.dataWS != ws {
		m.projects.dataWS = ws
		m.projects.wsCursor = 0
		m.projects.changes = changesState{}
		m.projects.files = filesState{}
	}
	switch m.projects.tab {
	case tabChanges:
		if m.projects.changes.files == nil && !m.projects.changes.loading && m.projects.changes.err == nil {
			m.projects.changes.loading = true
			return m, m.fetchChangedFiles(ws, m.projects.changes.against)
		}
	case tabFiles:
		if m.projects.files.entries == nil && !m.projects.files.loading && m.projects.files.err == nil {
			m.projects.files.loading = true
			return m, m.fetchListDir(ws, m.projects.files.dir)
		}
	}
	return m, nil
}

func (m model) cycleTab(d int) (tea.Model, tea.Cmd) {
	m.projects.tab = wsTab((int(m.projects.tab) + d + 3) % 3)
	return m.ensureTabData()
}

// --- pane key handling --------------------------------------------------------

func (m model) paneViewing() bool {
	return (m.projects.tab == tabChanges && m.projects.changes.viewing) ||
		(m.projects.tab == tabFiles && m.projects.files.viewing)
}

func (m model) handleProjectsPaneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.paneViewing() {
		switch {
		case key.Matches(msg, listKeys.TabNext):
			return m.cycleTab(1)
		case key.Matches(msg, listKeys.TabPrev):
			return m.cycleTab(-1)
		}
	}
	switch m.projects.tab {
	case tabChanges:
		return m.paneChangesKey(msg)
	case tabFiles:
		return m.paneFilesKey(msg)
	default:
		return m.paneSessionsKey(msg)
	}
}

func (m model) paneSessionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	ss := m.paneSessions()
	switch {
	case key.Matches(msg, projectsKeys.Up):
		m.projects.wsCursor = cursorUp(m.projects.wsCursor)
	case key.Matches(msg, projectsKeys.Down):
		m.projects.wsCursor = cursorDown(m.projects.wsCursor, len(ss))
	case key.Matches(msg, projectsKeys.Enter):
		if m.projects.wsCursor < len(ss) {
			return m.enterSession(ss[m.projects.wsCursor].ID)
		}
	}
	return m, nil
}

func (m model) paneChangesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.projects.changes
	if c.viewing {
		switch {
		case key.Matches(msg, projectsKeys.Up):
			c.scroll = max(0, c.scroll-1)
		case key.Matches(msg, projectsKeys.Down):
			c.scroll++
		case key.Matches(msg, projectsKeys.HalfUp):
			c.scroll = max(0, c.scroll-m.cardListPageStep())
		case key.Matches(msg, projectsKeys.HalfDown):
			c.scroll += m.cardListPageStep()
		}
		return m, nil
	}
	switch {
	case key.Matches(msg, projectsKeys.DiffMode):
		if c.against == "" {
			c.against = api.AgainstTarget
		} else {
			c.against = ""
		}
		c.files, c.cursor, c.err, c.loading = nil, 0, nil, true
		return m, m.fetchChangedFiles(m.projects.dataWS, c.against)
	case key.Matches(msg, projectsKeys.Up):
		c.cursor = cursorUp(c.cursor)
	case key.Matches(msg, projectsKeys.Down):
		c.cursor = cursorDown(c.cursor, len(c.files))
	case key.Matches(msg, projectsKeys.Top):
		c.cursor = 0
	case key.Matches(msg, projectsKeys.Bottom):
		c.cursor = cursorBottom(len(c.files))
	case key.Matches(msg, projectsKeys.Enter):
		if c.cursor < len(c.files) {
			f := c.files[c.cursor]
			c.viewing, c.diff, c.diffPath, c.notShown, c.scroll = true, "", f.Path, false, 0
			return m, m.fetchWorkspaceDiff(m.projects.dataWS, f, c.against)
		}
	}
	return m, nil
}

func (m model) paneFilesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := &m.projects.files
	if f.viewing {
		switch {
		case key.Matches(msg, projectsKeys.Up):
			f.scroll = max(0, f.scroll-1)
		case key.Matches(msg, projectsKeys.Down):
			f.scroll++
		case key.Matches(msg, projectsKeys.HalfUp):
			f.scroll = max(0, f.scroll-m.cardListPageStep())
		case key.Matches(msg, projectsKeys.HalfDown):
			f.scroll += m.cardListPageStep()
		}
		return m, nil
	}
	switch {
	case key.Matches(msg, projectsKeys.Up):
		f.cursor = cursorUp(f.cursor)
	case key.Matches(msg, projectsKeys.Down):
		f.cursor = cursorDown(f.cursor, len(f.entries))
	case key.Matches(msg, projectsKeys.Top):
		f.cursor = 0
	case key.Matches(msg, projectsKeys.Bottom):
		f.cursor = cursorBottom(len(f.entries))
	case key.Matches(msg, projectsKeys.Enter):
		if f.cursor < len(f.entries) {
			e := f.entries[f.cursor]
			if e.IsDir {
				f.entries, f.cursor, f.loading, f.err, f.dir = nil, 0, true, nil, e.Path
				return m, m.fetchListDir(m.projects.dataWS, e.Path)
			}
			f.viewing, f.content, f.filePath, f.notShown, f.scroll = true, "", e.Path, false, 0
			return m, m.fetchReadFile(m.projects.dataWS, e.Path)
		}
	}
	return m, nil
}

// filesUp navigates the Files tab up one directory (or does nothing at the root).
func (m model) filesUp() (tea.Model, tea.Cmd) {
	if m.projects.files.dir == "" {
		m.projects.focus = focusTree
		return m, nil
	}
	parent := path.Dir(m.projects.files.dir)
	if parent == "." {
		parent = ""
	}
	m.projects.files.entries, m.projects.files.cursor, m.projects.files.loading, m.projects.files.err, m.projects.files.dir = nil, 0, true, nil, parent
	return m, m.fetchListDir(m.projects.dataWS, parent)
}

// --- views --------------------------------------------------------------------

func (m model) projectsPaneContent(w, avail int) string {
	switch m.projects.tab {
	case tabChanges:
		return m.projectsChangesPane(w, avail)
	case tabFiles:
		return m.projectsFilesPane(w, avail)
	default:
		return m.projectsSessionPane(w, avail)
	}
}

// wsTabHeader is the tab strip for workspace row r, followed by its name and
// branch so the pane names what it shows.
func (m model) wsTabHeader(r projectsRow) string {
	active := StylePrimaryBold
	if m.projects.focus == focusPane {
		active = StyleAccentBold
	}
	names := []string{"Sessions", "Changes", "Files"}
	var parts []string
	for i, n := range names {
		if wsTab(i) == m.projects.tab {
			parts = append(parts, active.Render(n))
		} else {
			parts = append(parts, StyleDim.Render(n))
		}
	}
	ctx := r.label
	if r.branch != "" {
		ctx += "  " + r.branch
	}
	if m.projects.tab == tabChanges {
		mode := "uncommitted"
		if m.projects.changes.against == api.AgainstTarget {
			mode = "vs " + r.target
		}
		ctx += "  ·  " + mode
	}
	return strings.Join(parts, StyleDim.Render("  ")) + StyleDim.Render("   ·  "+ctx)
}

func (m model) projectsChangesPane(w, avail int) string {
	c := m.projects.changes
	if c.loading && c.files == nil {
		return dimStyle.Render("loading changes…")
	}
	if c.err != nil {
		return dimStyle.Render("error: " + c.err.Error())
	}
	if c.viewing {
		if c.notShown {
			return dimStyle.Render(c.diffPath + ": binary or too large")
		}
		return scrollView(m.highlightDiff(c.diff), c.scroll, avail, w)
	}
	if len(c.files) == 0 {
		return dimStyle.Render("no changes")
	}
	focused := m.projects.focus == focusPane
	lines := make([]string, len(c.files))
	for i, f := range c.files {
		lines[i] = truncateLine(cursorLine(changeMarker(f.Change)+" "+f.Path, i == c.cursor, focused), w)
	}
	return strings.Join(windowSpan(lines, c.cursor, c.cursor+1, avail), "\n")
}

func (m model) projectsFilesPane(w, avail int) string {
	f := m.projects.files
	if f.loading && f.entries == nil {
		return dimStyle.Render("loading…")
	}
	if f.err != nil {
		return dimStyle.Render("error: " + f.err.Error())
	}
	if f.viewing {
		if f.notShown {
			return dimStyle.Render(f.filePath + ": binary or too large")
		}
		return scrollView(m.highlightFile(f.filePath, f.content), f.scroll, avail, w)
	}
	header := dimStyle.Render("/" + f.dir)
	if len(f.entries) == 0 {
		return header + "\n" + dimStyle.Render("(empty)")
	}
	focused := m.projects.focus == focusPane
	lines := make([]string, len(f.entries))
	for i, e := range f.entries {
		name := e.Name
		if e.IsDir {
			name += "/"
		}
		lines[i] = truncateLine(cursorLine(name, i == f.cursor, focused), w)
	}
	body := strings.Join(windowSpan(lines, f.cursor, f.cursor+1, max(1, avail-1)), "\n")
	return header + "\n" + body
}

// scrollView renders content windowed to avail lines from an absolute top offset,
// each line hard-wrapped to w.
func scrollView(content string, scroll, avail, w int) string {
	var lines []string
	for _, ln := range strings.Split(content, "\n") {
		lines = append(lines, truncateLine(ln, w))
	}
	if scroll > max(0, len(lines)-1) {
		scroll = max(0, len(lines)-1)
	}
	end := min(len(lines), scroll+avail)
	return strings.Join(lines[scroll:end], "\n")
}

func changeMarker(change string) string {
	switch change {
	case "added", "untracked":
		return StyleAccentBold.Render("A")
	case "deleted":
		return StyleErrorBold.Render("D")
	case "renamed":
		return StyleSecondary.Render("R")
	default:
		return StyleSecondary.Render("M")
	}
}

func (m model) highlightDiff(s string) string {
	hl := newCodeHighlighter(m.hasDark, "diff")
	if out, ok := hl.highlight(s); ok {
		return out
	}
	return s
}

func (m model) highlightFile(name, s string) string {
	if out, ok := newCodeHighlighterForFile(m.hasDark, name).highlight(s); ok {
		return out
	}
	return s
}
