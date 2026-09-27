package tui

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

func withKeymap(m model, raw map[string]map[string]string) model {
	km, errs := buildKeymap(raw, time.Second, "")
	if len(errs) > 0 {
		panic(strings.Join(errs, "; "))
	}
	m.keys = km
	return m
}

func ctrlKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

func TestUserKeyAddsToTheDefaults(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"global": {"<C-h>": "toggle show-hidden"}})
	m, _ = upd(m, ctrlKey('h'))
	if !m.left.tree.showHidden {
		t.Error("<C-h> should toggle hidden projects")
	}
	m = typeKeys(m, "z.")
	if m.left.tree.showHidden {
		t.Error("the default z. should still work")
	}
}

func TestEmptyValueRemovesTheDefault(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"project-tree": {"z.": ""}})
	m = typeKeys(m, "z.")
	if m.left.tree.showHidden {
		t.Error(`"z.": "" should remove z.`)
	}
}

func TestKeyMappedToAnotherCommandLeavesItsDefault(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"project-tree": {"z.": "toggle show-gone"}})
	m = typeKeys(m, "z.")
	if m.left.tree.showHidden || !m.left.tree.showGone {
		t.Errorf("z. should now show gone projects only: hidden=%v gone=%v", m.left.tree.showHidden, m.left.tree.showGone)
	}
}

func TestScreenSectionBeatsGlobal(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{
		"global":       {"<C-h>": "toggle show-hidden"},
		"project-tree": {"<C-h>": "toggle show-gone"},
	})
	m, _ = upd(m, ctrlKey('h'))
	if m.left.tree.showHidden || !m.left.tree.showGone {
		t.Error("the project-tree section should win over global")
	}
}

func TestShiftedLetterMatchesBothForms(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"project-tree": {"<S-y>": "toggle show-gone"}})
	m, _ = upd(m, tea.KeyPressMsg{Code: 'y', Text: "Y", Mod: tea.ModShift})
	if !m.left.tree.showGone {
		t.Error("a press with Text Y should match <S-y>")
	}
}

func TestGlobalMappingSkipsScreensWithoutItsCommand(t *testing.T) {
	raw := map[string]map[string]string{"global": {"j": "toggle show-hidden"}}
	km, errs := buildKeymap(raw, time.Second, "")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var down binding
	for _, b := range screenBindings["logs"] {
		if b.name == "scroll down" {
			down = b
		}
	}
	if keys := km.screens["logs"].keys(down); !strings.Contains(strings.Join(keys, " "), "j") {
		t.Errorf("logs has no toggle show-hidden, so j must still scroll: %q", keys)
	}
	if _, taken := km.screens["project-tree"].takenBy["j"]; !taken {
		t.Error("on project-tree, j belongs to the user's mapping")
	}
}

func TestBuildKeymapReportsBadEntries(t *testing.T) {
	_, errs := buildKeymap(map[string]map[string]string{
		"nowhere":      {"a": "quit"},
		"project-tree": {"<X-a>": "quit", "b": "$lazygit", "c": "goto tpo", "gt": "workspace pick-target", "d": "toggle show-gone"},
		"transcript":   {"e": "toggle show-hidden"},
	}, time.Second, "")
	want := []string{
		`keymap: unknown screen "nowhere"`,
		`keymap: project-tree "<X-a>": unknown modifier "X" in <X-a>`,
		`keymap: project-tree "b": shell commands are not supported yet`,
		`keymap: project-tree "c": unknown argument "tpo" for goto (bottom, top)`,
		`keymap: project-tree "gt": workspace pick-target works only in a text input, which has no sequences`,
		`keymap: transcript "e": unknown command "toggle show-hidden"`,
	}
	if strings.Join(errs, "\n") != strings.Join(want, "\n") {
		t.Errorf("errors:\n%s\nwant:\n%s", strings.Join(errs, "\n"), strings.Join(want, "\n"))
	}
}

func TestBuildKeymapRecordsAKittyKey(t *testing.T) {
	km, _ := buildKeymap(map[string]map[string]string{"global": {"<C-S-b>": "toggle left-sidebar"}}, time.Second, "")
	if km.kittyKey != "<C-S-b>" {
		t.Errorf("kittyKey = %q", km.kittyKey)
	}
}

func TestTextInputOnly(t *testing.T) {
	if textInputOnly("prev") {
		t.Error("prev appears outside text-input sets; textInputOnly must be false")
	}
	if !textInputOnly("workspace pick-target") {
		t.Error("workspace pick-target is only in createKeys; textInputOnly must be true")
	}
	if !textInputOnly("answer submit") {
		t.Error("answer submit is only in promptKeys; textInputOnly must be true")
	}
}

func TestSequenceForNonTextInputCommandIsAllowed(t *testing.T) {
	_, errs := buildKeymap(map[string]map[string]string{
		"project-tree": {"gz": "prev"},
	}, time.Second, "")
	if len(errs) > 0 {
		t.Errorf("gz: prev should be valid on project-tree, got errors: %v", errs)
	}
}

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		return slices.ContainsFunc(msg, quits)
	}
	return false
}

