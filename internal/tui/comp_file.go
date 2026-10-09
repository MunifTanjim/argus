package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/wsscript"
)

// fileComp is a file or, with diff set, a changed file's diff, shown in the
// main pane over the component it was opened from.
type fileComp struct {
	ws, path          string
	orig              string   // diff only: the rename source
	wrap              bool     // wrap long lines instead of cutting them
	lines             []string // highlighted once on load; rendering only windows them
	diff              bool
	log               bool   // the workspace's setup log
	against           string // diff only: the Changes mode it was opened in
	rev               string // diff only: the commit sha, or "" for the working tree
	notShown, loading bool
	err               error
	scroll            int
	live              bool // log only: the setup ran at the last look, so the log refetches
	tick              int  // log only: the newest refetch tick; older ones are dropped
}

func (f fileComp) section() string           { return "file" }
func (f fileComp) raw(*ctx) bool             { return false }
func (f fileComp) spins(*ctx) bool           { return false }
func (f fileComp) fullScreen(*ctx) fullLevel { return notFull }
func (f fileComp) close(*ctx) tea.Cmd        { return nil }
func (f fileComp) offers(c *ctx) []binding   { return c.m.baseComp().offers(c) }
func (f fileComp) pageStep(c *ctx) int       { return c.m.baseComp().pageStep(c) }
func (f fileComp) layer() layer              { return fileLayer }

func (f fileComp) commands(c *ctx) []binding {
	fk := fileViewKeys
	out := []any{fk.Up, fk.Down, fk.HalfUp, fk.HalfDown, fk.Top, fk.Bottom, fk.Wrap, fk.Refresh, fk.Back}
	if f.diff {
		out = append(out, fk.NextFile, fk.PrevFile)
	}
	if c.m.inSession() && c.m.sessionInteraction() != nil {
		out = append(out, sessionKeys.FocusPrompt)
	}
	return bindingsOf(out...)
}

func (f fileComp) show(c *ctx) {
	if _, ok := c.m.openFile(); ok {
		c.replaceFile(f)
		return
	}
	c.open(f)
}

func (f fileComp) leave(c *ctx) {
	c.closeFile()
	if c.m.filesVisible() && c.m.currentWorkspace() != "" {
		c.focusOn(rightSidebar)
	}
}

// reload keeps the old content until the answer arrives.
func (f fileComp) reload(c *ctx) tea.Cmd {
	switch {
	case f.log:
		return c.m.fetchSetupLog(f.ws)
	case f.diff:
		return c.m.fetchWorkspaceDiff(f.ws, api.ChangedFile{Path: f.path, OrigPath: f.orig}, f.against, f.rev)
	}
	return c.m.fetchReadFile(f.ws, f.path)
}

// handleKey leaves the prompt focus key to a live transcript.
func (f fileComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m := c.m
	fk := fileViewKeys
	c.setFlash("")
	switch {
	case m.matches(msg, fk.Back):
		f.leave(c)
	case m.matches(msg, fk.Refresh):
		return f, f.reload(c), true
	case m.matches(msg, fk.Up):
		f.scroll = max(0, f.scroll-1)
	case m.matches(msg, fk.Down):
		f.scroll = min(f.scroll+1, f.maxScroll(c))
	case m.matches(msg, fk.HalfUp):
		f.scroll = max(0, f.scroll-m.cardListPageStep())
	case m.matches(msg, fk.HalfDown):
		f.scroll = min(f.scroll+m.cardListPageStep(), f.maxScroll(c))
	case m.matches(msg, fk.Top):
		f.scroll = 0
	case m.matches(msg, fk.Bottom):
		f.scroll = f.maxScroll(c)
	case m.matches(msg, fk.Wrap):
		f.wrap = !f.wrap
		f.scroll = min(f.scroll, f.maxScroll(c))
	case m.matches(msg, fk.NextFile):
		c.stepDiff(1)
	case m.matches(msg, fk.PrevFile):
		c.stepDiff(-1)
	case m.inSession() && m.matches(msg, sessionKeys.FocusPrompt):
		return f, nil, false
	}
	// Any other key would act on the content hidden behind the file.
	return f, nil, true
}

// maxScroll is the scroll, in source lines, that puts the last line at the
// bottom; wrapped lines count their rows.
func (f fileComp) maxScroll(c *ctx) int {
	w, h := c.m.mainSize()
	avail := fileViewHeight(h) - 1
	if !f.wrap {
		return max(0, len(f.lines)-avail)
	}
	rows := 0
	for i := len(f.lines) - 1; i >= 0; i-- {
		if rows += len(wrapLine(f.lines[i], fileViewWidth(w))); rows > avail {
			return i + 1
		}
	}
	return 0
}

