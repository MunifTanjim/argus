package tui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func cmdLineOf(m model) (cmdLinePopup, bool) {
	p, ok := m.popups.front().(cmdLinePopup)
	return p, ok
}

func cmdLineOpen(m model) bool {
	_, ok := cmdLineOf(m)
	return ok
}

func cmdLine(m model) cmdLinePopup {
	p, _ := cmdLineOf(m)
	return p
}

// pressDefault presses the default key tok (Vim notation) through Update, and
// ends a sequence that waits for the timeout. cmd is the last key's command.
func pressDefault(t *testing.T, m model, tok string) (model, tea.Cmd) {
	t.Helper()
	seq, err := parseKeySeq(tok)
	if err != nil {
		t.Fatal(err)
	}
	var cmd tea.Cmd
	for _, step := range replaceLeader(seq, keyStep{"space"}) {
		m, cmd = upd(m, tea.KeyPressMsg{Code: tea.KeyExtended, Text: step[0]})
	}
	if len(m.keyBuf) > 0 {
		m, cmd = upd(m, keyTimeoutMsg{m.keyGen})
	}
	return m, cmd
}

// frameSnapshot is what a user can see of m: the shown view, the focus, and
// the drawn frame.
func frameSnapshot(m model) string {
	return fmt.Sprintf("%v|%v|%s", viewOf(m), m.focused, ansi.Strip(m.View().Content))
}

// assertRunsLikeItsKey checks that run(state, name) gives the same frame as
// the command's first default key, and a command exactly when the key gives
// one. Commands that share a key pass when one of them matches. Each state
// needs a command that changes its frame, so no-ops alone cannot pass.
func assertRunsLikeItsKey(t *testing.T, run func(m model, name string) (model, tea.Cmd)) {
	t.Helper()
	for _, st := range sectionStates() {
		if len(sectionLists[st.section].focus) == 0 {
			continue
		}
		byTok := map[string][]string{}
		var toks []string
		for _, b := range st.build().commandSet() {
			if f := strings.Fields(b.defaults); len(f) > 0 {
				if byTok[f[0]] == nil {
					toks = append(toks, f[0])
				}
				byTok[f[0]] = append(byTok[f[0]], b.name)
			}
		}
		base, changed := frameSnapshot(st.build()), false
		for _, tok := range toks {
			km, kcmd := pressDefault(t, st.build(), tok)
			want := frameSnapshot(km)
			changed = changed || want != base
			var differ []string
			for _, name := range byTok[tok] {
				rm, rcmd := run(st.build(), name)
				if frameSnapshot(rm) != want || (rcmd == nil) != (kcmd == nil) {
					differ = append(differ, name)
				}
			}
			if len(differ) == len(byTok[tok]) {
				t.Errorf("%s: %s differs from its key %s", st.name, strings.Join(differ, ", "), tok)
			}
		}
		if !changed {
			t.Errorf("%s: no command changes the frame", st.name)
		}
	}
}

func TestCommandByNameMatchesItsKey(t *testing.T) {
	assertRunsLikeItsKey(t, func(m model, name string) (model, tea.Cmd) {
		return upd(m, cmdMsg(name))
	})
}

func TestCommandSetIsTheFocusedComponents(t *testing.T) {
	has := func(m model, name string) bool {
		for _, b := range m.commandSet() {
			if b.name == name {
				return true
			}
		}
		return false
	}
	tr := waitingSession()
	if !has(tr, transcriptKeys.CardNext.name) {
		t.Error("the transcript's set lacks next card")
	}
	if has(tr, projectsKeys.ShowHidden.name) {
		t.Error("the transcript's set has a tree command")
	}
	if has(tr, listKeys.Quit.name) {
		t.Error("the transcript does not offer quit, so its set must not have it")
	}
	if !has(homeTestModel(), listKeys.Quit.name) {
		t.Error("Home offers quit")
	}
	for _, b := range wideWorkspace().commandSet() {
		if textInputOnly(b.name) {
			t.Errorf("the set has the text input command %s", b.name)
		}
	}
	if len(withFocus(waitingSession(), sessionDock).commandSet()) != 0 {
		t.Error("the dock takes raw keys and has no command set")
	}
}

