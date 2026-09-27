package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

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

// stepDiff opens the file d places from the open diff, in the list it came from.
func (m model) stepDiff(d int) (tea.Model, tea.Cmd) {
	f, _ := m.openFile()
	c := &m.right.changes
	switch {
	case !f.diff:
	case f.rev != "" && c.commit != nil && c.commit.SHA == f.rev:
		if i := c.commitCursor + d; i >= 0 && i < len(c.commitFiles) {
			c.commitCursor = i
			return m.openDiff(c.commitFiles[i], f.rev)
		}
	case f.rev == "":
		if i := c.cursor + d; i >= 0 && i < len(c.files) {
			c.cursor = i
			return m.openDiff(c.files[i], "")
		}
	}
	return m, nil
}

func (m model) openDiff(f api.ChangedFile, rev string) (tea.Model, tea.Cmd) {
	c := &ctx{m: &m}
	cmd := m.right.changes.openDiff(c, f, rev)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

func (m model) paneHeadStyle() lipgloss.Style {
	if m.focused == mainPane {
		return StyleAccentBold
	}
	return StylePrimaryBold
}

func (m model) wsHeader(r projectsRow) string {
	out := m.paneHeadStyle().Render(r.label)
	if r.branch != "" {
		out += StyleDim.Render("  " + r.branch)
	}
	if r.target != "" && r.target != r.branch {
		out += StyleDim.Render(" → " + r.target)
	}
	return out
}

func (m model) targetOf(wsID string) string {
	w, _ := m.findWorkspace(wsID)
	return w.TargetBranch
}

func changeRow(f api.ChangedFile, sel, focused bool, tw int) string {
	path := truncateLeft(renamePath(f.OrigPath, f.Path), max(1, tw-2))
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

func renamePath(orig, p string) string {
	if orig == "" {
		return p
	}
	return orig + " → " + p
}

// truncateLeft cuts from the left so a path keeps its file name.
func truncateLeft(s string, width int) string {
	if n := lipgloss.Width(s); n > width {
		return "…" + xansi.TruncateLeft(s, n-width+1, "")
	}
	return s
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
