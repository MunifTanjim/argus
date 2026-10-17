package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

var twoShells = []session.RunningShell{
	{ID: "b1", ToolUseID: "t1", Command: "make test", Desc: "run tests", Since: "2026-10-16T09:00:00Z"},
	{ID: "b2", ToolUseID: "t2", Command: "npm run dev", Since: "2026-10-16T08:58:55Z"},
}

// withShells publishes the live session with shells running, as the node would.
func withShells(m model, shells []session.RunningShell) model {
	s := m.sessions[m.liveSessionID()]
	s.Shells = shells
	params, _ := json.Marshal(registry.Event{Type: registry.EventUpdated, Session: s})
	m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodSessionEvent, Params: params}))
	return m
}

func openShellsPopup(t *testing.T, m model) model {
	t.Helper()
	m, cmd := upd(m, cmdMsg(sessionKeys.Shells.name))
	runCmd(cmd)
	if _, ok := runningFront(m, runningShells); !ok {
		t.Fatalf("open shell should open the popup; front = %T", m.popups.front())
	}
	return m
}

func runningFront(m model, k runningKind) (runningPopup, bool) {
	p, ok := m.popups.front().(runningPopup)
	return p, ok && p.kind == k
}

func zoneAt(m model, k runningKind) (x, y int, ok bool) {
	m.View()
	for _, a := range m.hits.areas {
		for _, z := range a.zones {
			if z.target.kind == hitRunning && z.target.index == int(k) {
				return a.rect.Min.X + z.rect.Min.X, a.rect.Min.Y + z.rect.Min.Y, true
			}
		}
	}
	return 0, 0, false
}

func TestDockRuleShowsRunningShells(t *testing.T) {
	m := withSession(sessionModel(nil), func(s *session.Session) {
		s.Summary = &session.Summary{ModelName: "Opus 5.5", Effort: "high"}
	})
	m.width = 120
	_, before, _ := m.dockRules(80, ColorBorder)
	m = withSession(m, func(s *session.Session) { s.Shells = twoShells })
	_, bottom, _ := m.dockRules(80, ColorBorder)
	if got := ansi.Strip(bottom); !strings.HasPrefix(got, "── "+Icon.ShellRunning.Glyph+" 2 ") {
		t.Fatalf("bottom rule should lead with the shell count: %q", got)
	}
	m = withSession(m, func(s *session.Session) { s.Subagents = twoRunning })
	_, bottom, _ = m.dockRules(80, ColorBorder)
	if got := ansi.Strip(bottom); !strings.HasPrefix(got, "── "+Icon.Subagent.Glyph+" 2 "+Icon.ShellRunning.Glyph+" 2 ") {
		t.Fatalf("the subagent count should come first: %q", got)
	}
	m = withSession(m, func(s *session.Session) { s.Subagents, s.Shells = nil, nil })
	if _, bottom, _ := m.dockRules(80, ColorBorder); bottom != before {
		t.Fatalf("nothing running: the rule should be unchanged: %q", ansi.Strip(bottom))
	}
	if runningShells.label(0) != "" {
		t.Fatalf("runningShells.label(0) = %q, want empty", runningShells.label(0))
	}
}

func TestShellsPopupListsRunning(t *testing.T) {
	fixClock(t)
	m := withShells(sessionModel(nil), twoShells)
	m.width, m.height = 120, 30
	m = openShellsPopup(t, m)
	out := viewText(m)
	for _, want := range []string{"Shells", "run tests", "npm run dev"} {
		if !strings.Contains(out, want) {
			t.Fatalf("popup should show %q:\n%s", want, out)
		}
	}
	if l := lineWith(out, "run tests"); !strings.Contains(l, formatElapsed(12*time.Second)) {
		t.Fatalf("row should show its elapsed time: %q", l)
	}
	if l := lineWith(out, "npm run dev"); !strings.Contains(l, formatElapsed(77*time.Second)) {
		t.Fatalf("row should show its elapsed time: %q", l)
	}
	if !m.popups.front().spins(&ctx{m: &m}) {
		t.Fatal("the popup should tick while open")
	}
}

func TestShellsPopupEnterDrills(t *testing.T) {
	rc := &recordingClient{}
	m := withShells(resumeInto(streamModel(rc), "n1:s1", subagentEntry()), twoShells)
	m = openShellsPopup(t, m)
	before := countCalls(rc, api.MethodSessionToolDetail)
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	if len(m.popups) != 0 {
		t.Fatalf("enter should close the popup: %v", m.popups)
	}
	tr := trOf(m)
	if tr.historyView != histDetail || len(tr.transcript.detailStack) != 1 {
		t.Fatalf("want one detail frame: view=%v frames=%d", tr.historyView, len(tr.transcript.detailStack))
	}
	f := tr.transcript.detailStack[0]
	if len(f.items) != 1 || f.items[0].Kind != transcript.EntryTool || f.items[0].ToolName != "Bash" || f.items[0].ToolID != "t1" {
		t.Fatalf("frame should hold the shell's Bash call: %+v", f.items)
	}
	if got := countCalls(rc, api.MethodSessionToolDetail); got != before+1 {
		t.Fatalf("the drill should fetch the call's detail: %d calls, want %d", got, before+1)
	}
	if m.focused != mainPane {
		t.Fatalf("focus = %v, want the transcript", m.focused)
	}
}

func TestShellsPopupClosesWhenNoneRun(t *testing.T) {
	m := withShells(sessionModel(nil), twoShells)
	m = openShellsPopup(t, m)
	m = pressKeys(m, keyMsg("j"))
	m = withShells(m, twoShells[:1])
	if p, ok := runningFront(m, runningShells); !ok || p.cursor != 0 {
		t.Fatalf("a dropped row should clamp the cursor: %+v", m.popups.front())
	}
	m = withShells(m, nil)
	if len(m.popups) != 0 {
		t.Fatalf("the popup should close when none run: %v", m.popups)
	}
}

