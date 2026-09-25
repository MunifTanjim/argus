package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

// --- fetch commands & messages ------------------------------------------------

func (m model) fetchChangedFiles(ws, against string, gen int) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.ChangedFilesResult
		err := client.Call(api.MethodWorkspaceChangedFiles, api.WorkspaceRef{WorkspaceID: ws, Against: against}, &r)
		return changedFilesMsg{ws: ws, against: against, gen: gen, files: r.Files, err: err}
	}
}

func (m model) fetchWorkspaceDiff(ws string, f api.ChangedFile, against, rev string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.WorkspaceDiffResult
		params := api.WorkspaceFileParams{WorkspaceID: ws, Path: f.Path, OrigPath: f.OrigPath, Against: against, Rev: rev}
		err := client.Call(api.MethodWorkspaceDiff, params, &r)
		return wsDiffMsg{ws: ws, path: f.Path, against: against, rev: rev, diff: r.Diff, notShown: r.NotShown, err: err}
	}
}

func (m model) fetchCommits(ws string, gen int) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.CommitsResult
		err := client.Call(api.MethodWorkspaceCommits, api.WorkspaceRef{WorkspaceID: ws}, &r)
		return commitsMsg{ws: ws, gen: gen, commits: r.Commits, err: err}
	}
}

func (m model) fetchCommitFiles(ws, sha string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.ChangedFilesResult
		err := client.Call(api.MethodWorkspaceCommitFiles, api.WorkspaceCommitParams{WorkspaceID: ws, SHA: sha}, &r)
		return commitFilesMsg{ws: ws, sha: sha, files: r.Files, err: err}
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

// syncPane resets the pane's session cursor when the selected workspace changes.
func (m model) syncPane() (tea.Model, tea.Cmd) {
	if ws := m.selectedWorkspaceID(); ws != "" && m.projects.dataWS != ws {
		m.projects.dataWS = ws
		m.projects.wsCursor = 0
	}
	return m, nil
}

// --- pane key handling --------------------------------------------------------

func (m model) handleProjectsPaneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := projectsKeys
	if key.Matches(msg, k.New, k.Rename, k.Hide, k.Pin, k.Target, k.ForceRemove) {
		m.flash = "manage keys work in the tree · esc to go there"
		return m, nil
	}
	if mm, cmd, ok := m.handleFileViewKey(msg); ok {
		return mm, cmd
	}
	return m.paneSessionsKey(msg)
}

func (m model) paneSessionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	ss := m.paneSessions()
	switch {
	case key.Matches(msg, projectsKeys.Up):
		m.projects.wsCursor = cursorUp(m.projects.wsCursor)
	case key.Matches(msg, projectsKeys.Down):
		m.projects.wsCursor = cursorDown(m.projects.wsCursor, len(ss))
	case key.Matches(msg, projectsKeys.Top):
		m.projects.wsCursor = 0
	case key.Matches(msg, projectsKeys.Bottom):
		m.projects.wsCursor = cursorBottom(len(ss))
	case key.Matches(msg, projectsKeys.HalfUp):
		m.projects.wsCursor = max(0, m.projects.wsCursor-m.cardListPageStep())
	case key.Matches(msg, projectsKeys.HalfDown):
		m.projects.wsCursor = min(cursorBottom(len(ss)), m.projects.wsCursor+m.cardListPageStep())
	case key.Matches(msg, listKeys.Jump):
		if m.projects.wsCursor < len(ss) {
			return m.jumpTo(ss[m.projects.wsCursor])
		}
	case key.Matches(msg, projectsKeys.Enter):
		if m.projects.wsCursor < len(ss) {
			return m.enterSession(ss[m.projects.wsCursor].ID)
		}
	case key.Matches(msg, listKeys.Kill):
		if m.projects.wsCursor < len(ss) {
			s := ss[m.projects.wsCursor]
			if refusal := killRefusal(s); refusal != "" {
				m.flash = refusal
				return m, nil
			}
			m.projects.pendingKill = s.ID
		}
	}
	return m, nil
}

// --- Changes tab --------------------------------------------------------------

// reload drops both lists and closes a drilled-in commit, so the next sync
// fetches everything again.
func (c *changesState) reload() { *c = changesState{ws: c.ws, against: c.against, gen: c.gen + 1} }