func TestMappedQuitKeyQuits(t *testing.T) {
	raw := map[string]map[string]string{"global": {"<C-q>": "quit"}}
	home := withKeymap(homeTestModel(), raw)
	tree := withKeymap(homeTestModel(), raw)
	tree = withView(tree, viewTree)
	tree = withFocus(tree, leftSidebar)
	for name, m := range map[string]model{"home": home, "tree": tree} {
		if _, cmd := upd(m, ctrlKey('q')); !quits(cmd) {
			t.Errorf("%s: <C-q> mapped to quit should quit", name)
		}
	}
}

func TestRemovedQuitKeyDoesNotQuitFromTheTree(t *testing.T) {
	m := withKeymap(homeTestModel(), map[string]map[string]string{"project-tree": {"Q": ""}})
	m = withView(m, viewTree)
	m = withFocus(m, leftSidebar)
	if _, cmd := upd(m, keyMsg("Q")); quits(cmd) {
		t.Error(`"Q": "" should stop Q from quitting on the tree`)
	}
}

func TestTreeQuitLabelFollowsQuitNotBack(t *testing.T) {
	m := withKeymap(homeTestModel(), map[string]map[string]string{"project-tree": {"<C-x>": "back"}})
	m = withView(m, viewTree)
	m = withFocus(m, leftSidebar)
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "Q quit") {
		t.Errorf("the tree footer should keep Q quit after back is remapped:\n%s", f)
	}
	m.showHelp = true
	h := ansi.Strip(m.projectsHelpView())
	if !strings.Contains(h, "quit · quit") || strings.Contains(h, "^x  quit") {
		t.Errorf("the help should list quit under its own command:\n%s", h)
	}
}

func TestMappedTargetKeyOpensThePicker(t *testing.T) {
	m := withKeymap(createTestModel(t), map[string]map[string]string{"project-tree": {"<C-g>": "workspace pick-target"}})
	if m, _ = upd(m, ctrlKey('g')); !createOf(m).picking {
		t.Error("<C-g> mapped to workspace pick-target should open the target picker")
	}
}

func TestDockFollowsMappedKeys(t *testing.T) {
	raw := map[string]map[string]string{"session-dock": {
		"<C-x>": "next", "<C-y>": "option select", "<C-l>": "tab next",
		"<M-h>": "tab prev", "<C-o>": "answer submit",
	}}
	m := withKeymap(promptModel(&session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{
		{Question: "Many", Options: []string{"A", "B"}, MultiSelect: true},
		{Question: "One", Options: []string{"C", "D"}},
	}}), raw)
	m, _ = upd(m, ctrlKey('x'))
	m, _ = upd(m, ctrlKey('y'))
	if m.dock.qSel(0) != 1 || !m.dock.toggles[0][1] {
		t.Fatalf("<C-x> moves down and <C-y> toggles: sel=%d toggles=%v", m.dock.qSel(0), m.dock.toggles[0])
	}
	m, _ = upd(m, ctrlKey('l'))
	m, _ = upd(m, tea.KeyPressMsg{Code: 'h', Mod: tea.ModAlt})
	m, _ = upd(m, ctrlKey('l'))
	m, _ = upd(m, ctrlKey('o'))
	if m.dock.tab != 2 || m.dock.chosen[1] != 0 {
		t.Fatalf("<C-l>, <M-h>, <C-l>, then <C-o> commits question 2: tab=%d chosen=%v", m.dock.tab, m.dock.chosen)
	}

	d := withKeymap(promptModel(&session.Interaction{
		Kind: session.InteractionPermission, ToolName: "Bash",
		Options: []session.DecisionOption{{Label: "Allow", Value: "allow"}, {Label: "Deny", Value: "deny", Reject: true}},
	}), raw)
	d, _ = upd(d, ctrlKey('x'))
	if d.dock.decisionSel != 1 {
		t.Fatalf("<C-x> should move the decision down: sel=%d", d.dock.decisionSel)
	}
	if d, cmd := upd(d, ctrlKey('o')); d.focused != mainPane || cmd == nil {
		t.Errorf("<C-o> should submit the decision: focus=%v cmd=%v", d.focused, cmd)
	}
}

func TestLiteralKeyFootersIgnoreProjectsRemaps(t *testing.T) {
	raw := map[string]map[string]string{"project-tree": {"<C-x>": "back", "<C-a>": "open", "<C-p>": "prev", "<C-n>": "focus next"}}
	m := withKeymap(createTestModel(t), raw)
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "enter create") || !strings.Contains(f, "tab next tab") || !strings.Contains(f, "esc cancel") {
		t.Errorf("the create footer names the keys its handler reads:\n%s", f)
	}
	m = withCreate(m, func(p *createComp) { p.picking = true })
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "↑/↓ move") || !strings.Contains(f, "enter select") || !strings.Contains(f, "esc back") {
		t.Errorf("the target picker footer names the keys its handler reads:\n%s", f)
	}
	r := withKeymap(projectsTestModel(), raw)
	r = withPicker(r, retargetComp{pick: newBranchPicker()})
	if f := ansi.Strip(r.currentFooter()); !strings.Contains(f, "↑/↓ move") || !strings.Contains(f, "enter select") || !strings.Contains(f, "esc cancel") {
		t.Errorf("the retarget footer names the keys its handler reads:\n%s", f)
	}
}

