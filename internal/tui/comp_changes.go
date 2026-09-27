package tui

import (
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

// changesComp is the right sidebar's Changes tab: one workspace's changed files
// and its commits since the target branch. A diff opens in the main pane.
type changesComp struct {
	ws      string
	against string // "" = uncommitted; api.AgainstTarget = vs the target branch
	gen     int    // bumped by reload, so a list requested before it is dropped
	files   []api.ChangedFile
	cursor  int // over the changed files, then the commits
	loading bool
	err     error

	commits        []api.Commit
	commitsLoading bool
	commitsErr     error

	commit       *api.Commit // the commit drilled into; nil shows the list
	commitFiles  []api.ChangedFile
	commitCursor int
	commitErr    error
}

func (ch changesComp) section() string           { return "changes" }
func (ch changesComp) raw(*ctx) bool             { return false }
func (ch changesComp) spins(*ctx) bool           { return false }
func (ch changesComp) fullScreen(*ctx) fullLevel { return notFull }
func (ch changesComp) close(*ctx) tea.Cmd        { return nil }
func (ch changesComp) offers(c *ctx) []binding   { return c.m.baseComp().offers(c) }
func (ch changesComp) pageStep(c *ctx) int       { return c.m.baseComp().pageStep(c) }
func (ch changesComp) layer() layer              { return baseLayer }

// commands leaves out the diff mode in a commit's files, and collapse, which
// only leaves a commit, outside them.
func (ch changesComp) commands(*ctx) []binding {
	drop := projectsKeys.Left
	if ch.commit != nil {
		drop = projectsKeys.DiffMode
	}
	return slices.DeleteFunc(slices.Clone(sectionLists[ch.section()].own), func(b binding) bool { return b.name == drop.name })
}

// The next sync after reload fetches everything again.
func (ch *changesComp) reload() { *ch = changesComp{ws: ch.ws, against: ch.against, gen: ch.gen + 1} }

// refresh keeps the old lists on screen, so the cursor and the open commit
// hold. gen drops answers to requests from before.
func (ch changesComp) refresh(c *ctx) (changesComp, tea.Cmd) {
	ch.gen++
	ch.loading, ch.err, ch.commitsLoading, ch.commitsErr = true, nil, true, nil
	cmds := []tea.Cmd{c.m.fetchChangedFiles(ch.ws, ch.against, ch.gen), c.m.fetchCommits(ch.ws, ch.gen)}
	if ch.commit != nil {
		ch.commitErr = nil
		cmds = append(cmds, c.m.fetchCommitFiles(ch.ws, ch.commit.SHA))
	}
	return ch, tea.Batch(cmds...)
}

func (ch changesComp) load(c *ctx) (changesComp, tea.Cmd) {
	var cmds []tea.Cmd
	if ch.files == nil && !ch.loading && ch.err == nil {
		ch.loading = true
		cmds = append(cmds, c.m.fetchChangedFiles(ch.ws, ch.against, ch.gen))
	}
	if ch.commits == nil && !ch.commitsLoading && ch.commitsErr == nil {
		ch.commitsLoading = true
		cmds = append(cmds, c.m.fetchCommits(ch.ws, ch.gen))
	}
	return ch, tea.Batch(cmds...)
}

func (ch changesComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m := c.m
	k := projectsKeys
	switch {
	case m.matches(msg, k.Back):
		if ch.commit != nil {
			ch.commit = nil
		} else {
			c.focusOn(mainPane)
		}
		return ch, nil, true
	case m.matches(msg, k.Refresh):
		ch, cmd := ch.refresh(c)
		return ch, cmd, true
	}
	if ch.commit != nil {
		return ch.commitFilesKey(c, msg)
	}
	n := len(ch.files) + len(ch.commits)
	switch {
	case m.matches(msg, k.DiffMode):
		if ch.against == "" && m.targetOf(ch.ws) == "" {
			c.setFlash("no target branch · " + m.keyTextOn("project-tree", projectsKeys.Target) + " in the tree sets one")
			return ch, nil, true
		}
		if ch.against == "" {
			ch.against = api.AgainstTarget
		} else {
			ch.against = ""
		}
		// The old files stay until the new list arrives, so the cursor keeps its row.
		ch.loading, ch.err = true, nil
		return ch, m.fetchChangedFiles(ch.ws, ch.against, ch.gen), true
	case m.matches(msg, k.Up):
		ch.cursor = cursorUp(ch.cursor)
	case m.matches(msg, k.Down):
		ch.cursor = cursorDown(ch.cursor, n)
	case m.matches(msg, k.Top):
		ch.cursor = 0
	case m.matches(msg, k.Bottom):
		ch.cursor = cursorBottom(n)
	case m.matches(msg, k.HalfUp):
		ch.cursor = max(0, ch.cursor-m.cardListPageStep())
	case m.matches(msg, k.HalfDown):
		ch.cursor = min(cursorBottom(n), ch.cursor+m.cardListPageStep())
	case m.matches(msg, k.Enter, k.Right):
		switch {
		case ch.cursor < len(ch.files):
			return ch, ch.openDiff(c, ch.files[ch.cursor], ""), true
		case ch.cursor < n:
			ch, cmd := ch.openCommit(c, ch.commits[ch.cursor-len(ch.files)])
			return ch, cmd, true
		}
	default:
		return ch, nil, false
	}
	return ch, nil, true
}

// commitFilesKey handles a drilled-in commit's file list; collapse goes back
// like back does.
func (ch changesComp) commitFilesKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m := c.m
	k := projectsKeys
	n := len(ch.commitFiles)
	switch {
	case m.matches(msg, k.Left):
		ch.commit = nil
	case m.matches(msg, k.Up):
		ch.commitCursor = cursorUp(ch.commitCursor)
	case m.matches(msg, k.Down):
		ch.commitCursor = cursorDown(ch.commitCursor, n)
	case m.matches(msg, k.Top):
		ch.commitCursor = 0
	case m.matches(msg, k.Bottom):
		ch.commitCursor = cursorBottom(n)
	case m.matches(msg, k.HalfUp):
		ch.commitCursor = max(0, ch.commitCursor-m.cardListPageStep())
	case m.matches(msg, k.HalfDown):
		ch.commitCursor = min(cursorBottom(n), ch.commitCursor+m.cardListPageStep())
	case m.matches(msg, k.Enter, k.Right):
		if ch.commitCursor < n {
			return ch, ch.openDiff(c, ch.commitFiles[ch.commitCursor], ch.commit.SHA), true
		}
	default:
		return ch, nil, false
	}
	return ch, nil, true
}

