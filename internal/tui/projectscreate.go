package tui

import (
	"path/filepath"
	"strconv"
	"strings"

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

type createPicker struct {
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
	seq             int // tags this picker's create call; see projectTreeComp.createSeq
}

func newCreatePicker(projectID, project, defaultTarget string, seq int) createPicker {
	name := textinput.New()
	name.Prompt = ""
	filter := textinput.New()
	filter.Prompt = ""
	return createPicker{
		projectID: projectID, project: project, defaultTarget: defaultTarget,
		name: name, filter: filter, branches: newBranchPicker(), seq: seq,
	}
}

func (c createPicker) targetLabel() string {
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

func (p createPicker) createCmd(c *ctx, params api.WorkspaceCreateParams) tea.Cmd {
	client, seq := c.m.client, p.seq
	return func() tea.Msg {
		var r api.WorkspaceCreateResult
		err := client.Call(api.MethodWorkspaceCreate, params, &r)
		return createDoneMsg{res: r, source: params.Source, seq: seq, err: err}
	}
}

// retryFailedTab clears the current tab's load error so ensureData fetches it
// again.
func (p *createPicker) retryFailedTab() {
	switch {
	case p.tab == ctBranches && p.branches.err != nil:
		p.branches.loaded, p.branches.err = false, nil
	case p.tab == ctPRs && p.prsErr != nil:
		p.prsLoaded, p.prsErr = false, nil
	case p.tab == ctIssues && p.issuesErr != nil:
		p.issuesLoaded, p.issuesErr = false, nil
	}
}

func (p createPicker) listLoading() bool {
	switch p.tab {
	case ctBranches:
		return !p.branches.loaded
	case ctPRs:
		return !p.prsLoaded
	case ctIssues:
		return !p.issuesLoaded
	}
	return false
}

func (p createPicker) ensureData(c *ctx) tea.Cmd {
	switch {
	case p.tab == ctBranches && !p.branches.loaded:
		return c.m.fetchBranchesCmd(p.projectID)
	case p.tab == ctPRs && !p.prsLoaded:
		return c.m.fetchPRsCmd(p.projectID)
	case p.tab == ctIssues && !p.issuesLoaded:
		return c.m.fetchIssuesCmd(p.projectID)
	}
	return nil
}

func (p createPicker) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	c.setFlash("")
	if p.creating {
		if msg.String() == "esc" {
			c.closePopup() // the call still finishes; its reply only reports
		}
		return p, nil
	}
	if p.picking {
		var cmd tea.Cmd
		p, cmd = p.targetPickKey(msg)
		return p, cmd
	}
	if c.m.matches(msg, createKeys.Target) {
		if p.tab == ctPRs {
			return p, nil
		}
		p.picking = true
		p.targetPick = newBranchPicker()
		if p.branches.loaded {
			p.targetPick.branches, p.targetPick.loaded, p.targetPick.err = p.branches.branches, true, p.branches.err
			return p, nil
		}
		return p, c.m.fetchBranchesCmd(p.projectID)
	}
	switch msg.String() {
	case "esc":
		c.closePopup()
		return p, nil
	case "tab", "shift+tab":
		d := 1
		if msg.String() == "shift+tab" {
			d = -1
		}
		return p.switchTab(c, createTab((int(p.tab)+d+len(createTabNames))%len(createTabNames)))
	case "enter":
		var cmd tea.Cmd
		p, cmd = p.submit(c)
		return p, cmd
	}
	p.err = "" // an edit answers the last error
	var cmd tea.Cmd
	switch p.tab {
	case ctNew:
		p.name, cmd = p.name.Update(msg)
	case ctBranches:
		_, cmd = p.branches.key(msg)
	default:
		switch msg.String() {
		case "up":
			p.cursor = cursorUp(p.cursor)
		case "down":
			p.cursor = cursorDown(p.cursor, p.listLen())
		default:
			p.filter, cmd = p.filter.Update(msg)
			p.cursor = min(p.cursor, cursorBottom(p.listLen()))
		}
	}
	return p, cmd
}

func (p createPicker) targetPickKey(msg tea.KeyPressMsg) (createPicker, tea.Cmd) {
	if msg.String() == "esc" {
		p.picking = false
		return p, nil
	}
	picked, cmd := p.targetPick.key(msg)
	if picked != nil {
		p.target, p.picking = picked.Name, false
	}
	return p, cmd
}

func (p createPicker) update(c *ctx, msg tea.Msg) (popup, tea.Cmd) {
	switch msg := msg.(type) {
	case branchesMsg:
		if msg.projectID == p.projectID {
			p.branches.branches, p.branches.loaded, p.branches.err = msg.branches, true, msg.err
			if p.picking {
				p.targetPick.branches, p.targetPick.loaded, p.targetPick.err = msg.branches, true, msg.err
			}
		}
	case prsMsg:
		if msg.projectID == p.projectID {
			p.prs, p.prsLoaded, p.prsErr, p.prsTruncated = msg.prs, true, msg.err, msg.truncated
		}
	case issuesMsg:
		if msg.projectID == p.projectID {
			p.issues, p.issuesLoaded, p.issuesErr, p.issuesTruncated = msg.issues, true, msg.err, msg.truncated
		}
	case tea.PasteMsg:
		return p.paste(msg)
	case createDoneMsg:
		switch {
		case msg.seq != p.seq:
		case msg.err != nil:
			p.creating, p.err = false, msg.err.Error()
		default:
			c.closePopup()
		}
	}
	return p, nil
}

func (p createPicker) paste(msg tea.PasteMsg) (popup, tea.Cmd) {
	if p.creating {
		return p, nil
	}
	if p.picking {
		return p, p.targetPick.paste(msg)
	}
	p.err = ""
	var cmd tea.Cmd
	switch p.tab {
	case ctNew:
		p.name, cmd = p.name.Update(msg)
	case ctBranches:
		cmd = p.branches.paste(msg)
	default:
		p.filter, cmd = p.filter.Update(msg)
		p.cursor = min(p.cursor, cursorBottom(p.listLen()))
	}
	return p, cmd
}

func (p createPicker) filteredPRs() []api.PRInfo {
	q := strings.ToLower(strings.TrimSpace(p.filter.Value()))
	var out []api.PRInfo
	for _, pr := range p.prs {
		hay := strings.ToLower(strconv.Itoa(pr.Number) + " " + pr.Title + " " + pr.Author + " " + pr.HeadBranch + " " + pr.BaseBranch)
		if q == "" || strings.Contains(hay, q) {
			out = append(out, pr)
		}
	}
	return out
}

func (p createPicker) filteredIssues() []api.IssueInfo {
	q := strings.ToLower(strings.TrimSpace(p.filter.Value()))
	var out []api.IssueInfo
	for _, is := range p.issues {
		hay := strings.ToLower(strconv.Itoa(is.Number) + " " + is.Title + " " + is.Author)
		if q == "" || strings.Contains(hay, q) {
			out = append(out, is)
		}
	}
	return out
}

func (p createPicker) listLen() int {
	if p.tab == ctPRs {
		return len(p.filteredPRs())
	}
	return len(p.filteredIssues())
}

func (p createPicker) submit(c *ctx) (createPicker, tea.Cmd) {
	params := api.WorkspaceCreateParams{ProjectID: p.projectID, TargetBranch: p.target}
	switch p.tab {
	case ctNew:
		params.Source, params.Branch = api.SourceNew, strings.TrimSpace(p.name.Value())
		if params.Branch == "" {
			return p, nil
		}
	case ctBranches:
		ms := p.branches.matches()
		if p.branches.cursor >= len(ms) {
			return p, nil
		}
		br := ms[p.branches.cursor]
		if br.CheckedOut {
			p.err = br.Name + " is checked out in another workspace"
			return p, nil
		}
		params.Source, params.Branch = api.SourceBranch, br.Name
	case ctPRs:
		prs := p.filteredPRs()
		if p.cursor >= len(prs) {
			return p, nil
		}
		params.Source, params.Number, params.TargetBranch = api.SourcePR, prs[p.cursor].Number, ""
	case ctIssues:
		is := p.filteredIssues()
		if p.cursor >= len(is) {
			return p, nil
		}
		params.Source, params.Number = api.SourceIssue, is[p.cursor].Number
	}
	p.creating, p.err = true, ""
	return p, p.createCmd(c, params)
}

// --- view ---------------------------------------------------------------------

func (p createPicker) switchTab(c *ctx, t createTab) (createPicker, tea.Cmd) {
	p.tab = t
	p.cursor, p.err = 0, ""
	p.retryFailedTab()
	p.filter.SetValue("")
	p.branches.filter.SetValue("")
	p.branches.cursor = 0
	var focus tea.Cmd
	if p.tab == ctNew {
		focus = p.name.Focus()
	} else {
		p.name.Blur()
		focus = p.filter.Focus()
	}
	return p, tea.Batch(focus, p.ensureData(c))
}

func (p createPicker) titleInfo(c *ctx) string {
	info := dimStyle.Render("target: " + p.targetLabel())
	if p.tab != ctPRs {
		info += dimStyle.Render("  (" + c.m.keyText(createKeys.Target) + ")")
	}
	return info
}

func (p createPicker) body(c *ctx, w, h int) string {
	m := c.m
	var top string
	if pr, ok := m.findProject(p.projectID); ok && pr.Scripts != nil && pr.Scripts.Setup != "" {
		top = truncateLine(dimStyle.Render("setup: "+commandLine(pr.Scripts.Setup)), w) + "\n\n"
	}
	c.hitTabs(0, strings.Count(top, "\n"), 3, createTabNames...)
	var tabs []string
	for i, n := range createTabNames {
		if createTab(i) == p.tab {
			tabs = append(tabs, StyleAccentBold.Render(n))
		} else {
			tabs = append(tabs, StyleDim.Render(n))
		}
	}
	top += strings.Join(tabs, StyleDim.Render("   ")) + "\n\n"
	bc := c.below(strings.Count(top, "\n"))
	bodyH := max(1, h-strings.Count(top, "\n"))
	switch {
	case p.creating:
		return top + spinnerFrame(*m) + " creating…"
	case p.picking:
		return top + dimStyle.Render("select target branch") + "\n" + p.targetPick.view(bc.below(1), w, bodyH-1, false)
	}
	var body string
	switch p.tab {
	case ctNew:
		body = dimStyle.Render("branch: ") + p.name.View()
	case ctBranches:
		body = p.branches.view(bc, w, bodyH, true)
	case ctPRs:
		prs := p.filteredPRs()
		body = p.listView(bc, w, bodyH, p.prsLoaded, p.prsErr, p.prsTruncated, len(p.prs), len(prs), func(i int) string {
			pr := prs[i]
			return "#" + strconv.Itoa(pr.Number) + "  " + pr.Title + dimStyle.Render("  "+pr.Author+"  "+pr.HeadBranch+" → "+pr.BaseBranch)
		})
	case ctIssues:
		is := p.filteredIssues()
		body = p.listView(bc, w, bodyH, p.issuesLoaded, p.issuesErr, p.issuesTruncated, len(p.issues), len(is), func(i int) string {
			return "#" + strconv.Itoa(is[i].Number) + "  " + is[i].Title + dimStyle.Render("  "+is[i].Author)
		})
	}
	if p.err != "" {
		body += "\n\n" + StyleErrorBold.Render(truncateLine(p.err, w))
	}
	return top + body
}

func (p createPicker) listView(c *ctx, w, h int, loaded bool, err error, truncated bool, total, n int, line func(int) string) string {
	head := dimStyle.Render("filter: ") + p.filter.View()
	if truncated {
		head += dimStyle.Render("  (first " + strconv.Itoa(total) + "; filter searches only these)")
	}
	switch {
	case err != nil:
		return head + "\n\n" + dimStyle.Render("error: "+firstLine(err.Error()))
	case !loaded:
		return head + "\n\n" + dimStyle.Render(spinnerFrame(*c.m)+" loading…")
	case total == 0:
		return head + "\n\n" + dimStyle.Render("none open")
	case n == 0:
		return head + "\n\n" + dimStyle.Render("no matches")
	}
	var l itemLines
	for i := range n {
		l.add(i, truncateLine(cursorLine(line(i), i == p.cursor, true), w))
	}
	return head + "\n\n" + strings.Join(l.window(c.below(2), p.cursor, max(1, h-2)), "\n")
}

func spinnerFrame(m model) string {
	return SpinnerFrames[m.spin%len(SpinnerFrames)]
}

func (p createPicker) footer(*ctx) []binding {
	switch {
	case p.creating:
		return []binding{hint("esc", "hide")}
	case p.picking:
		return []binding{hint("↑/↓", "move"), hint("enter", "select"), hint("esc", "back")}
	}
	b := []binding{hint("enter", "create"), hint("tab", "next tab")}
	if p.tab != ctPRs {
		b = append(b, createKeys.Target)
	}
	return append(b, hint("esc", "cancel"))
}

func (p createPicker) click(c *ctx, t hitTarget) (popup, tea.Cmd) {
	switch {
	case p.creating:
	case t.kind == hitTab:
		if !p.picking && createTab(t.index) != p.tab {
			return p.switchTab(c, createTab(t.index))
		}
	case t.index == *p.listCursor():
		return p.handleKey(c, enterKey)
	default:
		*p.listCursor() = t.index
	}
	return p, nil
}

func (p createPicker) wheel(_ *ctx, d int) (popup, tea.Cmd) {
	var n int
	switch {
	case p.creating:
		return p, nil
	case p.picking:
		n = len(p.targetPick.matches())
	case p.tab == ctBranches:
		n = len(p.branches.matches())
	case p.tab == ctNew:
		return p, nil
	default:
		n = p.listLen()
	}
	cur := p.listCursor()
	*cur = cursorBy(*cur, d, n)
	return p, nil
}

func (p *createPicker) listCursor() *int {
	switch {
	case p.picking:
		return &p.targetPick.cursor
	case p.tab == ctBranches:
		return &p.branches.cursor
	}
	return &p.cursor
}
