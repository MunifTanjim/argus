package tui

import (
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

type createTab int

const (
	ctNew createTab = iota
	ctBranches
	ctPRs
	ctIssues
)

var createTabNames = []string{"New", "Branches", "PRs", "Issues"}

// createState is the "new workspace" picker on the projects screen.
type createState struct {
	active          bool
	projectID       string
	project         string
	defaultTarget   string
	tab             createTab
	name            textinput.Model // New tab
	filter          textinput.Model // PRs and Issues tabs
	cursor          int             // PRs and Issues tabs
	target          string          // "" = default branch
	branches        branchPicker
	prs             []api.PRInfo
	prsLoaded       bool
	prsErr          error
	prsTruncated    bool
	issues          []api.IssueInfo
	issuesLoaded    bool
	issuesErr       error
	issuesTruncated bool
	picking         bool // target picker open
	targetPick      branchPicker
	creating        bool
	err             string
	seq             int // tags this picker's create call; see projectsState.createSeq
}

func (m model) startCreate(projectID string) (tea.Model, tea.Cmd) {
	p, _ := m.findProject(projectID)
	m.projects.createSeq++
	name := textinput.New()
	name.Prompt = ""
	filter := textinput.New()
	filter.Prompt = ""
	m.projects.create = createState{
		active: true, projectID: projectID, project: p.Name, defaultTarget: p.DefaultBranch,
		name: name, filter: filter, branches: newBranchPicker(), seq: m.projects.createSeq,
	}
	return m, m.projects.create.name.Focus()
}

func (c createState) targetLabel() string {
	switch {
	case c.tab == ctPRs:
		return "from PR base"
	case c.target != "":
		return c.target
	case c.defaultTarget != "":
		return c.defaultTarget
	}
	return "default branch"
}

func (m model) fetchBranchesCmd(projectID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.BranchesResult
		err := client.Call(api.MethodProjectBranches, api.ProjectRef{ProjectID: projectID}, &r)
		return branchesMsg{projectID: projectID, branches: r.Branches, err: err}
	}
}

func (m model) fetchPRsCmd(projectID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.PRsResult
		err := client.Call(api.MethodProjectPRs, api.ProjectRef{ProjectID: projectID}, &r)
		return prsMsg{projectID: projectID, prs: r.PRs, truncated: r.Truncated, err: err}
	}
}

func (m model) fetchIssuesCmd(projectID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.IssuesResult
		err := client.Call(api.MethodProjectIssues, api.ProjectRef{ProjectID: projectID}, &r)
		return issuesMsg{projectID: projectID, issues: r.Issues, truncated: r.Truncated, err: err}
	}
}

func createdFlash(res api.WorkspaceCreateResult) string {
	s := "created workspace " + filepath.Base(res.Dir)
	if res.Setup != "" {
		s += " · setting up"
	}
	if res.Warning != "" {
		s += " · " + res.Warning
	}
	return s
}

func truncateEachLine(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = truncateLine(lines[i], w)
	}
	return strings.Join(lines, "\n")
}

func (m model) createCmd(p api.WorkspaceCreateParams) tea.Cmd {
	client, seq := m.client, m.projects.create.seq
	return func() tea.Msg {
		var r api.WorkspaceCreateResult
		err := client.Call(api.MethodWorkspaceCreate, p, &r)
		return createDoneMsg{res: r, source: p.Source, seq: seq, err: err}
	}
}

// retryFailedTab clears the current tab's load error so ensureCreateData
// fetches it again.
func (c *createState) retryFailedTab() {
	switch {
	case c.tab == ctBranches && c.branches.err != nil:
		c.branches.loaded, c.branches.err = false, nil
	case c.tab == ctPRs && c.prsErr != nil:
		c.prsLoaded, c.prsErr = false, nil
	case c.tab == ctIssues && c.issuesErr != nil:
		c.issuesLoaded, c.issuesErr = false, nil
	}
}

func (c createState) listLoading() bool {
	switch c.tab {
	case ctBranches:
		return !c.branches.loaded
	case ctPRs:
		return !c.prsLoaded
	case ctIssues:
		return !c.issuesLoaded
	}
	return false
}

// ensureCreateData loads the current tab's list the first time it is shown.
func (m model) ensureCreateData() tea.Cmd {
	c := m.projects.create
	switch {
	case c.tab == ctBranches && !c.branches.loaded:
		return m.fetchBranchesCmd(c.projectID)
	case c.tab == ctPRs && !c.prsLoaded:
		return m.fetchPRsCmd(c.projectID)
	case c.tab == ctIssues && !c.issuesLoaded:
		return m.fetchIssuesCmd(c.projectID)
	}
	return nil
}