func (ch changesComp) openDiff(c *ctx, f api.ChangedFile, rev string) tea.Cmd {
	fileComp{ws: ch.ws, path: f.Path, orig: f.OrigPath, diff: true, against: ch.against, rev: rev, loading: true}.show(c)
	c.focusOn(mainPane)
	return c.m.fetchWorkspaceDiff(ch.ws, f, ch.against, rev)
}

func (ch changesComp) openCommit(c *ctx, cm api.Commit) (changesComp, tea.Cmd) {
	ch.commit, ch.commitFiles, ch.commitCursor, ch.commitErr = &cm, nil, 0, nil
	return ch, c.m.fetchCommitFiles(ch.ws, cm.SHA)
}

func (ch changesComp) update(_ *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case changedFilesMsg:
		if msg.ws == ch.ws && msg.against == ch.against && msg.gen == ch.gen {
			had, prev := ch.files != nil, len(ch.files)
			ch.loading, ch.err, ch.files = false, msg.err, msg.files
			if ch.files == nil {
				ch.files = []api.ChangedFile{}
			}
			// The commits follow the files in the cursor's index, so a cursor on a
			// commit moves with the file count. A cursor at the top before the
			// first files stays at the top.
			if ch.cursor >= prev && len(ch.commits) > 0 && (had || ch.cursor > 0) {
				ch.cursor += len(ch.files) - prev
			} else {
				ch.cursor = min(ch.cursor, cursorBottom(len(ch.files)))
			}
		}
	case commitsMsg:
		if msg.ws == ch.ws && msg.gen == ch.gen {
			ch.commitsLoading, ch.commitsErr, ch.commits = false, msg.err, msg.commits
			if ch.commits == nil {
				ch.commits = []api.Commit{}
			}
			ch.cursor = min(ch.cursor, cursorBottom(len(ch.files)+len(ch.commits)))
		}
	case commitFilesMsg:
		if msg.ws == ch.ws && ch.commit != nil && msg.sha == ch.commit.SHA {
			ch.commitErr, ch.commitFiles = msg.err, msg.files
			if ch.commitFiles == nil {
				ch.commitFiles = []api.ChangedFile{}
			}
			ch.commitCursor = min(ch.commitCursor, cursorBottom(len(ch.commitFiles)))
		}
	}
	return ch, nil
}