// changesKey handles the sidebar's Changes tab; the lists load in syncSidebar.
func (m model) changesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.projects.changes
	k := projectsKeys
	if key.Matches(msg, k.Refresh) {
		c.reload()
		return m, nil
	}
	if c.commit != nil {
		return m.commitFilesKey(msg)
	}
	n := len(c.files) + len(c.commits)
	switch {
	case key.Matches(msg, k.DiffMode):
		if c.against == "" {
			c.against = api.AgainstTarget
		} else {
			c.against = ""
		}
		// The old files stay until the new list arrives, so the cursor keeps its row.
		c.loading, c.err = true, nil
		return m, m.fetchChangedFiles(c.ws, c.against, c.gen)
	case key.Matches(msg, k.Up):
		c.cursor = cursorUp(c.cursor)
	case key.Matches(msg, k.Down):
		c.cursor = cursorDown(c.cursor, n)
	case key.Matches(msg, k.Top):
		c.cursor = 0
	case key.Matches(msg, k.Bottom):
		c.cursor = cursorBottom(n)
	case key.Matches(msg, k.HalfUp):
		c.cursor = max(0, c.cursor-m.cardListPageStep())
	case key.Matches(msg, k.HalfDown):
		c.cursor = min(cursorBottom(n), c.cursor+m.cardListPageStep())
	case key.Matches(msg, k.Enter, k.Right):
		switch {
		case c.cursor < len(c.files):
			return m.openDiff(c.files[c.cursor], "")
		case c.cursor < n:
			return m.openCommit(c.commits[c.cursor-len(c.files)])
		}
	}
	return m, nil
}

// commitFilesKey handles a drilled-in commit's file list. esc comes here from
// handleFilesKey; h goes back too.
func (m model) commitFilesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.projects.changes
	k := projectsKeys
	n := len(c.commitFiles)
	switch {
	case key.Matches(msg, k.Left):
		c.commit = nil
	case key.Matches(msg, k.Up):
		c.commitCursor = cursorUp(c.commitCursor)
	case key.Matches(msg, k.Down):
		c.commitCursor = cursorDown(c.commitCursor, n)
	case key.Matches(msg, k.Top):
		c.commitCursor = 0
	case key.Matches(msg, k.Bottom):
		c.commitCursor = cursorBottom(n)
	case key.Matches(msg, k.HalfUp):
		c.commitCursor = max(0, c.commitCursor-m.cardListPageStep())
	case key.Matches(msg, k.HalfDown):
		c.commitCursor = min(cursorBottom(n), c.commitCursor+m.cardListPageStep())
	case key.Matches(msg, k.Enter, k.Right):
		if c.commitCursor < n {
			return m.openDiff(c.commitFiles[c.commitCursor], c.commit.SHA)
		}
	}
	return m, nil
}

func (m model) openDiff(f api.ChangedFile, rev string) (tea.Model, tea.Cmd) {
	c := m.projects.changes
	m.projects.fileView = fileViewState{ws: c.ws, path: f.Path, diff: true, against: c.against, rev: rev, loading: true}
	m.projects.focus = focusPane
	return m, m.fetchWorkspaceDiff(c.ws, f, c.against, rev)
}

func (m model) openCommit(cm api.Commit) (tea.Model, tea.Cmd) {
	c := &m.projects.changes
	c.commit, c.commitFiles, c.commitCursor, c.commitErr = &cm, nil, 0, nil
	return m, m.fetchCommitFiles(c.ws, cm.SHA)
}

// --- views --------------------------------------------------------------------

func (m model) wsHeader(r projectsRow) string {
	title := StylePrimaryBold
	if m.projects.focus == focusPane {
		title = StyleAccentBold
	}
	out := title.Render(r.label)
	if r.branch != "" {
		out += StyleDim.Render("  " + r.branch)
	}
	return out
}

// changesMode names what the Changes list compares against.
func (m model) changesMode() string {
	if m.projects.changes.against != api.AgainstTarget {
		return "uncommitted"
	}
	w, _ := m.findWorkspace(m.projects.changes.ws)
	return "vs " + w.TargetBranch
}

