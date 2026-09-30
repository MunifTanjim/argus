package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestProjectRowShowsTheSummaryComponent(t *testing.T) {
	m := selectRow(wideWorkspace(), "n1:p1")
	s, ok := m.baseComp().(summaryComp)
	if !ok || s.kind != rowProject || s.id != "n1:p1" {
		t.Fatalf("base = %#v, want the summary of n1:p1", m.baseComp())
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "/repo/.git") {
		t.Error("the summary should show the project's directory")
	}
	if m.currentWorkspace() != "" {
		t.Errorf("currentWorkspace = %q, want none for a summary", m.currentWorkspace())
	}
}

func TestWorkspacePaneReadsItsOwnWorkspace(t *testing.T) {
	m := wideWorkspace()
	m.main = m.main.replaceAt(0, workspaceComp{ws: "n1:w2"})
	if got := m.currentWorkspace(); got != "n1:w2" {
		t.Errorf("currentWorkspace = %q, want n1:w2 (the pane's own workspace)", got)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "repo-feat  feature") {
		t.Error("the pane should draw n1:w2's header, not the cursor row's")
	}
}

func TestSummaryKeysActOnTheTree(t *testing.T) {
	project := func() model { return withFocus(selectRow(wideWorkspace(), "n1:p1"), mainPane) }
	node := func() model {
		m := wideWorkspace()
		m.left.tree.data = append(m.left.tree.data, api.ProjectNode{ID: "n2:p9", Name: "other", NodeID: "n2"})
		m.left.tree.rebuild()
		return withFocus(selectRow(m, "n1"), mainPane)
	}
	for section, base := range map[string]func() model{"project": project, "node": node} {
		if got := base().screen(); got != section {
			t.Fatalf("%s summary: screen %q", section, got)
		}
		cases := []struct {
			name string
			keys []tea.KeyPressMsg
			ok   func(m model) bool
		}{
			{"/", []tea.KeyPressMsg{keyMsg("/")}, func(m model) bool { return m.focused == leftSidebar && m.left.tree.inputMode == pmFilter }},
			{"z.", []tea.KeyPressMsg{keyMsg("z"), keyMsg(".")}, func(m model) bool { return m.left.tree.showHidden }},
			{"zg", []tea.KeyPressMsg{keyMsg("z"), keyMsg("g")}, func(m model) bool { return m.left.tree.showGone }},
			{"<C-w>>", cw('>'), func(m model) bool { return m.projectsLeftW() == base().projectsLeftW()+4 }},
			{"<C-w><lt>", cw('<'), func(m model) bool { return m.projectsLeftW() == base().projectsLeftW()-4 }},
			{"<Esc>", []tea.KeyPressMsg{keyMsg("esc")}, func(m model) bool { return m.focused == leftSidebar }},
			{"<C-w>h", cw('h'), func(m model) bool { return m.focused == leftSidebar }},
			{"<C-w>l", cw('l'), func(model) bool { return true }}, // a summary has no workspace for the right sidebar
			{"<C-w>w", cw('w'), func(m model) bool { return m.focused != mainPane }},
			{"<C-w>W", cw('W'), func(m model) bool { return m.focused != mainPane }},
			{"<C-w>j", cw('j'), func(model) bool { return true }},
			{"<C-w>k", cw('k'), func(model) bool { return true }},
			{"g?", []tea.KeyPressMsg{keyMsg("g"), keyMsg("?")}, func(m model) bool { return m.showHelp }},
			{"<Space>o", []tea.KeyPressMsg{keyMsg(" "), keyMsg("o")}, func(m model) bool { return m.left.hidden }},
			{"<Space>e", []tea.KeyPressMsg{keyMsg(" "), keyMsg("e")}, func(m model) bool { return m.right.hidden }},
		}
		for _, c := range cases {
			if m := pressKeys(base(), c.keys...); !c.ok(m) {
				t.Errorf("%s summary: %s did not act: focus=%v flash=%q", section, c.name, m.focused, m.flash)
			}
		}
		m, _ := upd(base(), keyMsg("g"))
		if _, cmd := upd(m, keyMsg("r")); cmd == nil {
			t.Errorf("%s summary: gr should reload the tree", section)
		}
		if _, cmd := upd(base(), keyMsg("Q")); cmd == nil {
			t.Errorf("%s summary: Q should quit", section)
		}
	}
}

func TestSummaryHeaderFollowsFocus(t *testing.T) {
	project := selectRow(wideWorkspace(), "n1:p1")
	node := wideWorkspace()
	node.left.tree.data = append(node.left.tree.data, api.ProjectNode{ID: "n2:p9", Name: "other", NodeID: "n2"})
	node.left.tree.rebuild()
	node = selectRow(node, "n1")
	for name, c := range map[string]struct {
		m     model
		title string
	}{"project": {project, "argus"}, "node": {node, "home"}} {
		focused := withFocus(c.m, mainPane)
		body := rowSummary(&ctx{m: &focused}, projectsRowOf(focused), 80)
		if !strings.Contains(body, StyleAccentBold.Render(c.title)) {
			t.Errorf("%s summary: the header should use the focus color when the pane has focus:\n%q", name, body)
		}
		blurred := withFocus(c.m, leftSidebar)
		if body := rowSummary(&ctx{m: &blurred}, projectsRowOf(blurred), 80); strings.Contains(body, StyleAccentBold.Render(c.title)) {
			t.Errorf("%s summary: the header should not use the focus color without focus", name)
		}
	}
}

func projectsRowOf(m model) projectsRow {
	r, _ := m.baseComp().(summaryComp).row(&ctx{m: &m})
	return r
}

func TestSummaryHasNoWorkspaceKeys(t *testing.T) {
	node := wideWorkspace()
	node.left.tree.data = append(node.left.tree.data, api.ProjectNode{ID: "n2:p9", Name: "other", NodeID: "n2"})
	node.left.tree.rebuild()
	node = withFocus(selectRow(node, "n1"), mainPane)
	project := withFocus(selectRow(wideWorkspace(), "n1:p1"), mainPane)
	for name, m := range map[string]model{"node": node, "project": project} {
		if f := ansi.Strip(m.currentFooter()); strings.Contains(f, "spawn") {
			t.Errorf("%s summary footer should not offer spawn: %q", name, f)
		}
		for _, k := range []string{"s", "L"} {
			if m, _ := upd(m, keyMsg(k)); spawnOpen(m) || m.hasOpenFile() || m.flash != "" {
				t.Errorf("%s summary: %s should do nothing: flash=%q", name, k, m.flash)
			}
		}
	}
}
