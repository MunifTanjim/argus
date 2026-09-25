package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

// paramsFor is the params of the last recorded call to method, or nil.
func paramsFor(m model, method string) any {
	rc := m.client.(*recordingClient)
	for i := len(rc.calls) - 1; i >= 0; i-- {
		if rc.calls[i] == method {
			return rc.params[i]
		}
	}
	return nil
}

func TestChangesTabFetchesFilesAndCommits(t *testing.T) {
	m := filesFocused()
	m.client = &recordingClient{}
	m, cmd := upd(m, keyMsg("]"))
	if !m.projects.changes.loading || !m.projects.changes.commitsLoading {
		t.Fatalf("both lists should start loading: %+v", m.projects.changes)
	}
	runCmd(cmd)
	if p, _ := paramsFor(m, api.MethodWorkspaceChangedFiles).(api.WorkspaceRef); p.WorkspaceID != "n1:w1" {
		t.Errorf("changedFiles params = %+v", p)
	}
	if p, _ := paramsFor(m, api.MethodWorkspaceCommits).(api.WorkspaceRef); p.WorkspaceID != "n1:w1" {
		t.Errorf("commits params = %+v", p)
	}
	m, _ = upd(m, commitsMsg{ws: "n1:w2", commits: []api.Commit{{SHA: "stale"}}})
	if m.projects.changes.commits != nil {
		t.Error("commits for another workspace must be dropped")
	}
	m, _ = upd(m, commitsMsg{ws: "n1:w1"})
	if c := m.projects.changes; c.commits == nil || len(c.commits) != 0 || c.commitsLoading {
		t.Errorf("an empty answer should mark the list loaded: %+v", c)
	}
}

func TestDiffModeReloadsOnlyFiles(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "a.go"})
	m, _ = upd(m, commitsMsg{ws: "n1:w1", commits: []api.Commit{{SHA: "abc1234", Short: "abc1234", Subject: "s"}}})
	rc := &recordingClient{}
	m.client = rc
	m, cmd := upd(m, keyMsg("t"))
	runCmd(cmd)
	if paramsFor(m, api.MethodWorkspaceChangedFiles) == nil || paramsFor(m, api.MethodWorkspaceCommits) != nil {
		t.Errorf("t should reload only the changed files: calls = %v", rc.calls)
	}
	if len(m.projects.changes.commits) != 1 {
		t.Error("t must keep the commits")
	}
}

func TestCommitFilesAnswerMatchesOpenCommit(t *testing.T) {
	m := changesFocused()
	cm := api.Commit{SHA: "abc1234", Short: "abc1234", Subject: "s"}
	mm, cmd := m.openCommit(cm)
	m = mm.(model)
	runCmd(cmd)
	if p, _ := paramsFor(m, api.MethodWorkspaceCommitFiles).(api.WorkspaceCommitParams); p.WorkspaceID != "n1:w1" || p.SHA != "abc1234" {
		t.Fatalf("commitFiles params = %+v", p)
	}
	m, _ = upd(m, commitFilesMsg{ws: "n1:w1", sha: "other", files: []api.ChangedFile{{Path: "x"}}})
	if m.projects.changes.commitFiles != nil {
		t.Error("files for another commit must be dropped")
	}
	m, _ = upd(m, commitFilesMsg{ws: "n1:w1", sha: "abc1234", files: []api.ChangedFile{{Path: "c.go"}}})
	if f := m.projects.changes.commitFiles; len(f) != 1 || f[0].Path != "c.go" {
		t.Errorf("commit files = %+v", f)
	}
}

func TestCommitDiffUsesRevAndDropsStale(t *testing.T) {
	m := changesFocused()
	mm, cmd := m.openDiff(api.ChangedFile{Path: "b.go", OrigPath: "a.go"}, "abc1234")
	m = mm.(model)
	runCmd(cmd)
	if p, _ := paramsFor(m, api.MethodWorkspaceDiff).(api.WorkspaceFileParams); p.Rev != "abc1234" || p.OrigPath != "a.go" {
		t.Fatalf("diff params = %+v, want rev abc1234 and the rename source", p)
	}
	m, _ = upd(m, wsDiffMsg{ws: "n1:w1", path: "b.go", diff: "@@\n+working tree"})
	if len(m.projects.fileView.lines) != 0 {
		t.Error("a working-tree diff must not fill a commit diff")
	}
	m, _ = upd(m, wsDiffMsg{ws: "n1:w1", path: "b.go", rev: "abc1234", diff: "@@ -1 +1 @@\n-a\n+commit side"})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "+commit side") || !strings.Contains(out, "abc1234 · b.go") {
		t.Errorf("the pane should show the commit diff under its sha:\n%s", out)
	}
}

func withCommits(m model, cs ...api.Commit) model {
	m, _ = upd(m, commitsMsg{ws: "n1:w1", commits: cs})
	return m
}

