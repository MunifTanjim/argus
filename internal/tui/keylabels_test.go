package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestKeyLabel(t *testing.T) {
	for id, want := range map[string]string{
		"ctrl+b": "^b", "up": "↑", "down": "↓", "left": "←", "right": "→", "g": "g",
		seqMark + "g g": "gg", seqMark + "g ctrl+b": "g^b",
		"space":              "␣",
		seqMark + "space o":  "␣o",
		seqMark + "ctrl+w l": "^wl",
	} {
		if got := keyLabel(id); got != want {
			t.Errorf("keyLabel(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestFooterShowsARemappedPair(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.showHelp = false
	m = selectRow(m, "n1:w2")
	m = withFile(m, fileComp{ws: "n1:w2", path: "a.go"})
	m = withFocus(m, mainPane)
	before := ansi.Strip(m.currentFooter())
	m = withKeymap(m, map[string]map[string]string{"global": {"gg": ":goto top<CR>"}})
	after := ansi.Strip(m.currentFooter())
	if !strings.Contains(before, "g/G") || !strings.Contains(after, "gg/G") {
		t.Errorf("footer before %q, after %q", before, after)
	}
}

func TestHelpNamesTheMappedExpandKey(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"project-tree": {"l": "<Nop>", "<Right>": "<Nop>", "zo": "<Nop>", "o": ":fold open<CR>"}})
	if out := ansi.Strip(m.projectsHelpView()); !strings.Contains(out, "open; o opens Home/workspace") {
		t.Errorf("help should name o, the mapped expand key:\n%s", out)
	}
}

func TestHelpShowsCommandNamesAndUserKeys(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"global": {"<C-y>": ":toggle show-hidden<CR>"}})
	m.width, m.height = 160, 50
	out := ansi.Strip(m.projectsHelpView())
	if !strings.Contains(out, "toggle show-hidden") || !strings.Contains(out, "^y") {
		t.Errorf("help should show the command name and ^y:\n%s", out)
	}
}

func TestAnyKeyHintsIgnoreRemaps(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"project-tree": {"<F1>": ":help<CR>", "<C-x>": ":back<CR>"}})
	m.width, m.height = 160, 40
	m.showHelp = true
	if f := ansi.Strip(m.helpScreen()); !strings.Contains(f, "any key close") {
		t.Errorf("the help screen footer should say any key closes it:\n%s", f)
	}
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "any key close") {
		t.Errorf("the projects help footer should say any key closes it:\n%s", f)
	}
}

func TestRedactFootersShowEffectiveKeys(t *testing.T) {
	m := withKeymap(newRedactModel(), map[string]map[string]string{"transcript": {
		"<C-r>": ":redaction add<CR>", "<C-l>": ":redaction list<CR>", "<C-w>": ":redaction save<CR>",
		"<C-x>": ":redaction remove<CR>", "gz": ":goto bottom<CR>",
	}})
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "redact: ^r add secret") {
		t.Errorf("idle footer: %q", f)
	}
	m = withTr(m, func(t *transcriptComp) { t.redact.literals = []string{"a"} })
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "^r add · ^l list · ^w save") {
		t.Errorf("queued footer: %q", f)
	}
	m = withTr(m, func(t *transcriptComp) { t.redact.listActive = true })
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "↑/↓ move · ^x delete · esc close") {
		t.Errorf("list footer: %q", f)
	}
	m.keyBuf = []tea.KeyPressMsg{keyMsg("g")}
	if f := ansi.Strip(m.currentFooter()); !strings.Contains(f, "g…") {
		t.Errorf("the list footer should show the pending keys: %q", f)
	}
}

func TestLabelUsesTheLabeledBindingsOwnKeys(t *testing.T) {
	m := testModel()
	m = withView(m, viewSession)
	m = withFocus(m, rightSidebar)
	m = withKeymap(m, map[string]map[string]string{"file-tree": {"<Up>": "<Nop>"}})
	if got := m.helpBinding(projectsKeys.Up).Help().Key; !strings.HasPrefix(got, "k/") {
		t.Errorf("the files sidebar's prev label should start with its own k: %q", got)
	}
}

