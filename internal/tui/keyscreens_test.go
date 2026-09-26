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
		"session":    {"x": "session kill", "E": "transcript export"},
		"projects":   {"<C-y>": "session load-more"},
		"transcript": {"<C-y>": "focus prompt"},
	}, time.Second, "")
	want := []string{
		`keymap: projects "<C-y>": unknown command "session load-more"`,
		`keymap: session "E": unknown command "transcript export"`,
		`keymap: session "x": unknown command "session kill"`,
		`keymap: transcript "<C-y>": unknown command "focus prompt"`,
	}
	if strings.Join(errs, "\n") != strings.Join(want, "\n") {
		t.Errorf("errors:\n%s\nwant:\n%s", strings.Join(errs, "\n"), strings.Join(want, "\n"))
	}

	m := testModel()
	m.mode = modeSession
	m = withKeymap(m, map[string]map[string]string{"global": {"qm": "session load-more"}})
	if m, _ = upd(m, keyMsg("q")); len(m.keyBuf) != 0 {
		t.Error("session load-more does not run on session, so q must not wait for qm there")
	}
}

func TestSessionTranscriptCardKeys(t *testing.T) {
	m := testModel()
	m.mode = modeSession
	m.transcript.chunks = []transcript.Chunk{{ID: "a", Kind: transcript.ChunkSystem, Text: "note", Detail: "more"}}
	if m, _ = upd(m, keyMsg("enter")); m.historyView != histDetail {
		t.Error("enter should open the card detail")
	}
}

func TestFileViewKeysInSessionAndDetail(t *testing.T) {
	for _, detail := range []bool{false, true} {
		m := testModel()
		m.mode, m.focus = modeSession, focusHistory
		if detail {
			m.transcript.chunks = []transcript.Chunk{{ID: "a", Kind: transcript.ChunkSystem, Text: "note", Detail: "more"}}
			m.historyView = histDetail
			m.enterDetail()
		}
		m.projects.fileView = fileViewState{ws: "n1:w1", path: "a.go", diff: true, lines: strings.Split(strings.Repeat("+x\n", 100), "\n")}
		m, _ = upd(m, keyMsg("j"))
		m, _ = upd(m, keyMsg("j"))
		m, _ = upd(m, keyMsg("k"))
		m = typeKeys(m, "yow")
		if m.projects.fileView.scroll != 1 || !m.projects.fileView.wrap {
			t.Errorf("detail=%v: j j k yow should scroll to 1 and wrap: scroll=%d wrap=%v", detail, m.projects.fileView.scroll, m.projects.fileView.wrap)
		}
		m = typeKeys(m, "]f[f")
		if !m.projects.fileView.open() {
			t.Errorf("detail=%v: ]f/[f with no changes list keep the diff open", detail)
		}
	}
}

func TestDetailChangesDiffModeKey(t *testing.T) {
	m := changesFocused()
	m.mode, m.historyView = modeSession, histDetail
	if m, _ = upd(m, keyMsg("t")); !strings.Contains(m.flash, "no target branch") {
		t.Errorf("t in the detail view's Changes tab should ask for a target: flash=%q", m.flash)
	}
}

func TestDetailDockFollowsDetailKeymap(t *testing.T) {
	m := withKeymap(promptModel(&session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{
		{Question: "Many", Options: []string{"A", "B"}, MultiSelect: true},
		{Question: "One", Options: []string{"C", "D"}},
	}}), map[string]map[string]string{"detail": {"<C-y>": "option select"}})
	m.historyView = histDetail
	m, _ = upd(m, ctrlKey('y'))
	if !m.prompt.toggles[0][0] {
		t.Fatal("<C-y> mapped in the detail section should toggle the option in the dock")
	}
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyRight}, {Code: tea.KeyLeft}, {Code: tea.KeyRight}, {Code: tea.KeyEnter}} {
		m, _ = upd(m, k)
	}
	if m.prompt.tab != 2 {
		t.Errorf("right, left, right, enter should commit question 2: tab=%d", m.prompt.tab)
	}
}