func commitN(i int) api.Commit {
	sha := fmt.Sprintf("%07x", i+0xabc0000)
	return api.Commit{SHA: sha, Short: sha, Subject: fmt.Sprintf("commit %d", i)}
}

func TestCommitsSectionRendersUnderChanges(t *testing.T) {
	m := wideWorkspace()
	m.projects.tree[0].Workspaces[0].TargetBranch = "main"
	m.projects.rebuild()
	m.projects.focus = focusFiles
	m.client = &recordingClient{}
	m, _ = upd(m, keyMsg("]"))
	m, _ = upd(m, changedFilesMsg{ws: "n1:w1", files: []api.ChangedFile{{Path: "a.go", Change: "modified"}}})
	m = withCommits(m, commitN(1), commitN(2))
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"M a.go", "COMMITS · vs main", "abc0001 commit 1", "abc0002 commit 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing %q:\n%s", want, out)
		}
	}
	m = withCommits(changesFocused())
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "no commits") {
		t.Errorf("an empty list should say so:\n%s", out)
	}
	e, _ := upd(changesFocused(), commitsMsg{ws: "n1:w1", err: fmt.Errorf("boom")})
	if out := ansi.Strip(e.View().Content); !strings.Contains(out, "error: boom") || !strings.Contains(out, "no changes") {
		t.Errorf("a commits error shows in its section and keeps the changes:\n%s", out)
	}
}

func TestCursorMovesAcrossFilesAndCommits(t *testing.T) {
	m := withCommits(changesFocused(api.ChangedFile{Path: "a.go"}), commitN(1), commitN(2))
	m, _ = upd(m, keyMsg("j"))
	if m.projects.changes.cursor != 1 {
		t.Fatalf("j from the last file should reach the first commit: cursor=%d", m.projects.changes.cursor)
	}
	if f := ansi.Strip(m.projectsFooter()); !strings.Contains(f, "enter files") {
		t.Errorf("footer on a commit = %q", f)
	}
	m, _ = upd(m, keyMsg("G"))
	if m.projects.changes.cursor != 2 {
		t.Errorf("G should reach the last commit: cursor=%d", m.projects.changes.cursor)
	}
}

func TestEnterOnCommitDrillsInAndBack(t *testing.T) {
	m := withCommits(changesFocused(api.ChangedFile{Path: "a.go"}), commitN(1), commitN(2))
	m.client = &recordingClient{}
	m, _ = upd(m, keyMsg("G"))
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	if c := m.projects.changes.commit; c == nil || c.SHA != "abc0002" {
		t.Fatalf("enter on a commit should open it: %+v", c)
	}
	if m.projects.focus != focusFiles {
		t.Error("opening a commit keeps focus in the sidebar")
	}
	m, _ = upd(m, commitFilesMsg{ws: "n1:w1", sha: "abc0002", files: []api.ChangedFile{{Path: "internal/c.go", Change: "added"}}})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "abc0002 commit 2") || !strings.Contains(out, "A internal/c.go") || strings.Contains(out, "COMMITS") {
		t.Errorf("the drill-in should show the commit and its files only:\n%s", out)
	}
	if f := ansi.Strip(m.projectsFooter()); !strings.Contains(f, "enter diff") || !strings.Contains(f, "esc back") || strings.Contains(f, "esc pane") {
		t.Errorf("drill-in footer = %q", f)
	}
	m, _ = upd(m, keyMsg("esc"))
	if m.projects.changes.commit != nil || m.projects.changes.cursor != 2 || m.projects.focus != focusFiles {
		t.Errorf("esc should go back to the same commit in the list: %+v focus=%v", m.projects.changes, m.projects.focus)
	}
	m, _ = upd(m, keyMsg("enter"))
	m, _ = upd(m, keyMsg("h"))
	if m.projects.changes.commit != nil {
		t.Error("h should also go back to the list")
	}
}

func TestCommitFileDiffCarriesRev(t *testing.T) {
	m := withCommits(changesFocused(), commitN(1))
	m.client = &recordingClient{}
	m, _ = upd(m, keyMsg("enter"))
	m, _ = upd(m, commitFilesMsg{ws: "n1:w1", sha: "abc0001", files: []api.ChangedFile{{Path: "b.go", OrigPath: "a.go", Change: "renamed"}}})
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	if p, _ := paramsFor(m, api.MethodWorkspaceDiff).(api.WorkspaceFileParams); p.Rev != "abc0001" || p.OrigPath != "a.go" || p.Path != "b.go" {
		t.Fatalf("diff params = %+v", p)
	}
	if m.projects.focus != focusPane {
		t.Errorf("focus should move to the diff: focus=%v", m.projects.focus)
	}
	m, _ = upd(m, keyMsg("esc"))
	if m.projects.fileView.open() || m.projects.focus != focusFiles || m.projects.changes.commit == nil {
		t.Errorf("esc should close the diff and return to the drilled-in commit: open=%v focus=%v commit=%v",
			m.projects.fileView.open(), m.projects.focus, m.projects.changes.commit)
	}
}

