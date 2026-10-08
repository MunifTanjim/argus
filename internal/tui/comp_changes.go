package tui

import (
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

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
		ch, cmd := ch.enter(c)
		return ch, cmd, true
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
		return ch, ch.enterCommitFile(c), true
	default:
		return ch, nil, false
	}
	return ch, nil, true
}

func (ch changesComp) enter(c *ctx) (changesComp, tea.Cmd) {
	switch {
	case ch.cursor < len(ch.files):
		return ch, ch.openDiff(c, ch.files[ch.cursor], "")
	case ch.cursor < len(ch.files)+len(ch.commits):
		return ch.openCommit(c, ch.commits[ch.cursor-len(ch.files)])
	}
	return ch, nil
}

func (ch changesComp) enterCommitFile(c *ctx) tea.Cmd {
	if ch.commitCursor < len(ch.commitFiles) {
		return ch.openDiff(c, ch.commitFiles[ch.commitCursor], ch.commit.SHA)
	}
	return nil
}

func (ch changesComp) click(c *ctx, t hitTarget, focused bool) (component, tea.Cmd) {
	if ch.commit != nil {
		if focused && t.index == ch.commitCursor {
			return ch, ch.enterCommitFile(c)
		}
		ch.commitCursor = t.index
		return ch, nil
	}
	if focused && t.index == ch.cursor {
		ch, cmd := ch.enter(c)
		return ch, cmd
	}
	ch.cursor = t.index
	return ch, nil
}

func (ch changesComp) wheel(_ *ctx, d int) (component, tea.Cmd) {
	if ch.commit != nil {
		ch.commitCursor = cursorBy(ch.commitCursor, d, len(ch.commitFiles))
	} else {
		ch.cursor = cursorBy(ch.cursor, d, len(ch.files)+len(ch.commits))
	}
	return ch, nil
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
	switch {
	case ch.commit != nil:
		return []binding{helpAs(k.Enter, "diff")}
	case ch.cursor >= len(ch.files) && ch.cursor < len(ch.files)+len(ch.commits):
		return []binding{k.SideTabNext, helpAs(k.Enter, "files"), ch.diffModeKey(c)}
	}
	return []binding{k.SideTabNext, helpAs(k.Enter, "diff"), ch.diffModeKey(c)}
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
		return ch.commitFilesView(c, w, h, focused)
	}
	gutter := strings.Repeat(" ", screenMargin)
	tw := max(1, w-screenMargin)
	note := func(s string) string { return gutter + truncateLine(dimStyle.Render(s), tw) }
	l := itemLines{key: "changes:" + ch.ws}
	switch {
	case ch.err != nil:
		l.text(note("error: " + ch.err.Error()))
	case ch.files == nil:
		l.text(note("loading…"))
	case len(ch.files) == 0:
		l.text(note("no changes"))
	}
	for i, f := range ch.files {
		l.add(i, changeRow(f, i == ch.cursor, focused, tw))
	}
	l.text("")
	l.text(note(ch.commitsHeader(c)))
	switch {
	case ch.commitsErr != nil:
		l.text(note("error: " + ch.commitsErr.Error()))
	case ch.commits == nil:
		l.text(note("loading…"))
	case len(ch.commits) == 0:
		l.text(note("no commits"))
	}
	for j, cm := range ch.commits {
		l.add(len(ch.files)+j, commitRow(cm, len(ch.files)+j == ch.cursor, focused, tw))
	}
	return note(ch.header(c)) + "\n" + strings.Join(l.window(c.below(1), ch.cursor, max(1, h-1)), "\n")
}

func (ch changesComp) commitFilesView(c *ctx, w, h int, focused bool) string {
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
	l := itemLines{key: "changes-commit:" + ch.ws}
	for i, f := range ch.commitFiles {
		l.add(i, changeRow(f, i == ch.commitCursor, focused, tw))
	}
	return head + strings.Join(l.window(c.below(1), ch.commitCursor, max(1, h-1)), "\n")
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

func (ch changesComp) hoverItem(*ctx) (hoverItem, bool) {
	if ch.commit != nil {
		if ch.commitCursor >= len(ch.commitFiles) {
			return hoverItem{}, false
		}
		f := ch.commitFiles[ch.commitCursor]
		return changeHover("commit:"+ch.ws+":"+ch.commit.SHA+":"+f.Path, ch.commitCursor, f), true
	}
	if ch.cursor < len(ch.files) {
		f := ch.files[ch.cursor]
		return changeHover("change:"+ch.ws+":"+ch.against+":"+f.Path, ch.cursor, f), true
	}
	j := ch.cursor - len(ch.files)
	if j >= len(ch.commits) {
		return hoverItem{}, false
	}
	cm := ch.commits[j]
	it := hoverItem{key: "commit:" + ch.ws + ":" + cm.SHA, index: ch.cursor, title: cm.Short}
	it.add("SHA", cm.SHA)
	it.add("Subject", cm.Subject)
	it.add("Author", cm.Author)
	if cm.UnixSec > 0 {
		it.add("Date", dateTime(time.Unix(cm.UnixSec, 0)))
	}
	return it, true
}

func changeHover(key string, index int, f api.ChangedFile) hoverItem {
	it := hoverItem{key: key, index: index, title: path.Base(f.Path)}
	it.add("Path", f.Path)
	it.add("From", f.OrigPath)
	it.add("Change", f.Change)
	var state []string
	if f.Staged {
		state = append(state, "staged")
	}
	if f.Unstaged {
		state = append(state, "unstaged")
	}
	it.add("State", strings.Join(state, ", "))
	return it
}
