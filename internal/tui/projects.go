package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/wsscript"
)

type projInputMode int

const (
	pmNone projInputMode = iota
	pmRename
	pmFilter
)

type projRowKind int

const (
	rowNode projRowKind = iota
	rowProject
	rowWorkspace
	rowHome
)

const homeRowID = "home"

func homeRow() projectsRow { return projectsRow{kind: rowHome, id: homeRowID} }

// projectsRow is one flattened line of the tree, honoring the collapsed set.
type projectsRow struct {
	kind    projRowKind
	depth   int
	id      string // node id, composite project id, or composite workspace id
	label   string
	branch  string   // workspace only
	plain   bool     // workspace only: the directory of a plain project
	target  string   // workspace only: resolved target branch
	ws      []string // workspace ids under this row (itself for a workspace row)
	isGone  bool
	gitErr  bool // project only: git failed on it this list
	isMain  bool
	hidden  bool   // project only
	pinned  bool   // project only
	hasKids bool   // node/project: whether it can collapse
	setup   string // workspace only: "running" or "failed"; "" otherwise
}

func (m model) fetchProjects() tea.Cmd { return m.left.tree.fetch(m.client) }

func (m *model) loadProjects() tea.Cmd { return m.left.tree.load(m.client) }

func buildProjectRows(st projectTreeComp) []projectsRow {
	q := strings.ToLower(st.filter)
	folded := func(id string) bool { return q == "" && st.collapsed[id] }
	var order []string
	byNode := map[string][]api.ProjectNode{}
	label := map[string]string{}
	for _, p := range st.data {
		if (p.Hidden && !st.showHidden) || (p.IsGone && !st.showGone) {
			continue
		}
		p.Workspaces = visibleWorkspaces(p.Workspaces, st.showGone)
		if q != "" {
			var ok bool
			if p, ok = matchProject(p, q); !ok {
				continue
			}
		}
		if _, ok := byNode[p.NodeID]; !ok {
			order = append(order, p.NodeID)
		}
		byNode[p.NodeID] = append(byNode[p.NodeID], p)
		label[p.NodeID] = projNodeLabel(p)
	}
	sort.Slice(order, func(i, j int) bool {
		if label[order[i]] != label[order[j]] {
			return label[order[i]] < label[order[j]]
		}
		return order[i] < order[j]
	})
	// A single node adds a tree level with nothing to choose between.
	single := len(order) == 1
	var rows []projectsRow
	for _, nid := range order {
		depth := 0
		if !single {
			var ws []string
			for _, p := range byNode[nid] {
				ws = append(ws, workspaceIDs(p)...)
			}
			rows = append(rows, projectsRow{kind: rowNode, id: nid, label: label[nid], ws: ws, hasKids: true})
			if folded(nid) {
				continue
			}
			depth = 1
		}
		for _, p := range byNode[nid] {
			rows = append(rows, projectsRow{
				kind: rowProject, depth: depth, id: p.ID, label: p.Name, ws: workspaceIDs(p),
				isGone: p.IsGone, gitErr: p.Error != "", hidden: p.Hidden, pinned: p.Pinned, hasKids: len(p.Workspaces) > 0,
			})
			if folded(p.ID) {
				continue
			}
			for _, w := range p.Workspaces {
				rows = append(rows, projectsRow{
					kind: rowWorkspace, depth: depth + 1, id: w.ID, label: filepath.Base(w.Dir),
					branch: w.Branch, plain: p.Kind == "plain", target: w.TargetBranch, ws: []string{w.ID}, isGone: w.IsGone,
					isMain: w.IsMain, setup: setupState(w.Setup),
				})
			}
		}
	}
	return rows
}

func visibleWorkspaces(ws []api.WorkspaceNode, showGone bool) []api.WorkspaceNode {
	if showGone {
		return ws
	}
	var out []api.WorkspaceNode
	for _, w := range ws {
		if !w.IsGone {
			out = append(out, w)
		}
	}
	return out
}

func matchProject(p api.ProjectNode, q string) (api.ProjectNode, bool) {
	if strings.Contains(strings.ToLower(p.Name), q) {
		return p, true
	}
	var ws []api.WorkspaceNode
	for _, w := range p.Workspaces {
		if strings.Contains(strings.ToLower(filepath.Base(w.Dir)), q) || strings.Contains(strings.ToLower(w.Branch), q) {
			ws = append(ws, w)
		}
	}
	p.Workspaces = ws
	return p, len(ws) > 0
}