func TestRefreshClosesCommit(t *testing.T) {
	m := withCommits(changesFocused(), commitN(1))
	m, _ = upd(m, keyMsg("enter"))
	if m.projects.changes.commit == nil {
		t.Fatal("setup: enter should open the commit")
	}
	m.client = &recordingClient{}
	m, cmd := upd(m, keyMsg("r"))
	runCmd(cmd)
	if c := m.projects.changes; c.commit != nil || c.commits != nil || c.files != nil {
		t.Fatalf("r should close the commit and drop both lists: %+v", c)
	}
	if paramsFor(m, api.MethodWorkspaceChangedFiles) == nil || paramsFor(m, api.MethodWorkspaceCommits) == nil {
		t.Error("r should fetch both lists again")
	}
}

func TestCommitRowsFitWidth(t *testing.T) {
	long := api.Commit{SHA: "abc1234", Short: "abc1234", Subject: strings.Repeat("界🙂 wide subject ", 10)}
	if w := lipgloss.Width(commitRow(long, true, true, 30)); w > 30+screenMargin {
		t.Errorf("commit row is %d cells, wider than the sidebar's %d", w, 30+screenMargin)
	}
	m := withCommits(changesFocused(), long)
	assertFits(t, m.View().Content, m.width)
	if !strings.Contains(ansi.Strip(m.View().Content), "abc1234 界") {
		t.Error("the commit row should start with its sha and subject")
	}
}

func TestCommitCursorStaysVisible(t *testing.T) {
	cs := make([]api.Commit, 300)
	for i := range cs {
		cs[i] = commitN(i)
	}
	m := withCommits(changesFocused(), cs...)
	m, _ = upd(m, keyMsg("G"))
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "commit 299") {
		t.Errorf("the last commit should be on screen after G:\n%s", out)
	}
	m, _ = upd(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.projects.changes.cursor >= 299 {
		t.Error("^u should move the cursor up a page")
	}
}

func TestCursorStaysOnCommitWhenFilesArrive(t *testing.T) {
	m := filesFocused()
	m.client = &recordingClient{}
	m, _ = upd(m, keyMsg("]"))
	m = withCommits(m, commitN(1), commitN(2))
	m, _ = upd(m, keyMsg("G")) // the files are still loading
	m, _ = upd(m, changedFilesMsg{ws: "n1:w1", files: []api.ChangedFile{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}}})
	if c := m.projects.changes; c.cursor != 4 {
		t.Fatalf("late files should not move the cursor off commit 2: cursor=%d", c.cursor)
	}
	m, _ = upd(m, keyMsg("t"))
	m, _ = upd(m, changedFilesMsg{ws: "n1:w1", against: api.AgainstTarget, files: []api.ChangedFile{{Path: "x.go"}}})
	if c := m.projects.changes; c.cursor != 2 {
		t.Errorf("t should keep commit 2 selected: cursor=%d", c.cursor)
	}
	m, _ = upd(m, keyMsg("g"))
	m, _ = upd(m, keyMsg("t"))
	m, _ = upd(m, changedFilesMsg{ws: "n1:w1", files: []api.ChangedFile{{Path: "a.go"}, {Path: "b.go"}}})
	if c := m.projects.changes; c.cursor != 0 {
		t.Errorf("t on a file should stay on the files: cursor=%d", c.cursor)
	}
}

func TestRefreshDropsAnswersFromBefore(t *testing.T) {
	m := changesFocused()
	before := m.projects.changes.gen
	m, _ = upd(m, keyMsg("r"))
	m, _ = upd(m, commitsMsg{ws: "n1:w1", gen: before, commits: []api.Commit{commitN(9)}})
	m, _ = upd(m, changedFilesMsg{ws: "n1:w1", gen: before, files: []api.ChangedFile{{Path: "old.go"}}})
	if c := m.projects.changes; c.commits != nil || c.files != nil {
		t.Errorf("answers requested before r must be dropped: commits=%v files=%v", c.commits, c.files)
	}
}

func TestLOpensLikeEnterInChanges(t *testing.T) {
	m := changesFocused(api.ChangedFile{Path: "a.go"})
	m.client = &recordingClient{}
	m, _ = upd(m, commitsMsg{ws: "n1:w1", commits: []api.Commit{{SHA: "abc1234", Short: "abc1234", Subject: "s"}}})
	m, _ = upd(m, keyMsg("j")) // the commit row
	m, _ = upd(m, keyMsg("l"))
	if m.projects.changes.commit == nil {
		t.Fatal("l on a commit row should open the commit")
	}
	m, _ = upd(m, commitFilesMsg{ws: "n1:w1", sha: "abc1234", files: []api.ChangedFile{{Path: "b.go"}}})
	m, _ = upd(m, keyMsg("l"))
	if f := m.projects.fileView; !f.open() || f.path != "b.go" {
		t.Errorf("l on a commit's file should open its diff: %+v", f)
	}
}