func TestShellsListCommand(t *testing.T) {
	m := sessionModel(nil)
	m, _ = upd(m, cmdMsg(sessionKeys.Shells.name))
	if len(m.popups) != 0 {
		t.Fatalf("no running shells: nothing should open: %v", m.popups)
	}
	m = withShells(m, twoShells)
	openShellsPopup(t, m)
	openShellsPopup(t, withFocus(m, sessionDock))
}

func TestShellsClickOpensPopup(t *testing.T) {
	m := withShells(withRunning(sessionModel(nil), twoRunning), twoShells)
	m.width, m.height = 120, 30
	m = withMouse(m)
	x, y, ok := zoneAt(m, runningShells)
	if !ok {
		t.Fatal("no shells hit zone")
	}
	if cell := ansi.Cut(strings.Split(ansi.Strip(m.View().Content), "\n")[y], x, x+1); cell != Icon.ShellRunning.Glyph {
		t.Fatalf("the zone starts at %q, want the shell glyph", cell)
	}
	m2, _ := click(m, x, y)
	if _, ok := runningFront(m2, runningShells); !ok || m2.focused != mainPane {
		t.Fatalf("a click on the count should open the popup and keep focus: %T focus=%v", m2.popups.front(), m2.focused)
	}
	x, y, _ = zoneAt(m, runningSubagents)
	m, _ = click(m, x, y)
	if _, ok := runningFront(m, runningSubagents); !ok {
		t.Fatalf("the subagent count should still open its popup: %T", m.popups.front())
	}
}

func TestShellCountZoneNeedsTheLabelDrawn(t *testing.T) {
	m := withMouse(withSession(sessionModel(nil), func(s *session.Session) {
		s.Shells = twoShells
		s.Summary = &session.Summary{ModelName: "Opus 5.5", Effort: "high"}
	}))
	m.width, m.height = 24, 30
	if _, _, ok := zoneAt(m, runningShells); ok {
		t.Fatal("a narrow dock draws no count, so it should have no zone")
	}
	if out := ansi.Strip(m.View().Content); strings.Contains(out, Icon.ShellRunning.Glyph) {
		t.Fatalf("a narrow dock should not show the glyph:\n%s", out)
	}
}

func TestNarrowDockKeepsTheSubagentCount(t *testing.T) {
	m := withSession(sessionModel(nil), func(s *session.Session) {
		s.Subagents, s.Shells = twoRunning, twoShells
		s.Summary = &session.Summary{ModelName: "Opus 5.5", Effort: "high"}
	})
	m.width = 120
	subOnly := false
	for w := 8; w <= 80; w++ {
		_, bottom, zones := m.dockRules(w, ColorBorder)
		got := ansi.Strip(bottom)
		hasSub, hasShell := strings.Contains(got, Icon.Subagent.Glyph), strings.Contains(got, Icon.ShellRunning.Glyph)
		if hasShell && !hasSub {
			t.Fatalf("width %d: the shell count shows without the subagent count: %q", w, got)
		}
		var kinds []runningKind
		for _, z := range zones {
			kinds = append(kinds, z.kind)
		}
		want := []runningKind(nil)
		if hasSub {
			want = append(want, runningSubagents)
		}
		if hasShell {
			want = append(want, runningShells)
		}
		if fmt.Sprint(kinds) != fmt.Sprint(want) {
			t.Fatalf("width %d: zones %v, want %v for %q", w, kinds, want, got)
		}
		subOnly = subOnly || (hasSub && !hasShell)
	}
	if !subOnly {
		t.Fatal("some width should fit the subagent count but not both")
	}
}

func TestShellsPopupRowWithoutCallStaysOpen(t *testing.T) {
	rc := &recordingClient{}
	m := withShells(resumeInto(streamModel(rc), "n1:s1", subagentEntry()), []session.RunningShell{{ID: "b9", Command: "sleep 9"}})
	m = withFocus(m, sessionDock)
	m = openShellsPopup(t, m)
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	if _, ok := runningFront(m, runningShells); !ok || m.focused != sessionDock {
		t.Fatalf("a shell with no call has nothing to open: popup=%T focus=%v", m.popups.front(), m.focused)
	}
}

func TestShellsPopupRowText(t *testing.T) {
	m := withShells(sessionModel(nil), []session.RunningShell{
		{ID: "b1", Command: "make test", Desc: "run tests"},
		{ID: "b2"},
	})
	m.width, m.height = 120, 30
	m = openShellsPopup(t, m)
	out := viewText(m)
	if !strings.Contains(out, "run tests · make test") {
		t.Fatalf("a row should show its description and command:\n%s", out)
	}
	if lineWith(out, "Shell ") == "" {
		t.Fatalf("a shell with neither should read Shell:\n%s", out)
	}
}

func TestRunningPopupsKeepTheirOwnScroll(t *testing.T) {
	m := withShells(withRunning(sessionModel(nil), twoRunning), twoShells)
	m.width, m.height = 120, 30
	m = openSubagentsPopup(t, m)
	viewText(m)
	m = pressKeys(m, keyMsg("esc"))
	m = openShellsPopup(t, m)
	viewText(m)
	for _, k := range []string{"subagents", "shells"} {
		if _, ok := m.listScroll[k]; !ok {
			t.Fatalf("each popup should keep its scroll under its own key; have %v", m.listScroll)
		}
	}
}
