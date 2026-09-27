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
	m.left.tree.data[0].DefaultBranch = "main"
	m = selectRow(m, "n1:p1")
	res, _ := m.runKey(tea.KeyPressMsg{Code: 'a'})
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
		res, _ := m.runKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = res.(model)
	}
	return m
}

func TestCreatePickerOpensWithDefaultTarget(t *testing.T) {
	m := createTestModel(t)
	if !createOpen(m) || createOf(m).projectID != "n1:p1" {
		t.Fatalf("picker not open for n1:p1: %+v", createOf(m))
	}
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"New workspace in argus", "target: main", "Branches", "PRs", "Issues"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestCreateNewTabSubmitsNewSource(t *testing.T) {
	m := createTestModel(t)
	m = typeText(m, "feat-x")
	res, cmd := m.runKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if !createOf(m).creating || cmd == nil {
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
	res, cmd := m.runKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = res.(model)
	if createOf(m).tab != ctBranches || cmd == nil {
		t.Fatalf("tab should move to Branches and load: tab=%v", createOf(m).tab)
	}
	res, _ = m.Update(branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{
		{Name: "main", Local: true, CheckedOut: true}, {Name: "fix-a", Local: true}, {Name: "other", Remote: true},
	}})
	m = res.(model)
	m = typeText(m, "jk") // j/k are filter text here, not movement
	if got := createOf(m).branches.filter.Value(); got != "jk" {
		t.Errorf("filter = %q, want jk typed as text", got)
	}
	m = withCreate(m, func(p *createPicker) { p.branches.filter.SetValue("") })
	m = typeText(m, "fix")
	if got := createOf(m).branches.matches(); len(got) != 1 || got[0].Name != "fix-a" {
		t.Errorf("filtered = %+v", got)
	}
}

func TestCreateRefusesInUseBranch(t *testing.T) {
	m := createTestModel(t)
	res, _ := m.runKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = res.(model)
	res, _ = m.Update(branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "main", Local: true, CheckedOut: true}}})
	m = res.(model)
	res, cmd := m.runKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if cmd != nil || createOf(m).creating || createOf(m).err == "" {
		t.Errorf("an in-use branch must not create: creating=%v err=%q", createOf(m).creating, createOf(m).err)
	}
}

func TestCreatePRTabLocksTarget(t *testing.T) {
	m := createTestModel(t)
	for i := 0; i < 2; i++ {
		res, _ := m.runKey(tea.KeyPressMsg{Code: tea.KeyTab})
		m = res.(model)
	}
	if createOf(m).tab != ctPRs {
		t.Fatalf("tab = %v, want PRs", createOf(m).tab)
	}
	res, _ := m.runKey(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m = res.(model)
	if createOf(m).picking {
		t.Error("ctrl+t must do nothing on the PRs tab")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "from PR base") {
		t.Error("PR tab should show the target as from PR base")
	}
}

func TestCreateTargetPickerSetsTarget(t *testing.T) {
	m := createTestModel(t)
	res, cmd := m.runKey(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m = res.(model)
	if !createOf(m).picking || cmd == nil {
		t.Fatal("ctrl+t should open the target picker and load branches")
	}
	res, _ = m.Update(branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "main", Local: true, CheckedOut: true}, {Name: "dev", Local: true}}})
	m = res.(model)
	m = typeText(m, "dev")
	res, _ = m.runKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)
	if createOf(m).picking || createOf(m).target != "dev" {
		t.Errorf("target = %q picking=%v, want dev and closed", createOf(m).target, createOf(m).picking)
	}
}

func TestCreateDoneSelectsWorkspaceAndCloses(t *testing.T) {
	m := createTestModel(t)
	m = withCreate(m, func(p *createPicker) { p.creating = true })
	res, cmd := m.Update(createDoneMsg{seq: createOf(m).seq, res: api.WorkspaceCreateResult{WorkspaceID: "n1:w9", Warning: "fetch failed; branched from local main"}, source: api.SourceNew})
	m = res.(model)
	if createOpen(m) || m.left.tree.want != "n1:w9" || !strings.Contains(m.flash, "fetch failed") || cmd == nil {
		t.Errorf("after create: active=%v want=%q flash=%q", createOpen(m), m.left.tree.want, m.flash)
	}
}

