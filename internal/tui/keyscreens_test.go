package tui

import (
	"sort"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/logbuf"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// unusedExempt lists screen commands that no test reaches, with the reason.
var unusedExempt = map[string]bool{}

// unusedNames lists the commands a screen lists that no m.matches call checked
// on that screen, except the ones in exempt ("screen: name").
func unusedNames(exempt map[string]bool) []string {
	strictMu.Lock()
	defer strictMu.Unlock()
	var out []string
	for s, names := range screenNames {
		for n := range names {
			if id := s + ": " + n; !matchedNames[s][n] && !exempt[id] {
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}

func unlistedBindings() []string {
	strictMu.Lock()
	defer strictMu.Unlock()
	out := make([]string, 0, len(unlisted))
	for k := range unlisted {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestScreensListOnlyCommandsTheyRun(t *testing.T) {
	_, errs := buildKeymap(map[string]map[string]string{
		"transcript":   {"x": "session kill", "<C-y>": "session load-more"},
		"project-tree": {"<C-y>": "session load-more"},
		"session-dock": {"E": "transcript export"},
	}, time.Second, "")
	want := []string{
		`keymap: project-tree "<C-y>": unknown command "session load-more"`,
		`keymap: session-dock "E": unknown command "transcript export"`,
		`keymap: transcript "<C-y>": unknown command "session load-more"`,
		`keymap: transcript "x": unknown command "session kill"`,
	}
	if strings.Join(errs, "\n") != strings.Join(want, "\n") {
		t.Errorf("errors:\n%s\nwant:\n%s", strings.Join(errs, "\n"), strings.Join(want, "\n"))
	}

	m := testModel()
	m = withView(m, viewSession)
	m = withKeymap(m, map[string]map[string]string{"global": {"qm": "session load-more"}})
	if m, _ = upd(m, keyMsg("q")); len(m.keyBuf) != 0 {
		t.Error("session load-more does not run on session, so q must not wait for qm there")
	}
}

func TestSessionTranscriptCardKeys(t *testing.T) {
	m := testModel()
	m = withView(m, viewSession)
	m = withTr(m, func(t *transcriptComp) {
		t.transcript.chunks = []transcript.Chunk{{ID: "a", Kind: transcript.ChunkSystem, Text: "note", Detail: "more"}}
	})
	if m, _ = upd(m, keyMsg("enter")); trOf(m).historyView != histDetail {
		t.Error("enter should open the card detail")
	}
}

func TestFileViewKeysInSessionAndDetail(t *testing.T) {
	for _, detail := range []bool{false, true} {
		m := testModel()
		m = withView(m, viewSession)
		m = withFocus(m, mainPane)
		if detail {
			m = withTr(m, func(t *transcriptComp) {
				t.transcript.chunks = []transcript.Chunk{{ID: "a", Kind: transcript.ChunkSystem, Text: "note", Detail: "more"}}
			})
			m = withTr(m, func(t *transcriptComp) { t.historyView = histDetail })
			m, _ = onTr(m, func(v tview) tea.Cmd { v.enterDetail(); return nil })
		}
		m = withFile(m, fileComp{ws: "n1:w1", path: "a.go", diff: true, lines: strings.Split(strings.Repeat("+x\n", 100), "\n")})
		m, _ = upd(m, keyMsg("j"))
		m, _ = upd(m, keyMsg("j"))
		m, _ = upd(m, keyMsg("k"))
		m = typeKeys(m, "yow")
		if fileOf(m).scroll != 1 || !fileOf(m).wrap {
			t.Errorf("detail=%v: j j k yow should scroll to 1 and wrap: scroll=%d wrap=%v", detail, fileOf(m).scroll, fileOf(m).wrap)
		}
		m = typeKeys(m, "]f[f")
		if !m.hasOpenFile() {
			t.Errorf("detail=%v: ]f/[f with no changes list keep the diff open", detail)
		}
	}
}

func TestDetailChangesDiffModeKey(t *testing.T) {
	m := changesFocused()
	m = withView(m, viewSession)
	m = withTr(m, func(t *transcriptComp) { t.historyView = histDetail })
	if m, _ = upd(m, keyMsg("t")); !strings.Contains(m.flash, "no target branch") {
		t.Errorf("t in the detail view's Changes tab should ask for a target: flash=%q", m.flash)
	}
}

func TestDetailDockFollowsDockKeymap(t *testing.T) {
	m := withKeymap(promptModel(&session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{
		{Question: "Many", Options: []string{"A", "B"}, MultiSelect: true},
		{Question: "One", Options: []string{"C", "D"}},
	}}), map[string]map[string]string{"session-dock": {"<C-y>": "option select"}})
	m = withTr(m, func(t *transcriptComp) { t.historyView = histDetail })
	m, _ = upd(m, ctrlKey('y'))
	if !m.dock.toggles[0][0] {
		t.Fatal("<C-y> mapped in the session-dock section should toggle the option in the dock under the detail")
	}
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyRight}, {Code: tea.KeyLeft}, {Code: tea.KeyRight}, {Code: tea.KeyEnter}} {
		m, _ = upd(m, k)
	}
	if m.dock.tab != 2 {
		t.Errorf("right, left, right, enter should commit question 2: tab=%d", m.dock.tab)
	}
}

func TestDetailRedactionListRemove(t *testing.T) {
	m := newRedactModel()
	m = withTr(m, func(t *transcriptComp) {
		t.historyView = histDetail
		t.redact.literals = []string{"aaa", "bbb"}
	})
	for _, k := range []tea.KeyPressMsg{{Code: 'D', Text: "D"}, {Code: 'j', Text: "j"}, {Code: 'u', Text: "u"}} {
		res, _ := m.baseKey(k)
		m = res.(model)
	}
	if len(trOf(m).redact.literals) != 1 || trOf(m).redact.literals[0] != "aaa" {
		t.Errorf("u in the detail view's redaction list should remove bbb: %v", trOf(m).redact.literals)
	}
}

func TestHistorySessionsMoreAndBack(t *testing.T) {
	m := testModel()
	m = withHistorySessions(m, session.HistoryProject{}, session.HistorySessionPage{HasMore: true})
	if m, _ = upd(m, keyMsg("m")); !historyOf(m).loading {
		t.Fatal("m should load more sessions")
	}
	if m, _ = upd(m, keyMsg("esc")); viewOf(m) != viewHistoryProjects {
		t.Errorf("esc should return to the history projects: view=%v", viewOf(m))
	}
}

func TestLogsKeys(t *testing.T) {
	b := logbuf.New(1000)
	fillLogs(b, 100)
	m := newModel(logsStubClient{}, false, b)
	m.width, m.height = 120, 30
	m = withView(m, viewLogs)
	for _, k := range []tea.KeyPressMsg{keyMsg("k"), keyMsg("j"), ctrlKey('u'), ctrlKey('d'), keyMsg("g"), keyMsg("g")} {
		m, _ = upd(m, k)
	}
	if l := logsOf(m); l.scroll != 0 || l.follow {
		t.Fatalf("k j ^u ^d gg should end at the top: scroll=%d follow=%v", l.scroll, l.follow)
	}
	if m, _ = upd(m, keyMsg("G")); !logsOf(m).follow {
		t.Error("G should follow the newest line")
	}
	if m = typeKeys(m, "g?"); !m.showHelp {
		t.Error("g? should open the help")
	}
	m, _ = upd(m, keyMsg("?"))
	hidden, filesHidden := m.left.hidden, m.right.hidden
	m = typeKeys(m, " o e")
	if m.left.hidden == hidden || m.right.hidden == filesHidden {
		t.Error("␣o and ␣e should toggle the sidebars")
	}
	m.left.hidden = false
	for k, want := range map[string]shownView{"gT": viewHistoryProjects, "gt": viewHome} {
		if mm := typeKeys(m, k); viewOf(mm) != want {
			t.Errorf("%s on logs: view=%v, want %v", k, viewOf(mm), want)
		}
	}
	if mm, _ := upd(m, keyMsg("esc")); viewOf(mm) != viewHome {
		t.Errorf("esc on logs: view=%v, want %v", viewOf(mm), viewHome)
	}
	if mm := pressKeys(m, cw('h')...); viewOf(mm) != viewLogs || !mm.treeFocused() {
		t.Errorf("<C-w>h on logs: view=%v focus=%v, want the tree over Logs", viewOf(mm), mm.focused)
	}
	hist := m
	hist = withView(hist, viewHistoryProjects)
	if hist = typeKeys(hist, "gt"); viewOf(hist) != viewLogs {
		t.Errorf("gt on history: view=%v, want %v", viewOf(hist), viewLogs)
	}
}

func TestHistoryTranscriptSidebarToggles(t *testing.T) {
	m := testModel()
	m.width = 200
	m = withView(m, viewHistoryTranscript)
	m = typeKeys(m, " o e")
	if m.left.hidden || m.right.hidden {
		t.Errorf("␣o and ␣e should show both sidebars: left hidden=%v right hidden=%v", m.left.hidden, m.right.hidden)
	}
}

func TestKeymapSectionsByComponent(t *testing.T) {
	t.Run("each component names its section", func(t *testing.T) {
		detail := pressKeys(waitingSession(), keyMsg("enter"))
		cases := []struct {
			name string
			m    model
			want string
		}{
			{"tree", wideWorkspace(), "project-tree"},
			{"workspace pane", withFocus(wideWorkspace(), mainPane), "workspace"},
			{"file over a workspace", withFocus(openedFile(wideWorkspace()), mainPane), "file"},
			{"Files tab", filesFocused(), "file-tree"},
			{"Changes tab", changesFocused(), "changes"},
			{"Home", homeTestModel(), "home"},
			{"History", withHistoryProjects(homeTestModel(), historyProjects()...), "history"},
			{"Logs", logsModel(120, false), "logs"},
			{"live transcript", waitingSession(), "transcript"},
			{"card detail", detail, "transcript"},
			{"history transcript", historyTranscript(false), "transcript"},
			{"session dock", withFocus(waitingSession(), sessionDock), "session-dock"},
			{"create picker", createTestModel(t), "project-tree"},
			{"retarget picker", withPopup(projectsTestModel(), retargetPicker{pick: newBranchPicker()}), ""},
			{"spawn flow", spawnOverHome(), ""},
			{"live screen", liveScreenModel(), ""},
		}
		if trOf(detail).historyView != histDetail {
			t.Fatal("setup: enter should open the card detail")
		}
		for _, c := range cases {
			if got := c.m.screen(); got != c.want {
				t.Errorf("%s: section %q, want %q", c.name, got, c.want)
			}
		}
	})

	t.Run("a file-tree mapping applies only in the file tree", func(t *testing.T) {
		raw := map[string]map[string]string{"file-tree": {"<C-y>": "back"}}
		if m, _ := upd(withKeymap(filesFocused(), raw), ctrlKey('y')); m.focused != mainPane {
			t.Errorf("<C-y> in the Files tab should run back: focus = %v", m.focused)
		}
		filtered := wideWorkspace()
		filtered.left.tree.setFilter("repo")
		cases := []struct {
			name string
			m    model
			want container
		}{
			{"Changes tab", changesFocused(), rightSidebar},
			{"tree", filtered, leftSidebar},
			{"workspace pane", withFocus(wideWorkspace(), mainPane), mainPane},
			{"file over a workspace", withFocus(openedFile(wideWorkspace()), mainPane), mainPane},
		}
		for _, c := range cases {
			m, _ := upd(withKeymap(c.m, raw), ctrlKey('y'))
			if m.focused != c.want || (c.name == "file over a workspace" && !m.hasOpenFile()) ||
				(c.name == "tree" && m.left.tree.filter != "repo") {
				t.Errorf("%s: <C-y> mapped in file-tree acted: focus = %v file open = %v filter = %q",
					c.name, m.focused, m.hasOpenFile(), m.left.tree.filter)
			}
		}
	})

	t.Run("transcript covers the card detail", func(t *testing.T) {
		raw := map[string]map[string]string{"transcript": {"<C-y>": "back"}}
		for name, m := range map[string]model{"live": waitingSession(), "history": historyTranscript(false)} {
			m = pressKeys(withKeymap(m, raw), keyMsg("enter"))
			if trOf(m).historyView != histDetail {
				t.Fatalf("%s: enter should open the card detail", name)
			}
			if m, _ = upd(m, ctrlKey('y')); trOf(m).historyView != histTranscript {
				t.Errorf("%s: <C-y> mapped in transcript should leave the card detail", name)
			}
		}
	})

	t.Run("the old names give the hint", func(t *testing.T) {
		km, errs := buildKeymap(map[string]map[string]string{
			"projects": {"a": "quit"},
			"session":  {"b": "back"},
			"detail":   {"c": "back"},
		}, time.Second, "")
		want := []string{
			`keymap: unknown screen "detail" (now transcript)`,
			`keymap: unknown screen "projects" (now project-tree, workspace, file, file-tree, changes)`,
			`keymap: unknown screen "session" (now transcript, session-dock)`,
		}
		if strings.Join(errs, "\n") != strings.Join(want, "\n") {
			t.Errorf("errors:\n%s\nwant:\n%s", strings.Join(errs, "\n"), strings.Join(want, "\n"))
		}
		for _, old := range []string{"projects", "session", "detail"} {
			if km.screens[old] != nil {
				t.Errorf("the old section %s was installed", old)
			}
		}
		for s := range km.screens {
			for _, k := range []string{"a", "b", "c"} {
				if _, taken := km.screens[s].takenBy[k]; taken {
					t.Errorf("an old section's mapping of %s reached %s", k, s)
				}
			}
		}
	})
}
