package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

func createTestModel(t *testing.T) model {
	t.Helper()
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.client = &recordingClient{}
	m.projects.tree[0].DefaultBranch = "main"
	m.projects.selectRow("n1:p1")
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 'n'})
	return res.(model)
}

// execCmd runs cmd and every batched command, returning all messages.
func execCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, execCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func typeText(m model, s string) model {
	for _, r := range s {
		res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = res.(model)
	}
	return m
}

func TestCreatePickerOpensWithDefaultTarget(t *testing.T) {
	m := createTestModel(t)
	if !m.projects.create.active || m.projects.create.projectID != "n1:p1" {
		t.Fatalf("picker not open for n1:p1: %+v", m.projects.create)
	}
	out := ansi.Strip(m.projectsView())
	for _, want := range []string{"New workspace in argus", "target: main", "Branches", "PRs", "Issues"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestCreateNewTabSubmitsNewSource(t *testing.T) {
	m := createTestModel(t)
	m = typeText(m, "feat-x")
	res, cmd := m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if !m.projects.create.creating || cmd == nil {
		t.Fatal("enter should start the create call")
	}
	var done *createDoneMsg
	for _, msg := range execCmd(cmd) { // the batch also holds the spinner tick
		if d, ok := msg.(createDoneMsg); ok {
			done = &d
		}
	}
	rc := m.client.(*recordingClient)
	p := rc.params[len(rc.params)-1].(api.WorkspaceCreateParams)
	if done == nil || p.Source != api.SourceNew || p.Branch != "feat-x" || p.TargetBranch != "" || done.source != api.SourceNew {
		t.Errorf("create params = %+v, done = %+v", p, done)
	}
}

func TestCreateTabsLoadListsAndFilter(t *testing.T) {
	m := createTestModel(t)
	res, cmd := m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = res.(model)
	if m.projects.create.tab != ctBranches || cmd == nil {
		t.Fatalf("tab should move to Branches and load: tab=%v", m.projects.create.tab)
	}
	res, _ = m.Update(branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{
		{Name: "main", Local: true, CheckedOut: true}, {Name: "fix-a", Local: true}, {Name: "other", Remote: true},
	}})
	m = res.(model)
	m = typeText(m, "jk") // j/k are filter text here, not movement
	if got := m.projects.create.branches.filter.Value(); got != "jk" {
		t.Errorf("filter = %q, want jk typed as text", got)
	}
	m.projects.create.branches.filter.SetValue("")
	m = typeText(m, "fix")
	if got := m.projects.create.branches.matches(); len(got) != 1 || got[0].Name != "fix-a" {
		t.Errorf("filtered = %+v", got)
	}
}

func TestCreateRefusesInUseBranch(t *testing.T) {
	m := createTestModel(t)
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = res.(model)
	res, _ = m.Update(branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "main", Local: true, CheckedOut: true}}})
	m = res.(model)
	res, cmd := m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if cmd != nil || m.projects.create.creating || m.flash == "" {
		t.Errorf("an in-use branch must not create: creating=%v flash=%q", m.projects.create.creating, m.flash)
	}
}

func TestCreatePRTabLocksTarget(t *testing.T) {
	m := createTestModel(t)
	for i := 0; i < 2; i++ {
		res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyTab})
		m = res.(model)
	}
	if m.projects.create.tab != ctPRs {
		t.Fatalf("tab = %v, want PRs", m.projects.create.tab)
	}
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m = res.(model)
	if m.projects.create.picking {
		t.Error("ctrl+t must do nothing on the PRs tab")
	}
	if !strings.Contains(ansi.Strip(m.projectsView()), "from PR base") {
		t.Error("PR tab should show the target as from PR base")
	}
}

func TestCreateTargetPickerSetsTarget(t *testing.T) {
	m := createTestModel(t)
	res, cmd := m.handleProjectsKey(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m = res.(model)
	if !m.projects.create.picking || cmd == nil {
		t.Fatal("ctrl+t should open the target picker and load branches")
	}
	res, _ = m.Update(branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "main", Local: true, CheckedOut: true}, {Name: "dev", Local: true}}})
	m = res.(model)
	m = typeText(m, "dev")
	res, _ = m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if m.projects.create.picking || m.projects.create.target != "dev" {
		t.Errorf("target = %q picking=%v, want dev and closed", m.projects.create.target, m.projects.create.picking)
	}
}

func TestCreateDoneSelectsWorkspaceAndCloses(t *testing.T) {
	m := createTestModel(t)
	m.projects.create.creating = true
	res, cmd := m.Update(createDoneMsg{seq: m.projects.create.seq, res: api.WorkspaceCreateResult{WorkspaceID: "n1:w9", Warning: "fetch failed; branched from local main"}, source: api.SourceNew})
	m = res.(model)
	if m.projects.create.active || m.projects.want != "n1:w9" || !strings.Contains(m.flash, "fetch failed") || cmd == nil {
		t.Errorf("after create: active=%v want=%q flash=%q", m.projects.create.active, m.projects.want, m.flash)
	}
}