// changesView is the Changes tab body: the diff mode and the changed files, then
// the commits since the target, or a drilled-in commit's files.
func (m model) changesView(w, h int, focused bool) string {
	c := m.projects.changes
	if c.commit != nil {
		return m.commitFilesView(w, h, focused)
	}
	gutter := strings.Repeat(" ", screenMargin)
	tw := max(1, w-screenMargin)
	note := func(s string) string { return gutter + truncateLine(dimStyle.Render(s), tw) }
	lines := []string{note(m.changesMode())}
	curLine := 0
	switch {
	case c.err != nil:
		lines = append(lines, note("error: "+c.err.Error()))
	case c.files == nil:
		lines = append(lines, note("loading…"))
	case len(c.files) == 0:
		lines = append(lines, note("no changes"))
	}
	for i, f := range c.files {
		if i == c.cursor {
			curLine = len(lines)
		}
		lines = append(lines, changeRow(f, i == c.cursor, focused, tw))
	}
	lines = append(lines, "", note(m.commitsHeader()))
	switch {
	case c.commitsErr != nil:
		lines = append(lines, note("error: "+c.commitsErr.Error()))
	case c.commits == nil:
		lines = append(lines, note("loading…"))
	case len(c.commits) == 0:
		lines = append(lines, note("no commits"))
	}
	for j, cm := range c.commits {
		sel := len(c.files)+j == c.cursor
		if sel {
			curLine = len(lines)
		}
		lines = append(lines, commitRow(cm, sel, focused, tw))
	}
	return strings.Join(windowSpan(lines, curLine, curLine+1, h), "\n")
}

// commitFilesView is a drilled-in commit: its sha and subject, then its files.
func (m model) commitFilesView(w, h int, focused bool) string {
	c := m.projects.changes
	gutter := strings.Repeat(" ", screenMargin)
	tw := max(1, w-screenMargin)
	head := gutter + truncateLine(StyleSecondary.Render(c.commit.Short)+" "+c.commit.Subject, tw) + "\n"
	switch {
	case c.commitErr != nil:
		return head + gutter + truncateLine(dimStyle.Render("error: "+c.commitErr.Error()), tw)
	case c.commitFiles == nil:
		return head + gutter + dimStyle.Render("loading…")
	case len(c.commitFiles) == 0:
		return head + gutter + dimStyle.Render("no files")
	}
	rows := make([]string, len(c.commitFiles))
	for i, f := range c.commitFiles {
		rows[i] = changeRow(f, i == c.commitCursor, focused, tw)
	}
	return head + strings.Join(windowSpan(rows, c.commitCursor, c.commitCursor+1, max(1, h-1)), "\n")
}

// commitsHeader heads the COMMITS section with the target it counts from.
func (m model) commitsHeader() string {
	if w, _ := m.findWorkspace(m.projects.changes.ws); w.TargetBranch != "" {
		return "COMMITS · vs " + w.TargetBranch
	}
	return "COMMITS"
}

func changeRow(f api.ChangedFile, sel, focused bool, tw int) string {
	path := truncateLeft(f.Path, max(1, tw-2))
	if sel && focused {
		path = cursorStyle.Render(path)
	}
	return sideMarker(sel, focused) + changeMarker(f.Change) + " " + path
}

func commitRow(cm api.Commit, sel, focused bool, tw int) string {
	subject := cm.Subject
	if sel && focused {
		subject = cursorStyle.Render(subject)
	}
	return sideMarker(sel, focused) + truncateLine(StyleSecondary.Render(cm.Short)+" "+subject, tw)
}

// truncateLeft cuts from the left so a path keeps its file name.
func truncateLeft(s string, width int) string {
	if n := lipgloss.Width(s); n > width {
		return "…" + xansi.TruncateLeft(s, n-width+1, "")
	}
	return s
}

// scrollView windows lines to avail rows from scroll, truncating each to w.
func scrollView(lines []string, scroll, avail, w int) string {
	scroll = min(scroll, cursorBottom(len(lines)))
	end := min(len(lines), scroll+avail)
	out := make([]string, 0, end-scroll)
	for _, ln := range lines[scroll:end] {
		out = append(out, truncateLine(ln, w))
	}
	return strings.Join(out, "\n")
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
