package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// changesFocused is the wide projects screen on n1:w1 with the sidebar focused
// on the Changes tab and its list loaded.
func changesFocused(files ...api.ChangedFile) model {
	m := filesFocused()
	m.client = &recordingClient{}
	m, _ = upd(m, keyMsg("]"))
	m, _ = upd(m, changedFilesMsg{ws: "n1:w1", files: files})
	return m
}

func lastParams(m model) any {
	rc := m.client.(*recordingClient)
	return rc.params[len(rc.params)-1]
}

func TestSidebarBracketsSwitchTabs(t *testing.T) {
	m := filesFocused()
	m.client = &recordingClient{}
	m, cmd := upd(m, keyMsg("]"))
	if m.projects.sideTab != sideChanges || cmd == nil {
		t.Fatalf("] should switch to Changes and fetch its list: tab=%v", m.projects.sideTab)
	}
	runCmd(cmd)
	if p, _ := paramsFor(m, api.MethodWorkspaceChangedFiles).(api.WorkspaceRef); p.WorkspaceID != "n1:w1" {
		t.Errorf("changedFiles params = %+v, want n1:w1", p)
	}
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "Files  Changes") {
		t.Errorf("the sidebar header should be the tab strip:\n%s", out)
	}
	m, _ = upd(m, keyMsg("]"))
	if m.projects.sideTab != sideFiles {
		t.Errorf("two tabs: ] twice should wrap to Files, got %v", m.projects.sideTab)
	}
	m, _ = upd(m, keyMsg("["))
	m, _ = upd(m, keyMsg("tab"))
	if m.projects.sideTab != sideChanges || m.projects.focus == focusFiles {
		t.Errorf("the tab should stay when focus leaves the sidebar: tab=%v focus=%v", m.projects.sideTab, m.projects.focus)
	}
}

func TestChangesListRendersInSidebar(t *testing.T) {
	long := "internal/some/deeply/nested/package/dir/filetree_really_long_name.go"
	m := changesFocused(api.ChangedFile{Path: "a.go", Change: "added"}, api.ChangedFile{Path: long, Change: "modified"})
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"A a.go", "M …", "filetree_really_long_name.go", "uncommitted"} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing %q:\n%s", want, out)
		}
	}
	assertFits(t, m.View().Content, m.width)
}

func TestChangesFollowWorkspace(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "old.go"})
	m.projects.focus = focusTree
	m, cmd := upd(m, keyMsg("j")) // to n1:w2
	if m.projects.changes.ws != "n1:w2" || m.projects.changes.files != nil || cmd == nil {
		t.Fatalf("moving to n1:w2 should drop w1's changes and fetch w2's: ws=%q files=%v", m.projects.changes.ws, m.projects.changes.files)
	}
	m, _ = upd(m, changedFilesMsg{ws: "n1:w1", files: []api.ChangedFile{{Path: "stale.go"}}})
	if m.projects.changes.files != nil {
		t.Error("a list for the old workspace must be ignored")
	}
}

func TestChangesTabInSessionUsesSessionWorkspace(t *testing.T) {
	m := wideWorkspace()
	m.client = &recordingClient{}
	m.projects.sideTab = sideChanges
	m.projects.focus = focusPane
	mm, _ := m.enterSession("n1:s1") // session in n1:w1
	m, cmd := mm.syncSidebar()
	if cmd == nil {
		t.Fatal("the session screen should load the Changes list for the session's workspace")
	}
	runCmd(cmd)
	if p, _ := paramsFor(m, api.MethodWorkspaceChangedFiles).(api.WorkspaceRef); p.WorkspaceID != "n1:w1" {
		t.Errorf("changedFiles params = %+v, want n1:w1", p)
	}
	m, _ = upd(m, changedFilesMsg{ws: "n1:w1", files: []api.ChangedFile{{Path: "a.go", Change: "modified"}}})
	wide := m
	wide.width = 200
	if f := ansi.Strip(wide.sessionFooter()); !strings.Contains(f, "^f changes") {
		t.Errorf("session footer should offer ^f changes: %q", f)
	}
	m, _ = upd(m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	m, _ = upd(m, keyMsg("enter"))
	m, _ = upd(m, wsDiffMsg{ws: "n1:w1", path: "a.go", diff: "@@ -1 +1 @@\n-a\n+bb"})
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "+bb") || m.mode != modeSession {
		t.Errorf("the diff should replace the transcript:\n%s", out)
	}
}