func TestNeedsKitty(t *testing.T) {
	for lhs, want := range map[string]bool{
		"<M-+>": false, "<C-b>": false, "<M-A>": false, "<S-Tab>": false, "g": false,
		"<C-]>": false, "<C-@>": false, "<C-_>": false, "<C-Space>": false, "<C-Up>": false,
		"<C-S-b>": true, "g<M-C-t>": true, "<D-x>": true,
		"<C-+>": true, "<C-.>": true, "<C-1>": true, "<C-;>": true,
		"<C-CR>": true, "<S-CR>": true, "<C-Tab>": true, "<C-BS>": true, "<S-BS>": true,
	} {
		seq, err := parseKeySeq(lhs)
		if err != nil {
			t.Fatal(err)
		}
		if got := needsKitty(seq); got != want {
			t.Errorf("needsKitty(%s) = %v, want %v", lhs, got, want)
		}
	}
}

func TestPassThroughOnlyCommandsAreNotMappable(t *testing.T) {
	_, errs := buildKeymap(map[string]map[string]string{
		"session-dock": {"a": "help"},
		"transcript":   {"b": "help"},
		"project-tree": {"c": "focus-right-sidebar", "d": "focus prompt"},
	}, time.Second, "")
	want := []string{
		`keymap: project-tree "c": unknown command "focus-right-sidebar"`,
		`keymap: project-tree "d": unknown command "focus prompt"`,
		`keymap: session-dock "a": unknown command "help"`,
		`keymap: transcript "b": unknown command "help"`,
	}
	if strings.Join(errs, "\n") != strings.Join(want, "\n") {
		t.Errorf("nothing acts on these commands there, so mapping them must fail:\n%s", strings.Join(errs, "\n"))
	}
}

func TestLeaderReplacesTheToken(t *testing.T) {
	km, errs := buildKeymap(map[string]map[string]string{"project-tree": {"<Leader>x": "toggle show-gone"}}, 0, ",")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if !slices.Contains(km.screens["project-tree"].addIDs["toggle show-gone"], seqMark+", x") {
		t.Errorf("<Leader>x should become ,x: %v", km.screens["project-tree"].addIDs)
	}
}

func TestBadLeaderFallsBackToSpace(t *testing.T) {
	km, errs := buildKeymap(map[string]map[string]string{"project-tree": {"<Leader>x": "toggle show-gone"}}, 0, "ab")
	if len(errs) != 1 || !strings.Contains(errs[0], `keymap: tui.leader-key "ab"`) {
		t.Errorf("errs = %v", errs)
	}
	if !slices.Contains(km.screens["project-tree"].addIDs["toggle show-gone"], seqMark+"space x") {
		t.Error("a bad leader falls back to <Space>")
	}
}

func TestLeaderSpaceTypesInTheDock(t *testing.T) {
	m := promptModel(&session.Interaction{Kind: session.InteractionIdle})
	m = withFocus(m, sessionDock)
	for _, k := range []tea.KeyPressMsg{{Code: 'h', Text: "h"}, {Code: ' ', Text: " "}, {Code: 'i', Text: "i"}} {
		m, _ = upd(m, k)
	}
	if got := m.dock.reply.Value(); got != "h i" {
		t.Errorf("the dock types the leader key as text: reply = %q, want %q", got, "h i")
	}
}

func TestNoDefaultKeyWaits(t *testing.T) {
	for _, screen := range slices.Sorted(maps.Keys(defaultKeymap.screens)) {
		sk := defaultKeymap.screens[screen]
		for _, short := range sk.seqs {
			if len(short.seq) != 1 {
				continue
			}
			for _, long := range sk.seqs {
				if len(long.seq) > 1 && slices.ContainsFunc(short.seq[0], func(k string) bool { return slices.Contains(long.seq[0], k) }) {
					t.Errorf("%s: %s (%s) waits for %s (%s)", screen, keyLabel(short.id), short.cmd, keyLabel(long.id), long.cmd)
				}
			}
		}
	}
}

func TestSpaceSelectsAndUnselectsAnOption(t *testing.T) {
	multi := &session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{
		{Question: "Many", Options: []string{"A", "B"}, MultiSelect: true},
	}}
	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	m := promptModel(multi)
	m, _ = upd(m, space)
	if !m.dock.toggles[0][0] {
		t.Fatal("<Space> must select the option")
	}
	m, _ = upd(m, space)
	if m.dock.toggles[0][0] {
		t.Fatal("<Space> must unselect the selected option")
	}

	m = withKeymap(promptModel(multi), map[string]map[string]string{"session-dock": {"<C-y>": "option select"}})
	m, _ = upd(m, ctrlKey('y'))
	m, _ = upd(m, ctrlKey('y'))
	if !m.dock.toggles[0][0] {
		t.Error("a key mapped to option select only must not unselect")
	}
}