func TestCommandWithRemovedKeysRunsByName(t *testing.T) {
	m := withKeymap(wideWorkspace(), map[string]map[string]string{
		"project-tree": {"z.": ""},
	})
	before := m.left.tree.showHidden
	m, _ = upd(m, cmdMsg(projectsKeys.ShowHidden.name))
	if m.left.tree.showHidden == before {
		t.Error("toggle show-hidden did not run by name")
	}
}

var (
	bsKey   = tea.KeyPressMsg{Code: tea.KeyBackspace}
	upKey   = tea.KeyPressMsg{Code: tea.KeyUp}
	downKey = tea.KeyPressMsg{Code: tea.KeyDown}
	sTabKey = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
)

func matchNames(ms []cmdMatch) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.name
	}
	return out
}

func TestColonOpensAndClosesTheCommandLine(t *testing.T) {
	m := typeKeys(wideWorkspace(), ":")
	if !cmdLineOpen(m) {
		t.Fatal(": did not open the command line")
	}
	if m.focused != leftSidebar {
		t.Errorf("focus moved to %v", m.focused)
	}
	if m = pressKeys(m, keyMsg("esc")); cmdLineOpen(m) {
		t.Error("esc did not close it")
	}
	m = typeKeys(m, ":x")
	if m = pressKeys(m, bsKey); !cmdLineOpen(m) {
		t.Error("backspace over text closed it")
	}
	if m = pressKeys(m, bsKey); cmdLineOpen(m) {
		t.Error("backspace on an empty input did not close it")
	}
}

func TestColonGoesElsewhere(t *testing.T) {
	m := typeKeys(wideWorkspace(), "/:")
	if cmdLineOpen(m) || !strings.Contains(m.left.tree.input.Value(), ":") {
		t.Error("the tree filter must get the colon")
	}
	m = wideWorkspace()
	m.showHelp = true
	if m = typeKeys(m, ":"); cmdLineOpen(m) || m.showHelp {
		t.Error("with help open, : must only close the help")
	}
	m = withKeymap(wideWorkspace(), map[string]map[string]string{"project-tree": {"g:": "toggle show-gone"}})
	before := m.left.tree.showGone
	if m = typeKeys(m, "g:"); cmdLineOpen(m) || m.left.tree.showGone == before {
		t.Error("g: must run its mapping")
	}
	m = withFocus(waitingSession(), sessionDock)
	if m = typeKeys(m, ":"); cmdLineOpen(m) {
		t.Error("the dock must get the colon")
	}
}

func TestColonAfterAMismatchedKeyOpens(t *testing.T) {
	if m := typeKeys(wideWorkspace(), "z:"); !cmdLineOpen(m) {
		t.Error(": re-fed after a mismatch must open the command line")
	}
}

func TestMatchOrder(t *testing.T) {
	set := []binding{nb("toggle right-sidebar", "", ""), nb("goto top", "", ""), nb("toggle left-sidebar", "", ""), nb("stop-toggle", "", "")}
	if got := matchNames(matchCommands(set, " TOG ")); len(got) != 3 || !strings.HasPrefix(got[0], "toggle ") || !strings.HasPrefix(got[1], "toggle ") || got[2] != "stop-toggle" {
		t.Errorf("tog matches %v, want the toggle commands first, then stop-toggle", got)
	}
	if got := matchNames(matchCommands(set, "tls")); len(got) == 0 || got[0] != "toggle left-sidebar" {
		t.Errorf("tls matches %v, want toggle left-sidebar first", got)
	}
	if got := strings.Join(matchNames(matchCommands(set, "")), ","); got != "goto top,stop-toggle,toggle left-sidebar,toggle right-sidebar" {
		t.Errorf("an empty input matches %s, want every command in name order", got)
	}
}