func TestDetailRedactionListRemove(t *testing.T) {
	m := newRedactModel()
	m.historyView = histDetail
	m.redact.literals = []string{"aaa", "bbb"}
	for _, k := range []tea.KeyPressMsg{{Code: 'D', Text: "D"}, {Code: 'j', Text: "j"}, {Code: 'u', Text: "u"}} {
		res, _ := m.handleHistoryTranscriptKey(k)
		m = res.(model)
	}
	if len(m.redact.literals) != 1 || m.redact.literals[0] != "aaa" {
		t.Errorf("u in the detail view's redaction list should remove bbb: %v", m.redact.literals)
	}
}

func TestHistorySessionsMoreAndBack(t *testing.T) {
	m := testModel()
	m.mode = modeHistorySessions
	m.history.hasMore = true
	if m, _ = upd(m, keyMsg("m")); !m.history.loading {
		t.Fatal("m should load more sessions")
	}
	if m, _ = upd(m, keyMsg("esc")); m.mode != modeHistoryProjects {
		t.Errorf("esc should return to the history projects: mode=%v", m.mode)
	}
}

func TestLogsKeys(t *testing.T) {
	b := logbuf.New(1000)
	fillLogs(b, 100)
	m := newModel(logsStubClient{}, false, b)
	m.width, m.height = 120, 30
	m.mode = modeLogs
	for _, k := range []tea.KeyPressMsg{keyMsg("k"), keyMsg("j"), ctrlKey('u'), ctrlKey('d'), keyMsg("g"), keyMsg("g")} {
		m, _ = upd(m, k)
	}
	if m.logsScroll != 0 || m.logsFollow {
		t.Fatalf("k j ^u ^d gg should end at the top: scroll=%d follow=%v", m.logsScroll, m.logsFollow)
	}
	if m, _ = upd(m, keyMsg("G")); !m.logsFollow {
		t.Error("G should follow the newest line")
	}
	if m = typeKeys(m, "g?"); !m.projects.showHelp {
		t.Error("g? should open the help")
	}
	m, _ = upd(m, keyMsg("?"))
	hidden, filesHidden := m.projects.sidebarHidden, m.projects.filesHidden
	m = typeKeys(m, " o e")
	if m.projects.sidebarHidden == hidden || m.projects.filesHidden == filesHidden {
		t.Error("␣o and ␣e should toggle the sidebars")
	}
	m.projects.sidebarHidden = false
	for k, want := range map[string]viewMode{"gT": modeHistoryProjects, "gt": modeList} {
		if mm := typeKeys(m, k); mm.mode != want {
			t.Errorf("%s on logs: mode=%v, want %v", k, mm.mode, want)
		}
	}
	if mm, _ := upd(m, keyMsg("esc")); mm.mode != modeList {
		t.Errorf("esc on logs: mode=%v, want %v", mm.mode, modeList)
	}
	if mm := pressKeys(m, cw('h')...); mm.mode != modeProjects {
		t.Errorf("<C-w>h on logs: mode=%v, want %v", mm.mode, modeProjects)
	}
	hist := m
	hist.mode = modeHistoryProjects
	if hist = typeKeys(hist, "gt"); hist.mode != modeLogs {
		t.Errorf("gt on history: mode=%v, want %v", hist.mode, modeLogs)
	}
}

func TestHistoryTranscriptSidebarToggles(t *testing.T) {
	m := testModel()
	m.width, m.mode = 200, modeHistoryTranscript
	m = typeKeys(m, " o e")
	if m.projects.sidebarHidden || m.projects.filesHidden {
		t.Errorf("␣o and ␣e should show both sidebars: left hidden=%v right hidden=%v", m.projects.sidebarHidden, m.projects.filesHidden)
	}
}