func workspaceIDs(p api.ProjectNode) []string {
	ids := make([]string, len(p.Workspaces))
	for i, w := range p.Workspaces {
		ids[i] = w.ID
	}
	return ids
}

func setupState(run *api.ScriptRun) string {
	if run == nil || run.State == "ok" {
		return ""
	}
	return run.State
}

func (m model) workspaceSetup(wsID string) *api.ScriptRun {
	w, _ := m.findWorkspace(wsID)
	return w.Setup
}

func (m model) setupRunningAt(dir string) bool {
	for _, p := range m.left.tree.data {
		for _, w := range p.Workspaces {
			if w.Dir == dir && w.Setup != nil && w.Setup.State == "running" {
				return true
			}
		}
	}
	return false
}

// setupBlock heads the pane of workspace ws.
func (m model) setupBlock(ws string, w int) string {
	run := m.workspaceSetup(ws)
	if run == nil || run.State == "ok" {
		return ""
	}
	cmd := commandLine(run.Command)
	var head string
	switch {
	case run.State == "failed" && run.ExitCode > 0:
		head = StyleErrorBold.Render(fmt.Sprintf("setup failed (exit %d) · %s", run.ExitCode, cmd))
	case run.State == "failed" && cmd == "":
		head = StyleErrorBold.Render("setup failed")
	case run.State == "failed":
		head = StyleErrorBold.Render("setup failed · " + cmd)
	case cmd == "":
		head = StyleSecondaryBold.Render("setup running")
	default:
		head = StyleSecondaryBold.Render("setup running · " + cmd)
	}
	lines := []string{truncateLine(head, w)}
	tail := strings.Split(wsscript.CleanOutput(run.OutputTail), "\n")
	for _, l := range tail[max(0, len(tail)-8):] {
		if l != "" {
			lines = append(lines, truncateLine(dimStyle.Render(l), w))
		}
	}
	lines = append(lines, truncateLine(dimStyle.Render(m.keyText(projectsKeys.RunSetup)+" runs setup again · "+m.keyText(projectsKeys.SetupLog)+" shows the full log"), w))
	return strings.Join(lines, "\n") + "\n\n"
}

// sidebarMinWidth is the terminal width below which the left sidebar auto-
// collapses (compact mode), mirroring Crush's breakpoint behavior.
const sidebarMinWidth = 80

func (m model) projectsLeftW() int { return clampWidth(m.rawLeftW(), 20, m.leftMaxW()) }

func (m model) rawLeftW() int { return m.left.rawWidth() }

func (m model) rawFilesW() int { return m.right.rawWidth() }

// minPaneWidth is the room the center pane keeps however wide the sidebars get.
const minPaneWidth = 30

// leftMaxW and filesMaxW bound each sidebar so that, with the other one at its
// own width, the pane keeps minPaneWidth columns.
func (m model) leftMaxW() int {
	w := m.frameWidth() - dividerWidth - minPaneWidth
	if m.filesVisible() {
		w -= m.rawFilesW() + screenMargin + dividerWidth
	}
	return w
}

func (m model) filesMaxW() int {
	w := m.frameWidth() - screenMargin - dividerWidth - minPaneWidth
	if m.sidebarVisible() {
		w -= m.rawLeftW() + dividerWidth
	}
	return w
}

func (m *model) toggleSidebar() {
	if m.width < sidebarMinWidth {
		m.flash = fmt.Sprintf("the tree needs %d columns", sidebarMinWidth)
		return
	}
	m.left.hidden = !m.left.hidden
}

func (m *model) toggleFiles() {
	if m.width < filesMinWidth {
		m.flash = fmt.Sprintf("the right sidebar needs %d columns", filesMinWidth)
		return
	}
	m.right.hidden = !m.right.hidden
}

func (m model) sidebarVisible() bool {
	return !m.left.hidden && m.width >= sidebarMinWidth
}

func projNodeLabel(p api.ProjectNode) string {
	if p.NodeLabel != "" {
		return p.NodeLabel
	}
	if p.NodeID != "" {
		return p.NodeID
	}
	return "this machine"
}