func TestMatchesHighlightTheMatchedCharacters(t *testing.T) {
	name, hits := "toggle left-sidebar", []int{0, 7, 12}
	for _, base := range []lipgloss.Style{lipgloss.NewStyle(), StyleSecondary.Reverse(true)} {
		got := highlightName(name, hits, base)
		if ansi.Strip(got) != name {
			t.Fatalf("highlighted text = %q", ansi.Strip(got))
		}
		for _, i := range hits {
			if hit := cmdMatchStyle(base).Render(name[i : i+1]); !strings.Contains(got, hit) {
				t.Errorf("base %v: %q is not highlighted in %q", base, name[i:i+1], got)
			}
		}
	}
}

func TestCtrlNAndCtrlPCycleLikeTab(t *testing.T) {
	m := typeKeys(wideWorkspace(), ":toggle-")
	ms := matchNames(cmdLine(m).matches)
	if m = pressKeys(m, ctrlKey('n')); cmdLine(m).input.Value() != ms[0] {
		t.Errorf("ctrl+n put %q, want %q", cmdLine(m).input.Value(), ms[0])
	}
	if m = pressKeys(m, ctrlKey('p')); cmdLine(m).input.Value() != ms[len(ms)-1] {
		t.Errorf("ctrl+p from the first put %q, want %q", cmdLine(m).input.Value(), ms[len(ms)-1])
	}
}

func TestTabCyclesTheMatches(t *testing.T) {
	m := typeKeys(wideWorkspace(), ":toggle-")
	ms := matchNames(cmdLine(m).matches)
	if len(ms) < 2 {
		t.Fatalf("want at least 2 matches, got %v", ms)
	}
	if cmdLine(m).sel != -1 {
		t.Error("no row is selected before the first tab")
	}
	m = pressKeys(m, keyMsg("tab"))
	if cmdLine(m).input.Value() != ms[0] {
		t.Errorf("tab put %q, want %q", cmdLine(m).input.Value(), ms[0])
	}
	m = pressKeys(m, sTabKey)
	if cmdLine(m).input.Value() != ms[len(ms)-1] {
		t.Errorf("shift+tab from the first wraps to the last, got %q", cmdLine(m).input.Value())
	}
	if strings.Join(matchNames(cmdLine(m).matches), ",") != strings.Join(ms, ",") {
		t.Error("cycling must not change the matches")
	}
	if m = typeKeys(m, "x"); cmdLine(m).sel != -1 {
		t.Error("typing must clear the selection")
	}
}

func TestRunFromTheCommandLineMatchesTheKey(t *testing.T) {
	assertRunsLikeItsKey(t, func(m model, name string) (model, tea.Cmd) {
		return upd(typeKeys(m, ":"+name), keyMsg("enter"))
	})
}

func TestAliasesAndUnknownNames(t *testing.T) {
	if m := pressKeys(typeKeys(homeTestModel(), ":q"), keyMsg("enter")); m.flash != "" || len(m.cmdHistory) != 1 {
		t.Errorf(":q on Home must run quit: flash %q, history %v", m.flash, m.cmdHistory)
	}
	m := pressKeys(typeKeys(waitingSession(), ":q"), keyMsg("enter"))
	if m.flash != "unknown command: q" {
		t.Errorf("flash %q", m.flash)
	}
	m = pressKeys(typeKeys(wideWorkspace(), ": nope "), keyMsg("enter"))
	if m.flash != "unknown command: nope" || cmdLineOpen(m) {
		t.Errorf("flash %q, open %v", m.flash, cmdLineOpen(m))
	}
	if len(m.cmdHistory) != 0 {
		t.Error("an unknown name must not enter the history")
	}
	m = pressKeys(typeKeys(wideWorkspace(), ":"), keyMsg("enter"))
	if m.flash != "" || cmdLineOpen(m) {
		t.Error("an empty line closes without a message")
	}
}