func TestCreateErrorKeepsPickerOpen(t *testing.T) {
	m := createTestModel(t)
	m = withCreate(m, func(p *createPicker) { p.creating = true })
	res, _ := m.Update(createDoneMsg{seq: createOf(m).seq, err: errString("boom")})
	m = res.(model)
	if !createOpen(m) || createOf(m).creating || !strings.Contains(createOf(m).err, "boom") {
		t.Errorf("error state: %+v", createOf(m))
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestIssueCreateOffersSpawn(t *testing.T) {
	m := createTestModel(t)
	m = withCreate(m, func(p *createPicker) { p.creating = true })
	res, _ := m.Update(createDoneMsg{seq: createOf(m).seq,
		res:    api.WorkspaceCreateResult{WorkspaceID: "n1:w9", Dir: "/repo/.worktrees/42-fix", Prompt: "Fix\n\nbody"},
		source: api.SourceIssue,
	})
	m = res.(model)
	if m.left.tree.offerSpawn == nil || !strings.Contains(m.currentFooter(), "start an agent") {
		t.Fatalf("no spawn offer: offer=%v footer=%q", m.left.tree.offerSpawn, m.currentFooter())
	}

	res, _ = m.runKey(tea.KeyPressMsg{Code: 'n'}) // decline
	if mm := res.(model); mm.left.tree.offerSpawn != nil || spawnOpen(mm) || createOpen(mm) {
		t.Error("n should decline without spawning or opening the picker")
	}

	res, cmd := m.runKey(tea.KeyPressMsg{Code: 'y'})
	m = res.(model)
	if !spawnOpen(m) || spawnOf(m).nodeID != "n1" || !spawnOf(m).fixedCwd || cmd == nil {
		t.Fatalf("y should start the spawn flow on n1 with a fixed cwd: %+v", spawnOf(m))
	}
	res, _ = m.Update(spawnAgentsMsg{nodeID: "n1", agents: []api.AgentInfo{{ID: "claude"}}})
	m = res.(model)
	if spawnOf(m).step != spawnStepPrompt || spawnOf(m).cwd.Value() != "/repo/.worktrees/42-fix" || spawnOf(m).prompt.Value() != "Fix\n\nbody" {
		t.Errorf("spawn should skip the dir step with the issue prompt: step=%v cwd=%q prompt=%q", spawnOf(m).step, spawnOf(m).cwd.Value(), spawnOf(m).prompt.Value())
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Fix") {
		t.Error("the projects screen should render the spawn flow")
	}
}

func TestHiddenPickerCreateOnlyReports(t *testing.T) {
	m := createTestModel(t)
	m = withCreate(m, func(p *createPicker) { p.creating = true })
	seq := createOf(m).seq
	res, _ := m.runKey(tea.KeyPressMsg{Code: tea.KeyEscape}) // hide; the call runs on
	m = res.(model)
	m = selectRow(m, "n1:w1")
	res, _ = m.Update(createDoneMsg{seq: seq,
		res:    api.WorkspaceCreateResult{WorkspaceID: "n1:w9", Prompt: "Fix"},
		source: api.SourceIssue,
	})
	m = res.(model)
	if m.left.tree.offerSpawn != nil || m.left.tree.want == "n1:w9" || !strings.Contains(m.flash, "created workspace") {
		t.Errorf("a hidden picker's result should only be flashed: offer=%v want=%q flash=%q", m.left.tree.offerSpawn, m.left.tree.want, m.flash)
	}
}

func TestTreeLooksUnfocusedUnderPicker(t *testing.T) {
	m := createTestModel(t)
	unfocused := closePicker(m)
	unfocused = withFocus(unfocused, mainPane)
	if got, want := treePane(m, 30, 20), treePane(unfocused, 30, 20); got != want {
		t.Errorf("the tree should draw unfocused while the picker takes keys:\n got: %q\nwant: %q", got, want)
	}
}

func TestPresetSpawnShowsWhereItRuns(t *testing.T) {
	m := createTestModel(t)
	m = closePicker(m)
	cmd := m.beginPresetSpawn("n1", "/repo/.worktrees/42-fix", "Fix")
	_ = cmd
	m, _ = upd(m, spawnAgentsMsg{nodeID: "n1", agents: []api.AgentInfo{{ID: "claude"}}})
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "in /repo/.worktrees/42-fix") {
		t.Errorf("the prompt step should show the directory it runs in:\n%s", out)
	}
}

func toPRsTab(m model) model {
	for createOf(m).tab != ctPRs {
		m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyTab})
	}
	return m
}