func (m model) handleCreateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.flash = ""
	c := &m.projects.create
	if c.creating {
		if msg.String() == "esc" {
			c.active = false // the call still finishes; createDoneMsg selects the result
		}
		return m, nil
	}
	if c.picking {
		return m.handleTargetPickKey(msg)
	}
	switch msg.String() {
	case "esc":
		m.projects.create = createState{}
		return m, nil
	case "tab", "shift+tab":
		d := 1
		if msg.String() == "shift+tab" {
			d = -1
		}
		c.tab = createTab((int(c.tab) + d + len(createTabNames)) % len(createTabNames))
		c.cursor, c.err = 0, ""
		c.retryFailedTab()
		c.filter.SetValue("")
		c.branches.filter.SetValue("")
		c.branches.cursor = 0
		var focus tea.Cmd
		if c.tab == ctNew {
			focus = c.name.Focus()
		} else {
			c.name.Blur()
			focus = c.filter.Focus()
		}
		return m, tea.Batch(focus, m.ensureCreateData(), m.maybeSpin())
	case "ctrl+t":
		if c.tab == ctPRs {
			return m, nil
		}
		c.picking = true
		c.targetPick = newBranchPicker()
		if c.branches.loaded {
			c.targetPick.branches, c.targetPick.loaded, c.targetPick.err = c.branches.branches, true, c.branches.err
			return m, nil
		}
		return m, m.fetchBranchesCmd(c.projectID)
	case "enter":
		return m.submitCreate()
	}
	c.err = "" // an edit answers the last error
	var cmd tea.Cmd
	switch c.tab {
	case ctNew:
		c.name, cmd = c.name.Update(msg)
	case ctBranches:
		_, cmd = c.branches.key(msg)
	default:
		switch msg.String() {
		case "up":
			c.cursor = cursorUp(c.cursor)
		case "down":
			c.cursor = cursorDown(c.cursor, m.createListLen())
		default:
			c.filter, cmd = c.filter.Update(msg)
			c.cursor = min(c.cursor, cursorBottom(m.createListLen()))
		}
	}
	return m, cmd
}

func (m model) handleTargetPickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.projects.create
	if msg.String() == "esc" {
		c.picking = false
		return m, nil
	}
	picked, cmd := c.targetPick.key(msg)
	if picked != nil {
		c.target, c.picking = picked.Name, false
	}
	return m, cmd
}

func (m model) filteredPRs() []api.PRInfo {
	q := strings.ToLower(strings.TrimSpace(m.projects.create.filter.Value()))
	var out []api.PRInfo
	for _, p := range m.projects.create.prs {
		hay := strings.ToLower(strconv.Itoa(p.Number) + " " + p.Title + " " + p.Author + " " + p.HeadBranch + " " + p.BaseBranch)
		if q == "" || strings.Contains(hay, q) {
			out = append(out, p)
		}
	}
	return out
}

func (m model) filteredIssues() []api.IssueInfo {
	q := strings.ToLower(strings.TrimSpace(m.projects.create.filter.Value()))
	var out []api.IssueInfo
	for _, is := range m.projects.create.issues {
		hay := strings.ToLower(strconv.Itoa(is.Number) + " " + is.Title + " " + is.Author)
		if q == "" || strings.Contains(hay, q) {
			out = append(out, is)
		}
	}
	return out
}

func (m model) createListLen() int {
	if m.projects.create.tab == ctPRs {
		return len(m.filteredPRs())
	}
	return len(m.filteredIssues())
}

func (m model) submitCreate() (tea.Model, tea.Cmd) {
	c := &m.projects.create
	p := api.WorkspaceCreateParams{ProjectID: c.projectID, TargetBranch: c.target}
	switch c.tab {
	case ctNew:
		p.Source, p.Branch = api.SourceNew, strings.TrimSpace(c.name.Value())
		if p.Branch == "" {
			return m, nil
		}
	case ctBranches:
		ms := c.branches.matches()
		if c.branches.cursor >= len(ms) {
			return m, nil
		}
		br := ms[c.branches.cursor]
		if br.CheckedOut {
			c.err = br.Name + " is checked out in another workspace"
			return m, nil
		}
		p.Source, p.Branch = api.SourceBranch, br.Name
	case ctPRs:
		prs := m.filteredPRs()
		if c.cursor >= len(prs) {
			return m, nil
		}
		p.Source, p.Number, p.TargetBranch = api.SourcePR, prs[c.cursor].Number, ""
	case ctIssues:
		is := m.filteredIssues()
		if c.cursor >= len(is) {
			return m, nil
		}
		p.Source, p.Number = api.SourceIssue, is[c.cursor].Number
	}
	c.creating, c.err = true, ""
	return m, tea.Batch(m.createCmd(p), m.maybeSpin())
}