func TestUnknownFlashShowsInEveryFooter(t *testing.T) {
	for _, st := range sectionStates() {
		if len(sectionLists[st.section].focus) == 0 {
			continue
		}
		m := pressKeys(typeKeys(st.build(), ":nope"), keyMsg("enter"))
		if !strings.Contains(ansi.Strip(m.currentFooter()), "unknown command: nope") {
			t.Errorf("%s: the footer does not show the flash", st.name)
		}
	}
}

func TestHistory(t *testing.T) {
	m := wideWorkspace()
	run := func(s string) { m = pressKeys(typeKeys(m, ":"+s), keyMsg("enter")) }
	hidden, gone := projectsKeys.ShowHidden.name, projectsKeys.ShowGone.name
	run(hidden)
	run(gone)
	run(gone)
	if got := strings.Join(m.cmdHistory, ","); got != hidden+","+gone {
		t.Fatalf("history %s: the newest entry must not repeat", got)
	}
	m = pressKeys(typeKeys(m, ":"), upKey)
	if cmdLine(m).input.Value() != gone {
		t.Errorf("up shows %q", cmdLine(m).input.Value())
	}
	m = pressKeys(m, upKey, downKey, downKey)
	if cmdLine(m).input.Value() != "" {
		t.Errorf("down past the newest shows the typed text, got %q", cmdLine(m).input.Value())
	}
	m = pressKeys(m, keyMsg("esc"))
	m = pressKeys(typeKeys(m, ":toggle show-h"), upKey)
	if cmdLine(m).input.Value() != hidden {
		t.Errorf("up with typed text shows %q", cmdLine(m).input.Value())
	}
}

func repeatKey(k tea.KeyPressMsg, n int) []tea.KeyPressMsg {
	out := make([]tea.KeyPressMsg, n)
	for i := range out {
		out[i] = k
	}
	return out
}

func TestCommandLineFrames(t *testing.T) {
	treeHidden := func() model {
		m := withFocus(wideWorkspace(), mainPane)
		m.left.hidden = true
		m, _ = m.syncSidebar()
		return m
	}
	cases := []struct {
		name string
		m    model
	}{
		{"cmdline-list", typeKeys(wideWorkspace(), ":toggle")},
		{"cmdline-list-scroll", pressKeys(typeKeys(wideWorkspace(), ":"), repeatKey(keyMsg("tab"), 12)...)},
		{"cmdline-no-match", typeKeys(wideWorkspace(), ":zzz")},
		{"cmdline-unknown", pressKeys(typeKeys(wideWorkspace(), ":zzz"), keyMsg("enter"))},
		{"cmdline-tree-hidden", typeKeys(treeHidden(), ":scroll")},
		{"cmdline-logs-full", typeKeys(logsModel(120, true), ":")},
	}
	for _, c := range cases {
		assertGolden(t, c.name, c.m)
	}
}

func TestCommandListAlignsWithTheTypedText(t *testing.T) {
	treeHidden := withFocus(wideWorkspace(), mainPane)
	treeHidden.left.hidden = true
	treeHidden, _ = treeHidden.syncSidebar()
	cases := []struct {
		name string
		m    model
	}{
		{"tree", wideWorkspace()},
		{"tree hidden", treeHidden},
		{"logs full", logsModel(120, true)},
	}
	for _, c := range cases {
		lines := strings.Split(ansi.Strip(typeKeys(c.m, ":toggle left").View().Content), "\n")
		col := func(line, sub string) int { return ansi.StringWidth(line[:strings.Index(line, sub)]) }
		typed := col(lines[len(lines)-1], ":toggle left") + 1
		listed := col(lines[len(lines)-3], "toggle left-sidebar")
		if listed != typed {
			t.Errorf("%s: the list starts at column %d, the typed text at %d", c.name, listed, typed)
		}
	}
}