func TestPRsTabRetriesAfterAnError(t *testing.T) {
	m := toPRsTab(createTestModel(t))
	m, _ = upd(m, prsMsg{projectID: "n1:p1", err: errString("gh down")})
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, cmd := upd(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	rc := &recordingClient{}
	m.client = rc
	runCmd(cmd)
	out := ansi.Strip(m.View().Content)
	if strings.Contains(out, "gh down") || !strings.Contains(out, "loading…") {
		t.Errorf("coming back to a failed tab should load it again:\n%s", out)
	}
}

func TestPRsTabSaysWhenTruncatedAndSpins(t *testing.T) {
	m := toPRsTab(createTestModel(t))
	if !m.spinning {
		t.Error("switching to a loading list should start the spinner")
	}
	prs := make([]api.PRInfo, 100)
	for i := range prs {
		prs[i] = api.PRInfo{Number: i + 1, Title: "t"}
	}
	m, _ = upd(m, prsMsg{projectID: "n1:p1", prs: prs, truncated: true})
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "first 100") {
		t.Errorf("a cut-off list should say so:\n%s", out)
	}
}

func TestCreateErrorsShowInPickerAndClearOnEdit(t *testing.T) {
	m := createTestModel(t)
	m = withCreate(m, func(p *createPicker) { p.creating = true })
	m, _ = upd(m, createDoneMsg{seq: createOf(m).seq, err: errString("boom")})
	m, _ = upd(m, keyMsg("x"))
	if createOf(m).err != "" {
		t.Errorf("editing the name should clear the error: %q", createOf(m).err)
	}

	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyTab}) // Branches
	m, _ = upd(m, branchesMsg{projectID: "n1:p1", branches: []api.BranchInfo{{Name: "busy", CheckedOut: true}}})
	m, _ = upd(m, keyMsg("enter"))
	if m.flash != "" || !strings.Contains(createOf(m).err, "busy is checked out in another workspace") {
		t.Errorf("a busy branch should report in the picker: flash=%q err=%q", m.flash, createOf(m).err)
	}
}