func TestFooterBuildsLabelsFromEffectiveKeys(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 120, 30
	m.showHelp = false
	m = selectRow(m, "n1:w2")
	m = withFile(m, fileComp{ws: "n1:w2", path: "a.go"})
	m = withFocus(m, mainPane)
	f := ansi.Strip(m.currentFooter())
	if !strings.Contains(f, "gg/G") {
		t.Errorf("file view footer should show gg/G from effective keys: %q", f)
	}
	m2 := projectsTestModel()
	m2.width, m2.height = 120, 30
	m2 = selectRow(m2, "n1:w1")
	m2 = withFocus(m2, mainPane)
	f2 := ansi.Strip(m2.currentFooter())
	if !strings.Contains(f2, "dd kill") {
		t.Errorf("pane footer should show dd from effective keys: %q", f2)
	}
}

func TestHelpKeysSkipQuietDefaultsUnlessMapped(t *testing.T) {
	if got := projectsTestModel().helpKeys("workspace", paneKeys.Left); got != "^wh" {
		t.Errorf("helpKeys focus left = %q, want %q", got, "^wh")
	}
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"global": {"<C-w><C-h>": ":focus left<CR>"}})
	if got := m.helpKeys("workspace", paneKeys.Left); got != "^w^h ^wh" {
		t.Errorf("helpKeys with <C-w><C-h> mapped = %q, want %q", got, "^w^h ^wh")
	}
}

func TestHelpKeysShowsAllEffectiveKeys(t *testing.T) {
	m := projectsTestModel()
	got := m.helpKeys("project-tree", projectsKeys.Up)
	if got != "↑ k/↓ j" {
		t.Errorf("helpKeys prev pair = %q, want %q", got, "↑ k/↓ j")
	}

	m2 := withKeymap(projectsTestModel(), map[string]map[string]string{"global": {"<C-y>": ":toggle show-hidden<CR>"}})
	got2 := m2.helpKeys("project-tree", projectsKeys.ShowHidden)
	if !strings.Contains(got2, "^y") || !strings.Contains(got2, "z.") {
		t.Errorf("helpKeys remapped ShowHidden = %q, want both ^y and z.", got2)
	}
	// help table must show the same pairs from any component
	h := ansi.Strip(m.projectsHelpView())
	if !strings.Contains(h, "↑ k/↓ j") {
		t.Errorf("help table should show ↑ k/↓ j pair: %q", h)
	}
}

func TestHelpKeysResolveOnProjectsScreen(t *testing.T) {
	// The help is shown from viewHome and viewHistoryProjects too; key resolution
	// must always use the projects screen so pair partners are found.
	m := projectsTestModel()
	m = withView(m, viewHome)
	m.width, m.height = 160, 50
	h := ansi.Strip(m.projectsHelpView())
	for _, want := range []string{"h ← zc/l → zo", "]f/[f"} {
		if !strings.Contains(h, want) {
			t.Errorf("help from viewHome missing %q:\n%s", want, h)
		}
	}
}

func TestFlashNamesEffectiveKey(t *testing.T) {
	m := projectsTestModel()
	m = selectRow(m, "n1:p1")
	m = withKeymap(m, map[string]map[string]string{"project": {"<C-y>": ":toggle left-sidebar<CR>"}})
	m = typeKeys(m, " o")
	m, _ = upd(m, keyMsg("/"))
	if !strings.Contains(m.flash, "^y shows the tree") {
		t.Errorf("flash should name the effective key: %q", m.flash)
	}
}

func TestKeyTextFallsBackToTheCommandName(t *testing.T) {
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"project-tree": {"<Leader>o": "<Nop>"}})
	if got := m.keyText(projectsKeys.ToggleSidebar); got != "toggle left-sidebar" {
		t.Errorf("keyText with every key removed = %q, want the command name", got)
	}
}