func TestCommandDoesNotAnswerAPromptThatAppearedMeanwhile(t *testing.T) {
	m := typeKeys(historyTranscript(false), ":"+transcriptKeys.Top.name)
	m = withTr(m, func(tr *transcriptComp) { tr.redact.pendingSave = true })
	m = pressKeys(m, keyMsg("enter"))
	if !trOf(m).redact.pendingSave {
		t.Error("the command answered the save prompt the user never saw")
	}
}

func TestCommandLineFollowsAResize(t *testing.T) {
	m := typeKeys(wideWorkspace(), ":")
	m, _ = upd(m, tea.WindowSizeMsg{Width: 90, Height: m.height})
	m = typeKeys(m, "abc")
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "  :abc") {
		t.Errorf("the command line row moved after a resize: %q", last)
	}
}

func TestLeaderCannotBeColon(t *testing.T) {
	km, errs := buildKeymap(nil, time.Second, ":")
	if len(errs) != 1 || errs[0] != `keymap: tui.leader-key ":": ":" opens the command line` {
		t.Errorf("errors %v", errs)
	}
	if ids := strings.Join(km.bare.keys(projectsKeys.ToggleSidebar), " "); !strings.Contains(ids, "space") {
		t.Errorf("the leader must fall back to space, toggle left-sidebar keys %q", ids)
	}
}

func TestLogsKeyClearsTheFlash(t *testing.T) {
	m := logsModel(120, false)
	m.flash = "unknown command: nope"
	if m = typeKeys(m, "j"); m.flash != "" {
		t.Errorf("flash %q stays on Logs", m.flash)
	}
}

func TestListWidthStaysWhileScrolling(t *testing.T) {
	m := typeKeys(wideWorkspace(), ":")
	w := func(m model) int {
		return lipgloss.Width(strings.Split(cmdLine(m).listBox(&ctx{m: &m}, m.layout().w), "\n")[0])
	}
	first := w(pressKeys(m, keyMsg("tab")))
	for i := 2; i <= len(cmdLine(m).matches); i++ {
		if got := w(pressKeys(m, repeatKey(keyMsg("tab"), i)...)); got != first {
			t.Fatalf("the list is %d wide at match %d, %d at the first", got, i, first)
		}
	}
}

func TestShortTerminalShowsTheWholeList(t *testing.T) {
	m := wideWorkspace()
	m, _ = upd(m, tea.WindowSizeMsg{Width: m.width, Height: 9})
	m = pressKeys(typeKeys(m, ":"), repeatKey(keyMsg("tab"), 12)...)
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "╭") || !strings.Contains(out, "│ "+cmdLine(m).input.Value()) {
		t.Errorf("the list lost its top or its selected row:\n%s", out)
	}
}

func TestTabWrapsFromTheLastMatch(t *testing.T) {
	m := typeKeys(wideWorkspace(), ":toggle-")
	ms := matchNames(cmdLine(m).matches)
	m = pressKeys(m, repeatKey(keyMsg("tab"), len(ms)+1)...)
	if cmdLine(m).input.Value() != ms[0] {
		t.Errorf("tab after the last match shows %q, want %q", cmdLine(m).input.Value(), ms[0])
	}
}

type noSectionComp struct{ homeComp }

func (noSectionComp) section() string { return "" }

func TestColonPassesOverAComponentWithNoCommands(t *testing.T) {
	m := withFocus(homeTestModel(), mainPane)
	m.main = m.main.push(noSectionComp{homeOf(m)})
	if m.focusedComp().raw(&ctx{m: &m}) {
		t.Fatal("the stub must take resolved keys")
	}
	if m = typeKeys(m, ":"); cmdLineOpen(m) {
		t.Error(": opened an empty command line")
	}
}