func TestCreateFlashNamesTheWorkspace(t *testing.T) {
	m := createTestModel(t)
	m = withCreate(m, func(p *createPicker) { p.creating = true })
	m, _ = upd(m, createDoneMsg{seq: createOf(m).seq, res: api.WorkspaceCreateResult{WorkspaceID: "n1:w9", Dir: "/repo/.worktrees/login", Warning: "fetch failed"}, source: api.SourceNew})
	if m.flash != "created workspace login · fetch failed" {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestNonIssueCreateDoesNotOfferSpawn(t *testing.T) {
	m := createTestModel(t)
	res, _ := m.Update(createDoneMsg{seq: createOf(m).seq, res: api.WorkspaceCreateResult{WorkspaceID: "n1:w9"}, source: api.SourceNew})
	if res.(model).left.tree.offerSpawn != nil {
		t.Error("only issue workspaces offer a spawn")
	}
}

func TestCreateKeyClearsStaleFlash(t *testing.T) {
	m := createTestModel(t)
	m.flash = "left over"
	res, _ := m.runKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = res.(model)
	if out := ansi.Strip(m.View().Content); m.flash != "" || !strings.Contains(out, "enter create") {
		t.Errorf("a picker key should clear the flash and show the hints: flash=%q view=\n%s", m.flash, out)
	}
}

func TestTargetPickerShowsBranchLoadError(t *testing.T) {
	m := createTestModel(t)
	m = withCreate(m, func(p *createPicker) { p.branches.loaded = true })
	m = withCreate(m, func(p *createPicker) { p.branches.err = errString("git exploded") })
	res, _ := m.runKey(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m = res.(model)
	if !strings.Contains(ansi.Strip(m.View().Content), "git exploded") {
		t.Error("the target picker should show the branch load error, not an empty list")
	}
}

func TestLateCreateResultLeavesNewerPickerAlone(t *testing.T) {
	m := createTestModel(t) // picker A
	oldSeq := createOf(m).seq
	m = withCreate(m, func(p *createPicker) { p.creating = true })
	res, _ := m.runKey(tea.KeyPressMsg{Code: tea.KeyEscape}) // hide A; its call runs on
	m = res.(model)
	m = openCreate(m, "n1:p1") // picker B
	m = withCreate(m, func(p *createPicker) { p.creating = true })

	res, _ = m.Update(createDoneMsg{seq: oldSeq, err: errString("A failed")})
	m = res.(model)
	if !createOf(m).creating || createOf(m).err != "" || !strings.Contains(m.flash, "A failed") {
		t.Errorf("A's error must not reach B: creating=%v err=%q flash=%q", createOf(m).creating, createOf(m).err, m.flash)
	}
	res, _ = m.Update(createDoneMsg{seq: oldSeq, res: api.WorkspaceCreateResult{WorkspaceID: "n1:w9"}, source: api.SourceNew})
	m = res.(model)
	if !createOpen(m) || m.left.tree.want == "n1:w9" {
		t.Errorf("A's success must not close B or move the cursor: active=%v want=%q", createOpen(m), m.left.tree.want)
	}
}

func TestPickerOpensWithoutMovingFocus(t *testing.T) {
	m := createTestModel(t)
	if !createOpen(m) || m.focused != leftSidebar {
		t.Errorf("open %v focus %v, want the picker open and focus on the tree", createOpen(m), m.focused)
	}
}

func TestPickerDrawnInsideMainPane(t *testing.T) {
	m := createTestModel(t)
	row, col := findBlock(t, m, "New workspace in argus")
	r := m.mainRect()
	if col < r.Min.X || col >= r.Max.X || row < r.Min.Y || row >= r.Max.Y {
		t.Errorf("title at row %d column %d, outside the main pane %v", row, col, r)
	}
}

func TestPickerHintsInsideTheBox(t *testing.T) {
	lines := frameLines(createTestModel(t))
	if strings.Contains(lines[len(lines)-1], "enter create") {
		t.Errorf("the footer row shows the picker keys: %q", lines[len(lines)-1])
	}
	for _, l := range lines {
		if strings.Contains(l, "enter create") && strings.Contains(l, "│") {
			return
		}
	}
	t.Error("no line in the box shows the picker keys")
}

func TestColonTypesIntoThePicker(t *testing.T) {
	m, _ := upd(createTestModel(t), keyMsg(":"))
	if _, open := cmdLineOf(m); open || createOf(m).name.Value() != ":" {
		t.Errorf("command line open %v, name %q; want the : in the name", open, createOf(m).name.Value())
	}
}

func TestKeyAfterPickerClosesReachesTheTree(t *testing.T) {
	m := createTestModel(t)
	cursor := m.left.tree.cursor
	m, _ = upd(m, keyMsg("esc"))
	m, _ = upd(m, keyMsg("j"))
	if createOpen(m) || m.left.tree.cursor == cursor {
		t.Errorf("open %v cursor %d→%d: esc must close the picker and j must move the tree", createOpen(m), cursor, m.left.tree.cursor)
	}
}

func TestPasteIntoTheCreatePicker(t *testing.T) {
	m, _ := upd(createTestModel(t), tea.PasteMsg{Content: "feat-x"})
	if got := createOf(m).name.Value(); got != "feat-x" {
		t.Errorf("name after a paste = %q", got)
	}
}

func TestCreatePickerShowsItsInputInAShortTerminal(t *testing.T) {
	m, _ := upd(createTestModel(t), tea.WindowSizeMsg{Width: 50, Height: 12})
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "branch:") {
		t.Errorf("the branch input is cut off at 50x12:\n%s", out)
	}
}