func (f fileComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case wsDiffMsg:
		if f.diff && msg.ws == f.ws && msg.path == f.path && msg.against == f.against && msg.rev == f.rev {
			f.lines, f.notShown, f.err, f.loading = viewLines(c.m.highlightDiff(fileText(msg.diff))), msg.notShown, msg.err, false
		}
	case readFileMsg:
		if !f.diff && msg.ws == f.ws && msg.path == f.path {
			f.lines, f.notShown, f.err, f.loading = viewLines(c.m.highlightFile(msg.path, fileText(msg.content))), msg.notShown, msg.err, false
		}
	case setupLogMsg:
		if f.log && f.ws == msg.ws {
			f.lines, f.err, f.loading = viewLines(fileText(msg.output)), msg.err, false
			if f.live {
				f.tick++
				return f, setupLogTick(f.ws, f.tick)
			}
		}
	case setupLogTickMsg:
		if f.log && f.ws == msg.ws && f.live && msg.tick == f.tick {
			return f, c.m.fetchSetupLog(f.ws)
		}
	case projectsTreeMsg:
		// A setup that starts or ends changes the log, whose last lines only a
		// fetch after the end holds.
		if running := c.m.setupRunning(f.ws); f.log && running != f.live {
			f.live = running
			return f, c.m.fetchSetupLog(f.ws)
		}
	}
	return f, nil
}

const setupLogEvery = time.Second

func setupLogTick(ws string, tick int) tea.Cmd {
	return tea.Tick(setupLogEvery, func(time.Time) tea.Msg { return setupLogTickMsg{ws: ws, tick: tick} })
}

func (m model) setupRunning(ws string) bool {
	s := m.workspaceSetup(ws)
	return s != nil && s.State == "running"
}

const fileTabWidth = 4

// fileText expands tabs because the layout measures a tab as zero cells.
func fileText(s string) string {
	if s = wsscript.CleanOutput(s); !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			n := fileTabWidth - col%fileTabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case '\n':
			b.WriteRune(r)
			col = 0
		default:
			b.WriteRune(r)
			col += xansi.StringWidth(string(r))
		}
	}
	return b.String()
}

// view widens the content past the card column, up to maxContentWidth.
func (f fileComp) view(c *ctx, w, h int) string {
	fw := fileViewWidth(w)
	out := centerBlock(f.content(c, fw, fileViewHeight(h)), fw, w)
	// One row covers the view, so that a right-click anywhere opens its menu.
	c.hitRows(rowSpan{index: 0, top: 0, bottom: strings.Count(out, "\n") + 1})
	return out
}

func (f fileComp) content(c *ctx, w, h int) string {
	m := c.m
	switch {
	case f.err != nil:
		return truncateLine(dimStyle.Render("error: "+f.err.Error()), w)
	case f.loading:
		return dimStyle.Render("loading " + f.path + "…")
	case f.notShown:
		return dimStyle.Render(f.path + ": binary or too large")
	case f.diff && len(f.lines) == 0:
		return dimStyle.Render(f.path + ": no changes")
	case f.log && len(f.lines) == 0 && m.workspaceSetup(f.ws) != nil:
		return dimStyle.Render("no output")
	case f.log && len(f.lines) == 0:
		return dimStyle.Render("no setup run")
	}
	title := renamePath(f.orig, f.path)
	switch {
	case f.log:
		title = "setup log · " + m.workspaceLabel(f.ws)
	case f.rev != "":
		title = shortSHA(f.rev) + " · " + title
	case f.diff && f.against == api.AgainstTarget:
		title += " · vs " + m.targetOf(f.ws)
	case f.diff:
		title += " · uncommitted"
	}
	return dimStyle.Render(title) + "\n" + fileViewRows(f.lines, f.scroll, max(1, h-1), w, f.wrap)
}

func (f fileComp) footerText(c *ctx) string {
	m := c.m
	if offersKey(f.offers(c), projectsKeys.Help) {
		return m.footer(append(f.footer(c), projectsKeys.Help)...)
	}
	return m.footer(f.footer(c)...)
}

func (f fileComp) footer(*ctx) []binding {
	fk := fileViewKeys
	b := []binding{helpAs(fk.Up, "scroll"), helpAs(fk.HalfDown, "page"), helpAs(fk.Bottom, "ends")}
	if f.diff {
		b = append(b, fk.NextFile)
	}
	return append(b, fk.Wrap, helpAs(fk.Refresh, "reload"), helpAs(fk.Back, "close"))
}

// openFile is the file open over the main pane's component, under the spawn
// flow or the live screen, or not.
func (m model) openFile() (fileComp, bool) {
	if i := m.fileAt(); i >= 0 {
		return m.main[i].(fileComp), true
	}
	return fileComp{}, false
}

func (m model) fileAt() int {
	i := len(m.main) - 1
	for i > 0 && m.main[i].layer().coversFile() {
		i--
	}
	if i >= 0 {
		if _, ok := m.main[i].(fileComp); ok {
			return i
		}
	}
	return -1
}

// updateFile gives a result for a file to the open file, which drops one for
// another file.
func (m model) updateFile(msg tea.Msg) (tea.Model, tea.Cmd) {
	f, ok := m.openFile()
	if !ok {
		return m, nil
	}
	c := &ctx{m: &m}
	comp, cmd := f.update(c, msg)
	m.main = m.main.replaceAt(m.fileAt(), comp)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

func (f fileComp) wheel(c *ctx, d int) (component, tea.Cmd) {
	f.scroll = max(0, min(f.scroll+d, f.maxScroll(c)))
	return f, nil
}

func (f fileComp) menu(*ctx) []binding {
	if f.diff {
		return []binding{fileViewKeys.Wrap, fileViewKeys.NextFile, fileViewKeys.PrevFile}
	}
	return []binding{fileViewKeys.Wrap}
}