func TestCommitBackKeys(t *testing.T) {
	if got := projectsTestModel().commitBackKeys(); got != "esc h" {
		t.Errorf("default: %q, want %q", got, "esc h")
	}
	m := withKeymap(projectsTestModel(), map[string]map[string]string{"changes": {"h": "<Nop>", "<Left>": "<Nop>", "zc": "<Nop>"}})
	if got := m.commitBackKeys(); got != "esc" {
		t.Errorf("with every collapse key removed: %q, want %q", got, "esc")
	}
	m = withKeymap(projectsTestModel(), map[string]map[string]string{"changes": {"<C-h>": ":fold close<CR>"}})
	if got := m.commitBackKeys(); got != "esc ^h" {
		t.Errorf("with collapse remapped: %q, want %q", got, "esc ^h")
	}
}

func TestLabelPairsNameRealCommands(t *testing.T) {
	names := map[string]bool{}
	for _, bs := range screenBindings {
		for _, b := range bs {
			names[b.name] = true
		}
	}
	for n := range labelPairs {
		if !names[n] {
			t.Errorf("labelPairs names %q, which no screen binds", n)
		}
	}
}

func TestTranscriptFooterPairsPromptKeys(t *testing.T) {
	m := resumeInto(streamModel(&recordingClient{}), "n1:s1", userEntry("u", "go"))
	if got := m.helpBinding(transcriptKeys.PromptNext).Help().Key; got != "}/{" {
		t.Errorf("prompt label = %q, want }/{", got)
	}
}

func TestFileFooterNamesHalfPage(t *testing.T) {
	for _, b := range (fileComp{}).footer(nil) {
		if b.name == fileViewKeys.HalfDown.name && b.Help().Desc != "half" {
			t.Errorf("half-page scroll is labeled %q; page names the full-page scroll", b.Help().Desc)
		}
	}
}

func TestHomeHintShowsItsKeyWhileTheTreeHasFocus(t *testing.T) {
	m := projectsTestModel()
	if m.focused != leftSidebar {
		t.Fatalf("the test needs the tree focused: %v", m.focused)
	}
	m.activeOnly = true
	for id, s := range m.sessions {
		s.Status = session.StatusIdle
		m.sessions[id] = s
	}
	m.reorder()
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "za shows all") {
		t.Errorf("the Home hint should name za while the tree has focus:\n%s", out)
	}
}

func TestKeyTextOnAnUnlistedSectionIsRecorded(t *testing.T) {
	m := projectsTestModel()
	m.keyTextOn("logs", projectsKeys.TogglePin)
	key := `logs: toggle pin (key text)`
	strictMu.Lock()
	recorded := unlisted[key]
	delete(unlisted, key)
	strictMu.Unlock()
	if !recorded {
		t.Error("a key lookup on a section that does not list the command must be recorded")
	}
}

func TestStartingNoticeNamesTheFocusedKey(t *testing.T) {
	m := withKeymap(waitingSession(), map[string]map[string]string{"session-dock": {"<C-g>": ":open live-screen<CR>"}, "transcript": {"<C-t>": "<Nop>"}})
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.screen() != "session-dock" {
		t.Fatalf("the test needs the dock focused: %q", m.screen())
	}
	if got := startingNotice(m); !strings.Contains(got, "^g") {
		t.Errorf("the notice should name the dock's key: %q", ansi.Strip(got))
	}
}

func TestRemovedCommandNamesItsReplacement(t *testing.T) {
	_, errs := buildKeymap(map[string]map[string]string{"project-tree": {"P": "project unpin", "p": ":project pin<CR>"}}, time.Second, "")
	want := []string{
		`keymap: project-tree "P": use ":toggle pin<CR>"`,
		`keymap: project-tree "p": project pin is now toggle pin`,
	}
	if strings.Join(errs, "\n") != strings.Join(want, "\n") {
		t.Errorf("errors:\n%s\nwant:\n%s", strings.Join(errs, "\n"), strings.Join(want, "\n"))
	}
}