func (m model) wsSessions(ws string) []session.Session {
	if ws == "" {
		return nil
	}
	var out []session.Session
	for _, s := range m.sessions {
		if s.WorkspaceID == ws && m.shows(s) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// wsRow builds the row of workspace ws even when the tree does not show it.
func (m model) wsRow(ws string) (projectsRow, bool) {
	for _, p := range m.left.tree.data {
		for _, w := range p.Workspaces {
			if w.ID == ws {
				return projectsRow{
					kind: rowWorkspace, id: w.ID, label: filepath.Base(w.Dir), branch: w.Branch, plain: p.Kind == "plain",
					target: w.TargetBranch, ws: []string{w.ID}, isGone: w.IsGone, isMain: w.IsMain,
					setup: setupState(w.Setup),
				}, true
			}
		}
	}
	return projectsRow{}, false
}

type wsActivity struct{ live, working, waiting int }

func (m model) workspaceActivity() map[string]wsActivity {
	out := map[string]wsActivity{}
	for _, s := range m.sessions {
		if s.WorkspaceID == "" || s.Offline || s.Status == session.StatusDead {
			continue
		}
		a := out[s.WorkspaceID]
		a.live++
		switch s.Status {
		case session.StatusWorking:
			a.working++
		case session.StatusAwaitingInput:
			a.waiting++
		}
		out[s.WorkspaceID] = a
	}
	return out
}

func (m model) activityBadge(act map[string]wsActivity, ws []string) string {
	var a wsActivity
	for _, id := range ws {
		b := act[id]
		a.live, a.working, a.waiting = a.live+b.live, a.working+b.working, a.waiting+b.waiting
	}
	return m.badgeFor(a)
}

// homeBadge counts every live session, with or without a workspace.
func (m model) homeBadge() string {
	var a wsActivity
	for _, s := range m.sessions {
		if s.Offline || s.Status == session.StatusDead {
			continue
		}
		a.live++
		switch s.Status {
		case session.StatusWorking:
			a.working++
		case session.StatusAwaitingInput:
			a.waiting++
		}
	}
	return m.badgeFor(a)
}

func (m model) badgeFor(a wsActivity) string {
	n := strconv.Itoa(a.live)
	switch {
	case a.live == 0:
		return ""
	case a.waiting > 0:
		return lipgloss.NewStyle().Foreground(statusColor(session.StatusAwaitingInput)).Render(statusGlyph(session.StatusAwaitingInput) + " " + n)
	case a.working > 0:
		return lipgloss.NewStyle().Foreground(ColorOngoing).Render(SpinnerFrames[m.spin%len(SpinnerFrames)] + " " + n)
	}
	return StyleDim.Render(statusGlyph(session.StatusIdle) + " " + n)
}

// Any key but a scroll key closes the help screen.
func (m model) helpScreen() string {
	lines := strings.Split(m.projectsHelpView(), "\n")
	rows := m.helpRows()
	top := min(m.helpScroll, max(0, len(lines)-rows))
	help := indentBlock(strings.Join(lines[top:min(len(lines), top+rows)], "\n"), strings.Repeat(" ", screenMargin))
	keys := []binding{hint("any key", "close")}
	if len(lines) > rows {
		keys = append([]binding{hint("↑/↓", "scroll")}, keys...)
	}
	return pinFooter(m.frameTitle()+"\n\n"+composeH(m.width, rows, flexPanel(help)), m.footer(keys...), m.width, m.height)
}

func paneTitle(text string, focused bool) string {
	if focused {
		return StyleAccentBold.Render(text)
	}
	return StyleDim.Render(text)
}

// treeMarker fills the screen margin so row text lines up with the Projects
// title.
func treeMarker(sel, focused bool) string {
	switch {
	case !sel:
		return strings.Repeat(" ", screenMargin)
	case focused:
		return lipgloss.NewStyle().Foreground(ColorFocus).Render("▌") + " "
	}
	return StyleDim.Render("▌") + " "
}

// The bar dims without focus instead of hiding, so the selection stays visible
// after focus moves to another pane.
func cursorLine(text string, sel, focused bool) string {
	switch {
	case !sel:
		return "  " + text
	case focused:
		return lipgloss.NewStyle().Foreground(ColorFocus).Render("▌") + " " + cursorStyle.Render(text)
	}
	return StyleDim.Render("▌") + " " + text
}

func (m model) projRowLine(r projectsRow, sel, focused bool, act map[string]wsActivity, w int) string {
	indent := strings.Repeat("  ", r.depth)
	var text string
	switch r.kind {
	case rowHome:
		text = Icon.Home.Render() + " " + StyleSecondaryBold.Render("Home")
	case rowNode:
		text = indent + collapseMark(m.left.tree.isFolded(r.id), true) + Icon.Node.Render() + " " + StyleSecondaryBold.Render(r.label)
	case rowProject:
		text = indent + collapseMark(m.left.tree.isFolded(r.id), r.hasKids) + projectLabel(r.label, r.hidden, r.pinned)
		if r.gitErr {
			text += StyleErrorBold.Render(" (git error)")
		}
	case rowWorkspace:
		bullet := "• "
		if r.isMain {
			bullet = "★ "
		}
		text = indent + bullet + r.label
		switch r.setup {
		case "running":
			text += dimStyle.Render("  " + spinnerFrame(m) + " setting up…")
		case "failed":
			text += StyleErrorBold.Render("  setup failed")
		}
		switch {
		case r.isGone, r.plain:
		case r.branch != "":
			text += dimStyle.Render("  " + r.branch)
		default:
			text += dimStyle.Render("  (detached)")
		}
		if m.left.tree.removing[r.id] {
			text += dimStyle.Render("  removing…")
		}
	}
	if r.isGone {
		text += dimStyle.Render(" (gone)")
	}
	var badge string
	switch {
	case r.kind == rowHome:
		badge = m.homeBadge()
	case r.kind == rowWorkspace || m.left.tree.isFolded(r.id):
		badge = m.activityBadge(act, r.ws)
	}
	if sel && focused {
		text = cursorStyle.Render(text)
	}
	return withBadge(text, badge, w)
}

func withBadge(line, badge string, w int) string {
	if badge == "" {
		return truncateLine(line, w)
	}
	bw := lipgloss.Width(badge)
	left := truncateLine(line, max(1, w-bw-1))
	return left + strings.Repeat(" ", max(1, w-lipgloss.Width(left)-bw)) + badge
}

func projectLabel(name string, hidden, pinned bool) string {
	if hidden {
		name = StyleMuted.Render(name) + " " + Icon.Hidden.Render()
	}
	if pinned {
		name += " " + Icon.Pin.Render()
	}
	return name
}

func collapseMark(collapsed, hasKids bool) string {
	if !hasKids {
		return "  "
	}
	if collapsed {
		return "▸ "
	}
	return "▾ "
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func (m model) bodyWidth() int { return m.layout().w }

// bodyHeight includes the main pane's footer rows.
func (m model) bodyHeight() int { return m.layout().h }

// bodyWidthFor takes a leftW of -dividerWidth when no left panel shows.
func (m model) bodyWidthFor(leftW int) int {
	return max(1, m.frameWidth()-leftW-dividerWidth-m.rightCols())
}

func (m model) rightCols() int {
	if !m.filesVisible() {
		return 0
	}
	return m.projectsFilesW() + screenMargin + dividerWidth
}

const (
	filesMinWidth = 120
	filesDefaultW = 34
)

func (m model) filesVisible() bool {
	return !m.right.hidden && m.width >= filesMinWidth && m.topFull() != fullTerminal
}

func (m model) projectsFilesW() int { return clampWidth(m.rawFilesW(), 20, m.filesMaxW()) }

type workspaceView interface {
	workspace(c *ctx) string
}

// currentWorkspace is the workspace the right sidebar shows.
func (m model) currentWorkspace() string {
	if v, ok := m.baseComp().(workspaceView); ok {
		return v.workspace(&ctx{m: &m})
	}
	return ""
}

func commandLine(cmd string) string {
	first, rest, _ := strings.Cut(strings.TrimSpace(cmd), "\n")
	first = strings.ReplaceAll(first, "\t", " ")
	first = strings.TrimSpace(stripControls(xansi.Strip(first)))
	if strings.TrimSpace(rest) != "" {
		first += " …"
	}
	return first
}

func stripControls(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func viewLines(s string) []string {
	if s = strings.TrimSuffix(s, "\n"); s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func fileViewWidth(w int) int { return min(w, maxContentWidth) }

// fileViewHeight is the height an open file lays out in, its title line
// included, in a main pane h high: below the header it keeps and above the
// footer.
func fileViewHeight(h int) int { return max(1, h-footerRows-2) }

func wrapLine(s string, w int) []string {
	return strings.Split(xansi.Hardwrap(s, max(1, w), true), "\n")
}

func fileViewRows(lines []string, scroll, avail, w int, wrap bool) string {
	var out []string
	for i := min(scroll, cursorBottom(len(lines))); i < len(lines) && len(out) < avail; i++ {
		if wrap {
			out = append(out, wrapLine(lines[i], w)...)
		} else {
			out = append(out, xansi.Truncate(lines[i], max(1, w), "…"))
		}
	}
	return strings.Join(out[:min(len(out), avail)], "\n")
}

func brandMark() string { return Icon.Claude.Render() + " " + headerStyle.Render("argus") + "    " }