func (ch changesComp) footerText(c *ctx) string { return c.m.right.footerText(c) }

func (ch changesComp) footer(c *ctx) []binding {
	k := projectsKeys
	esc := helpAs(k.Back, sidebarEscDesc(c))
	switch {
	case ch.commit != nil:
		return []binding{k.Up, helpAs(k.Enter, "diff"), helpAs(k.Back, "back")}
	case ch.cursor >= len(ch.files) && ch.cursor < len(ch.files)+len(ch.commits):
		return []binding{k.Up, k.SideTabNext, helpAs(k.Enter, "files"), ch.diffModeKey(c), esc}
	}
	return []binding{k.Up, k.SideTabNext, helpAs(k.Enter, "diff"), ch.diffModeKey(c), esc}
}

// diffModeKey labels the diff-mode key with the mode it switches to; with no
// target it only hints, so it is not offered.
func (ch changesComp) diffModeKey(c *ctx) binding {
	k := projectsKeys.DiffMode
	switch {
	case ch.against == api.AgainstTarget:
		return helpAs(k, "uncommitted")
	case c.m.targetOf(ch.ws) == "":
		k.SetEnabled(false)
		return k
	}
	return helpAs(k, "vs "+c.m.targetOf(ch.ws))
}

func (ch changesComp) view(c *ctx, w, h int) string {
	focused := c.m.focused == rightSidebar
	if ch.commit != nil {
		return ch.commitFilesView(w, h, focused)
	}
	gutter := strings.Repeat(" ", screenMargin)
	tw := max(1, w-screenMargin)
	note := func(s string) string { return gutter + truncateLine(dimStyle.Render(s), tw) }
	var lines []string
	curLine := 0
	switch {
	case ch.err != nil:
		lines = append(lines, note("error: "+ch.err.Error()))
	case ch.files == nil:
		lines = append(lines, note("loading…"))
	case len(ch.files) == 0:
		lines = append(lines, note("no changes"))
	}
	for i, f := range ch.files {
		if i == ch.cursor {
			curLine = len(lines)
		}
		lines = append(lines, changeRow(f, i == ch.cursor, focused, tw))
	}
	lines = append(lines, "", note(ch.commitsHeader(c)))
	switch {
	case ch.commitsErr != nil:
		lines = append(lines, note("error: "+ch.commitsErr.Error()))
	case ch.commits == nil:
		lines = append(lines, note("loading…"))
	case len(ch.commits) == 0:
		lines = append(lines, note("no commits"))
	}
	for j, cm := range ch.commits {
		sel := len(ch.files)+j == ch.cursor
		if sel {
			curLine = len(lines)
		}
		lines = append(lines, commitRow(cm, sel, focused, tw))
	}
	return note(ch.header(c)) + "\n" + strings.Join(windowSpan(lines, curLine, curLine+1, max(1, h-1)), "\n")
}

func (ch changesComp) commitFilesView(w, h int, focused bool) string {
	gutter := strings.Repeat(" ", screenMargin)
	tw := max(1, w-screenMargin)
	head := gutter + truncateLine(StyleSecondary.Render(ch.commit.Short)+" "+ch.commit.Subject, tw) + "\n"
	switch {
	case ch.commitErr != nil:
		return head + gutter + truncateLine(dimStyle.Render("error: "+ch.commitErr.Error()), tw)
	case ch.commitFiles == nil:
		return head + gutter + dimStyle.Render("loading…")
	case len(ch.commitFiles) == 0:
		return head + gutter + dimStyle.Render("no files")
	}
	rows := make([]string, len(ch.commitFiles))
	for i, f := range ch.commitFiles {
		rows[i] = changeRow(f, i == ch.commitCursor, focused, tw)
	}
	return head + strings.Join(windowSpan(rows, ch.commitCursor, ch.commitCursor+1, max(1, h-1)), "\n")
}

func (ch changesComp) header(c *ctx) string {
	head := "UNCOMMITTED"
	if ch.against == api.AgainstTarget {
		head = "CHANGES · vs " + c.m.targetOf(ch.ws)
	}
	if ch.files != nil {
		head += " · " + strconv.Itoa(len(ch.files))
		if ch.loading {
			head += " · loading…"
		}
	}
	return head
}

func (ch changesComp) commitsHeader(c *ctx) string {
	head := "COMMITS"
	if t := c.m.targetOf(ch.ws); t != "" {
		head += " · vs " + t
	}
	if ch.commits != nil {
		head += " · " + strconv.Itoa(len(ch.commits))
	}
	return head
}