func TestCtrlCQuitsFromTheCommandLine(t *testing.T) {
	_, cmd := upd(typeKeys(wideWorkspace(), ":ab"), tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !quits(cmd) {
		t.Error("ctrl+c must quit while the command line is open")
	}
}

func TestLiveScreenGetsTheColon(t *testing.T) {
	if m := typeKeys(liveScreenModel(), ":"); cmdLineOpen(m) {
		t.Error("the live screen must get the colon")
	}
}

func TestSelectedRowIsReversed(t *testing.T) {
	m := pressKeys(typeKeys(wideWorkspace(), ":toggle-"), keyMsg("tab"))
	sgr7 := regexp.MustCompile(`\x1b\[(?:[0-9]+;)*7(?:;[0-9]+)*m`)
	for _, line := range strings.Split(m.View().Content, "\n") {
		if strings.Contains(ansi.Strip(line), cmdLine(m).input.Value()+" ") && sgr7.MatchString(line) {
			return
		}
	}
	t.Error("the selected row is not in reverse video")
}

// Labels drawn under the open command line keep the keys of the section it
// opened on.
func TestCommandLineKeepsTheScreenUnderIt(t *testing.T) {
	m := wideWorkspace()
	want := m.screen()
	if got := typeKeys(m, ":").screen(); got != want {
		t.Errorf("screen with the command line open = %q, want %q", got, want)
	}
}

func TestCommandLineScrollsInANarrowTerminal(t *testing.T) {
	m, _ := upd(wideWorkspace(), tea.WindowSizeMsg{Width: 30, Height: 20})
	m = typeKeys(m, ":"+strings.Repeat("a", 30)+"xyz")
	lines := frameLines(m)
	if last := lines[len(lines)-1]; !strings.Contains(last, "xyz") {
		t.Errorf("the end of the typed text is cut off: %q", last)
	}
}

func TestPasteIntoTheCommandLine(t *testing.T) {
	m, _ := upd(typeKeys(wideWorkspace(), ":"), tea.PasteMsg{Content: "toggle left"})
	if got := cmdLine(m).input.Value(); got != "toggle left" || len(cmdLine(m).matches) == 0 || cmdLine(m).matches[0].name != "toggle left-sidebar" {
		t.Errorf("input %q matches %v after a paste", got, matchNames(cmdLine(m).matches))
	}
}

func TestTheListHighlightsMatches(t *testing.T) {
	m := typeKeys(wideWorkspace(), ":tls")
	box := func(m model) string { return cmdLine(m).listBox(&ctx{m: &m}, m.width) }
	if hit := cmdMatchStyle(lipgloss.NewStyle()).Render("l"); !strings.Contains(box(m), hit) {
		t.Errorf("a normal row does not highlight its matches:\n%q", box(m))
	}
	m = pressKeys(m, keyMsg("tab"))
	if hit := cmdMatchStyle(StyleSecondary.Reverse(true)).Render("l"); !strings.Contains(box(m), hit) {
		t.Errorf("the selected row does not highlight its matches:\n%q", box(m))
	}
}

func TestCommandLineIgnoresExtraSpaces(t *testing.T) {
	m := pressKeys(typeKeys(homeTestModel(), ":  goto   bottom "), keyMsg("enter"))
	if m.flash != "" {
		t.Errorf("extra spaces must not make the name unknown: flash %q", m.flash)
	}
}

func TestCommandSetListsTheHalfThatApplies(t *testing.T) {
	names := func(m model) []string {
		var out []string
		for _, b := range m.commandSet() {
			out = append(out, b.name)
		}
		return out
	}
	m, _ := projectOnCursor(false, true)
	got := names(m)
	for _, want := range []string{"project pin", "project unhide"} {
		if !slices.Contains(got, want) {
			t.Errorf("command set %v must list %q", got, want)
		}
	}
	for _, not := range []string{"project unpin", "project hide"} {
		if slices.Contains(got, not) {
			t.Errorf("command set %v must not list %q", got, not)
		}
	}
	m = pressKeys(typeKeys(m, ":project unpin"), keyMsg("enter"))
	if m.flash != "unknown command: project unpin" {
		t.Errorf("flash %q", m.flash)
	}
}