// --- view ---------------------------------------------------------------------

func (m model) createView(w, h int) string {
	c := m.projects.create
	head := StylePrimaryBold.Render("New workspace in "+c.project) + dimStyle.Render("   target: "+c.targetLabel())
	if c.tab != ctPRs {
		head += dimStyle.Render("  (^t)")
	}
	if p, ok := m.findProject(c.projectID); ok && p.Scripts != nil && p.Scripts.Setup != "" {
		head += "\n" + dimStyle.Render("setup: "+commandLine(p.Scripts.Setup))
	}
	var tabs []string
	for i, n := range createTabNames {
		if createTab(i) == c.tab {
			tabs = append(tabs, StyleAccentBold.Render(n))
		} else {
			tabs = append(tabs, StyleDim.Render(n))
		}
	}
	top := truncateEachLine(head, w) + "\n\n" + strings.Join(tabs, StyleDim.Render("   ")) + "\n\n"
	bodyH := max(1, h-4-strings.Count(head, "\n"))
	switch {
	case c.creating:
		return top + spinnerFrame(m) + " creating…"
	case c.picking:
		return top + dimStyle.Render("select target branch") + "\n" + c.targetPick.view(w, bodyH-1, false)
	}
	var body string
	switch c.tab {
	case ctNew:
		body = dimStyle.Render("branch: ") + c.name.View()
	case ctBranches:
		body = c.branches.view(w, bodyH, true)
	case ctPRs:
		prs := m.filteredPRs()
		body = m.createListView(w, bodyH, c.prsLoaded, c.prsErr, c.prsTruncated, len(c.prs), len(prs), func(i int) string {
			p := prs[i]
			return "#" + strconv.Itoa(p.Number) + "  " + p.Title + dimStyle.Render("  "+p.Author+"  "+p.HeadBranch+" → "+p.BaseBranch)
		})
	case ctIssues:
		is := m.filteredIssues()
		body = m.createListView(w, bodyH, c.issuesLoaded, c.issuesErr, c.issuesTruncated, len(c.issues), len(is), func(i int) string {
			return "#" + strconv.Itoa(is[i].Number) + "  " + is[i].Title + dimStyle.Render("  "+is[i].Author)
		})
	}
	if c.err != "" {
		body += "\n\n" + StyleErrorBold.Render(truncateLine(c.err, w))
	}
	return top + body
}

func (m model) createListView(w, h int, loaded bool, err error, truncated bool, total, n int, line func(int) string) string {
	c := m.projects.create
	head := dimStyle.Render("filter: ") + c.filter.View()
	if truncated {
		head += dimStyle.Render("  (first " + strconv.Itoa(total) + "; filter searches only these)")
	}
	switch {
	case err != nil:
		return head + "\n\n" + dimStyle.Render("error: "+firstLine(err.Error()))
	case !loaded:
		return head + "\n\n" + dimStyle.Render(spinnerFrame(m)+" loading…")
	case total == 0:
		return head + "\n\n" + dimStyle.Render("none open")
	case n == 0:
		return head + "\n\n" + dimStyle.Render("no matches")
	}
	lines := make([]string, n)
	for i := range n {
		lines[i] = truncateLine(cursorLine(line(i), i == c.cursor, true), w)
	}
	return head + "\n\n" + strings.Join(windowSpan(lines, c.cursor, c.cursor+1, max(1, h-2)), "\n")
}

func spinnerFrame(m model) string {
	return SpinnerFrames[m.spin%len(SpinnerFrames)]
}

func (m model) createFooter() string {
	c := m.projects.create
	k := projectsKeys
	switch {
	case c.creating:
		return m.footer(helpAs(k.Back, "esc", "hide"))
	case c.picking:
		return m.footer(helpAs(k.Up, "↑/↓", "move"), helpAs(k.Enter, "enter", "select"), helpAs(k.Back, "esc", "back"))
	}
	b := []key.Binding{helpAs(k.Enter, "enter", "create"), helpAs(k.Focus, "tab", "next tab")}
	if c.tab != ctPRs {
		b = append(b, createKeys.Target)
	}
	return m.footer(append(b, helpAs(k.Back, "esc", "cancel"))...)
}