func TestCreateErrorKeepsPickerOpen(t *testing.T) {
	m := createTestModel(t)
	m.projects.create.creating = true
	res, _ := m.Update(createDoneMsg{seq: m.projects.create.seq, err: errString("boom")})
	m = res.(model)
	if !m.projects.create.active || m.projects.create.creating || !strings.Contains(m.projects.create.err, "boom") {
		t.Errorf("error state: %+v", m.projects.create)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestIssueCreateOffersSpawn(t *testing.T) {
	m := createTestModel(t)
	m.projects.create.creating = true
	res, _ := m.Update(createDoneMsg{seq: m.projects.create.seq,
		res:    api.WorkspaceCreateResult{WorkspaceID: "n1:w9", Dir: "/repo/.worktrees/42-fix", Prompt: "Fix\n\nbody"},
		source: api.SourceIssue,
	})
	m = res.(model)
	if m.projects.offerSpawn == nil || !strings.Contains(m.projectsFooter(), "start an agent") {
		t.Fatalf("no spawn offer: offer=%v footer=%q", m.projects.offerSpawn, m.projectsFooter())
	}

	res, _ = m.handleProjectsKey(tea.KeyPressMsg{Code: 'n'}) // decline
	if mm := res.(model); mm.projects.offerSpawn != nil || mm.spawn.active() || mm.projects.create.active {
		t.Error("n should decline without spawning or opening the picker")
	}

	res, cmd := m.handleProjectsKey(tea.KeyPressMsg{Code: 'y'})
	m = res.(model)
	if !m.spawn.active() || m.spawn.nodeID != "n1" || !m.spawn.fixedCwd || cmd == nil {
		t.Fatalf("y should start the spawn flow on n1 with a fixed cwd: %+v", m.spawn)
	}
	res, _ = m.Update(spawnAgentsMsg{nodeID: "n1", agents: []api.AgentInfo{{ID: "claude"}}})
	m = res.(model)
	if m.spawn.step != spawnStepPrompt || m.spawn.cwd.Value() != "/repo/.worktrees/42-fix" || m.spawn.prompt.Value() != "Fix\n\nbody" {
		t.Errorf("spawn should skip the dir step with the issue prompt: step=%v cwd=%q prompt=%q", m.spawn.step, m.spawn.cwd.Value(), m.spawn.prompt.Value())
	}
	if !strings.Contains(ansi.Strip(m.projectsView()), "Fix") {
		t.Error("the projects screen should render the spawn flow")
	}
}

func TestHiddenPickerCreateOnlyReports(t *testing.T) {
	m := createTestModel(t)
	m.projects.create.creating = true
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyEscape}) // hide; the call runs on
	m = res.(model)
	m.projects.selectRow("n1:w1")
	res, _ = m.Update(createDoneMsg{seq: m.projects.create.seq,
		res:    api.WorkspaceCreateResult{WorkspaceID: "n1:w9", Prompt: "Fix"},
		source: api.SourceIssue,
	})
	m = res.(model)
	if m.projects.offerSpawn != nil || m.projects.want == "n1:w9" || !strings.Contains(m.flash, "workspace created") {
		t.Errorf("a hidden picker's result should only be flashed: offer=%v want=%q flash=%q", m.projects.offerSpawn, m.projects.want, m.flash)
	}
}

func TestTreeLooksUnfocusedUnderPicker(t *testing.T) {
	m := createTestModel(t)
	m.projects.focus = focusTree
	unfocused := m
	unfocused.projects.create = createState{}
	unfocused.projects.focus = focusPane
	if got, want := m.projectsTreePane(30, 20), unfocused.projectsTreePane(30, 20); got != want {
		t.Errorf("the tree should draw unfocused while the picker takes keys:\n got: %q\nwant: %q", got, want)
	}
}

func TestNonIssueCreateDoesNotOfferSpawn(t *testing.T) {
	m := createTestModel(t)
	res, _ := m.Update(createDoneMsg{seq: m.projects.create.seq, res: api.WorkspaceCreateResult{WorkspaceID: "n1:w9"}, source: api.SourceNew})
	if res.(model).projects.offerSpawn != nil {
		t.Error("only issue workspaces offer a spawn")
	}
}

func TestCreateKeyClearsStaleFlash(t *testing.T) {
	m := createTestModel(t)
	m.flash = "left over"
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = res.(model)
	if m.flash != "" || !strings.Contains(m.projectsFooter(), "create") {
		t.Errorf("a picker key should clear the flash and show the hints: flash=%q footer=%q", m.flash, m.projectsFooter())
	}
}

func TestTargetPickerShowsBranchLoadError(t *testing.T) {
	m := createTestModel(t)
	m.projects.create.branches.loaded = true
	m.projects.create.branches.err = errString("git exploded")
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m = res.(model)
	if !strings.Contains(ansi.Strip(m.projectsView()), "git exploded") {
		t.Error("the target picker should show the branch load error, not an empty list")
	}
}

func TestLateCreateResultLeavesNewerPickerAlone(t *testing.T) {
	m := createTestModel(t) // picker A
	oldSeq := m.projects.create.seq
	m.projects.create.creating = true
	res, _ := m.handleProjectsKey(tea.KeyPressMsg{Code: tea.KeyEscape}) // hide A; its call runs on
	m = res.(model)
	res, _ = m.startCreate("n1:p1") // picker B
	m = res.(model)
	m.projects.create.creating = true

	res, _ = m.Update(createDoneMsg{seq: oldSeq, err: errString("A failed")})
	m = res.(model)
	if !m.projects.create.creating || m.projects.create.err != "" || !strings.Contains(m.flash, "A failed") {
		t.Errorf("A's error must not reach B: creating=%v err=%q flash=%q", m.projects.create.creating, m.projects.create.err, m.flash)
	}
	res, _ = m.Update(createDoneMsg{seq: oldSeq, res: api.WorkspaceCreateResult{WorkspaceID: "n1:w9"}, source: api.SourceNew})
	m = res.(model)
	if !m.projects.create.active || m.projects.want == "n1:w9" {
		t.Errorf("A's success must not close B or move the cursor: active=%v want=%q", m.projects.create.active, m.projects.want)
	}
}