func TestChangesEnterOpensDiffInPane(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "b.go", OrigPath: "a.go", Change: "renamed"})
	m, cmd := upd(m, keyMsg("enter"))
	f := m.projects.fileView
	if !f.open() || !f.diff || f.path != "b.go" || cmd == nil {
		t.Fatalf("enter should open the diff in the pane and fetch it: %+v", f)
	}
	if m.projects.focus != focusPane {
		t.Errorf("focus should move to the opened diff: focus=%v", m.projects.focus)
	}
	cmd()
	if p := lastParams(m).(api.WorkspaceFileParams); p.OrigPath != "a.go" || p.Path != "b.go" {
		t.Errorf("diff params = %+v, want the rename source a.go", p)
	}
	m, _ = upd(m, readFileMsg{ws: "n1:w1", path: "b.go", content: "file body"})
	if len(m.projects.fileView.lines) != 0 {
		t.Error("a file read must not fill an open diff")
	}
	m, _ = upd(m, wsDiffMsg{ws: "n1:w1", path: "b.go", diff: "@@ -1 +1 @@\n-a\n+bb"})
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "+bb") {
		t.Fatalf("the pane should show the diff:\n%s", out)
	}
	for i := 0; i < 10; i++ {
		m, _ = upd(m, keyMsg("j"))
	}
	if s := m.projects.fileView.scroll; s > 2 {
		t.Errorf("diff scroll ran past the end: %d", s)
	}
	m, _ = upd(m, keyMsg("esc"))
	if m.projects.fileView.open() || m.projects.focus != focusFiles || m.projects.sideTab != sideChanges {
		t.Errorf("esc should close the diff and return to the Changes tab: open=%v focus=%v tab=%v",
			m.projects.fileView.open(), m.projects.focus, m.projects.sideTab)
	}
}

func TestEmptyDiffSaysNoChanges(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "a.go", Change: "modified"})
	m, _ = upd(m, keyMsg("enter"))
	m, _ = upd(m, wsDiffMsg{ws: "n1:w1", path: "a.go"})
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "a.go: no changes") {
		t.Errorf("an empty diff should say there are no changes:\n%s", out)
	}
}

func TestChangesDiffModeToggle(t *testing.T) {
	m := wideWorkspace()
	m.projects.tree[0].Workspaces[0].TargetBranch = "main"
	m.projects.rebuild()
	m.projects.focus = focusFiles
	m.client = &recordingClient{}
	m, _ = upd(m, keyMsg("]"))
	m, _ = upd(m, changedFilesMsg{ws: "n1:w1", files: []api.ChangedFile{}})
	m, cmd := upd(m, keyMsg("t"))
	if m.projects.changes.against != api.AgainstTarget || cmd == nil {
		t.Fatalf("t should switch to target mode and reload: against=%q", m.projects.changes.against)
	}
	cmd()
	if p := lastParams(m).(api.WorkspaceRef); p.Against != api.AgainstTarget {
		t.Errorf("changedFiles params = %+v", p)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "vs main") {
		t.Error("the Changes tab should show the diff mode")
	}
	m, _ = upd(m, changedFilesMsg{ws: "n1:w1", against: "", files: []api.ChangedFile{{Path: "stale"}}})
	if n := len(m.projects.changes.files); n != 0 {
		t.Errorf("a result for the other mode must be dropped, got %d files", n)
	}
}

func TestChangesRefreshRefetches(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "a.go"})
	m, cmd := upd(m, keyMsg("r"))
	if m.projects.changes.files != nil || !m.projects.changes.loading || cmd == nil {
		t.Errorf("r on the Changes tab should reload the list: %+v", m.projects.changes)
	}
}

func TestSetTargetReloadsChanges(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "old-target.go"})
	m.projects.changes.against = api.AgainstTarget
	res, _ := m.Update(m.setTargetCmd("n1:w1", "dev")())
	c := res.(model).projects.changes
	if c.files != nil || c.against != api.AgainstTarget {
		t.Errorf("after set target: files=%v against=%q, want files cleared and mode kept", c.files, c.against)
	}
}

func TestSidebarFootersListTabKeys(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "a.go"})
	m.width = 200
	f := ansi.Strip(m.projectsFooter())
	for _, want := range []string{"[/] tabs", "enter diff", "t vs target", "^e changes"} {
		if !strings.Contains(f, want) {
			t.Errorf("Changes footer missing %q: %q", want, f)
		}
	}
	m, _ = upd(m, keyMsg("["))
	if f := ansi.Strip(m.projectsFooter()); !strings.Contains(f, "[/] tabs") || strings.Contains(f, "vs target") {
		t.Errorf("Files footer = %q", f)
	}
}

func TestWorkspacePaneHasNoTabs(t *testing.T) {
	m := wideWorkspace()
	m.projects.focus = focusPane
	out := ansi.Strip(m.View().Content)
	if strings.Contains(out, "Sessions  Changes") || !strings.Contains(out, "repo  main") {
		t.Errorf("the pane header should be the workspace, with no tab strip:\n%s", out)
	}
	if f := ansi.Strip(m.projectsFooter()); strings.Contains(f, "tabs") || strings.Contains(f, "vs target") {
		t.Errorf("pane footer should not list tab or Changes keys: %q", f)
	}
}
