package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

var twoRunning = []session.RunningSubagent{
	{ID: "agent1", Type: "explorer", Desc: "map the repo", Since: "2026-10-16T09:00:00Z"},
	{ID: "agent2", Type: "general-purpose", Name: "reviewer", Desc: "review the diff", Since: "2026-10-16T08:58:55Z"},
}

// withRunning publishes the live session with subs running, as the node would.
func withRunning(m model, subs []session.RunningSubagent) model {
	s := m.sessions[m.liveSessionID()]
	s.Subagents = subs
	params, _ := json.Marshal(registry.Event{Type: registry.EventUpdated, Session: s})
	m, _ = upd(m, notificationMsg(api.Notification{Method: api.MethodSessionEvent, Params: params}))
	return m
}

func fixClock(t *testing.T) {
	old := clockNow
	clockNow = func() time.Time { return time.Date(2026, 10, 16, 9, 0, 12, 0, time.UTC) }
	t.Cleanup(func() { clockNow = old })
}

func runningStream(t *testing.T) (model, *recordingClient) {
	t.Helper()
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1", subagentEntry())
	return withRunning(m, twoRunning), rc
}

func openSubagentsPopup(t *testing.T, m model) model {
	t.Helper()
	m, cmd := upd(m, cmdMsg(sessionKeys.Subagents.name))
	runCmd(cmd)
	if _, ok := m.popups.front().(subagentsPopup); !ok {
		t.Fatalf("open subagent should open the popup; front = %T", m.popups.front())
	}
	return m
}

func TestDockRuleShowsRunningSubagents(t *testing.T) {
	m := withSession(sessionModel(nil), func(s *session.Session) {
		s.Summary = &session.Summary{ModelName: "Opus 5.5", Effort: "high"}
	})
	m.width = 120
	_, before, _ := m.dockRules(80, ColorBorder)
	m = withSession(m, func(s *session.Session) { s.Subagents = twoRunning })
	_, bottom, _ := m.dockRules(80, ColorBorder)
	got := ansi.Strip(bottom)
	if !strings.HasPrefix(got, "── "+Icon.Subagent.Glyph+" 2 ") || !strings.HasSuffix(got, " Opus 5.5 · high ──") {
		t.Fatalf("bottom rule should lead with the running count: %q", got)
	}
	m = withSession(m, func(s *session.Session) { s.Subagents = nil })
	if _, bottom, _ := m.dockRules(80, ColorBorder); bottom != before {
		t.Fatalf("no running subagents: the rule should be unchanged: %q", ansi.Strip(bottom))
	}
	if runningLabel(0) != "" {
		t.Fatalf("runningLabel(0) = %q, want empty", runningLabel(0))
	}
}

func TestSubagentsPopupListsRunning(t *testing.T) {
	fixClock(t)
	m := withRunning(sessionModel(nil), twoRunning)
	m.width, m.height = 120, 30
	m = openSubagentsPopup(t, m)
	out := viewText(m)
	for _, want := range []string{"Subagents", "explorer · map the repo", "reviewer · review the diff"} {
		if !strings.Contains(out, want) {
			t.Fatalf("popup should show %q:\n%s", want, out)
		}
	}
	if l := lineWith(out, "map the repo"); !strings.Contains(l, formatElapsed(12*time.Second)) {
		t.Fatalf("row should show its elapsed time: %q", l)
	}
	if l := lineWith(out, "review the diff"); !strings.Contains(l, formatElapsed(77*time.Second)) {
		t.Fatalf("row should show its elapsed time: %q", l)
	}
	if !m.popups.front().spins(&ctx{m: &m}) {
		t.Fatal("the popup should tick while open")
	}
}

func TestSubagentsPopupEnterDrills(t *testing.T) {
	m, _ := runningStream(t)
	m = openSubagentsPopup(t, m)
	m, cmd := upd(m, keyMsg("j"))
	runCmd(cmd)
	m, cmd = upd(m, keyMsg("enter"))
	runCmd(cmd)
	if len(m.popups) != 0 {
		t.Fatalf("enter should close the popup: %v", m.popups)
	}
	tr := trOf(m)
	if tr.historyView != histDetail || len(tr.transcript.detailStack) != 1 {
		t.Fatalf("want one detail frame: view=%v frames=%d", tr.historyView, len(tr.transcript.detailStack))
	}
	if f := tr.transcript.detailStack[0]; f.agentID != "agent2" || f.subID == "" {
		t.Fatalf("frame should stream agent2: %+v", f)
	}
	if tr.activeSub.agentID != "agent2" {
		t.Fatalf("activeSub.agentID = %q, want agent2", tr.activeSub.agentID)
	}
	if m.focused != mainPane {
		t.Fatalf("focus = %v, want the transcript", m.focused)
	}
}

func TestSubagentsPopupEnterFromDetailSwitches(t *testing.T) {
	m, rc := runningStream(t)
	m = withFocus(m, mainPane)
	m = drillSubagent(t, m)
	old := trOf(m).transcript.detailStack[0].subID
	session := trOf(m).sessionSub
	if old == "" || session.subID == "" {
		t.Fatalf("setup: want a streamed frame over the session stream")
	}
	m = openSubagentsPopup(t, m)
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	tr := trOf(m)
	if len(tr.transcript.detailStack) != 1 || tr.transcript.detailStack[0].agentID != "agent1" {
		t.Fatalf("want only agent1's frame: %+v", tr.transcript.detailStack)
	}
	assertUnsubscribed(t, rc, old)
	if tr.sessionSub != session {
		t.Fatalf("the session stream should be kept for back: %+v, want %+v", tr.sessionSub, session)
	}
	m, cmd = upd(m, keyMsg("esc"))
	runCmd(cmd)
	if tr := trOf(m); tr.historyView != histTranscript || tr.activeSub != session {
		t.Fatalf("back should return to the session stream: view=%v sub=%+v", tr.historyView, tr.activeSub)
	}
}

func TestSubagentsPopupFromTheDock(t *testing.T) {
	m, _ := runningStream(t)
	m = withFocus(m, sessionDock)
	m = openSubagentsPopup(t, m)
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	if tr := trOf(m); tr.activeSub.agentID != "agent1" || m.focused != mainPane {
		t.Fatalf("from the dock: sub=%+v focus=%v", tr.activeSub, m.focused)
	}
}

func TestSubagentsPopupClosesWhenNoneRun(t *testing.T) {
	m := withRunning(sessionModel(nil), twoRunning)
	m = openSubagentsPopup(t, m)
	m = pressKeys(m, keyMsg("j"))
	m = withRunning(m, twoRunning[:1])
	if p, ok := m.popups.front().(subagentsPopup); !ok || p.cursor != 0 {
		t.Fatalf("a dropped row should clamp the cursor: %+v", m.popups.front())
	}
	m = withRunning(m, nil)
	if len(m.popups) != 0 {
		t.Fatalf("the popup should close when none run: %v", m.popups)
	}
}

func TestSubagentsListCommand(t *testing.T) {
	m := sessionModel(nil)
	m, _ = upd(m, cmdMsg(sessionKeys.Subagents.name))
	if len(m.popups) != 0 {
		t.Fatalf("no running subagents: nothing should open: %v", m.popups)
	}
	m = withRunning(m, twoRunning)
	openSubagentsPopup(t, m)
	openSubagentsPopup(t, withFocus(m, sessionDock))
}

func TestSubagentsClickOpensPopup(t *testing.T) {
	m := withRunning(sessionModel(nil), twoRunning)
	m.width, m.height = 120, 30
	m = withMouse(m)
	m.View()
	a := m.hits.areas
	var at *hitArea
	for _, h := range a {
		if h.region == regDock {
			at = h
		}
	}
	if at == nil {
		t.Fatal("no dock area")
	}
	var zone hitZone
	for _, z := range at.zones {
		if z.target.kind == hitSubagents {
			zone = z
		}
	}
	if zone.rect.Empty() {
		t.Fatal("no subagents hit zone")
	}
	x, y := at.rect.Min.X+zone.rect.Min.X, at.rect.Min.Y+zone.rect.Min.Y
	if l := strings.Split(viewText(m), "\n")[y]; !strings.Contains(l, Icon.Subagent.Glyph+" 2") {
		t.Fatalf("the zone should sit on the count's row: %q", l)
	}
	m, _ = click(m, x, y)
	if _, ok := m.popups.front().(subagentsPopup); !ok || m.focused != mainPane {
		t.Fatalf("a click on the count should open the popup and keep focus: %T focus=%v", m.popups.front(), m.focused)
	}
}

func countZone(m model) (x, y int, ok bool) {
	m.View()
	for _, a := range m.hits.areas {
		for _, z := range a.zones {
			if z.target.kind == hitSubagents {
				return a.rect.Min.X + z.rect.Min.X, a.rect.Min.Y + z.rect.Min.Y, true
			}
		}
	}
	return 0, 0, false
}

func TestRunningCountZoneNeedsTheLabelDrawn(t *testing.T) {
	m := withMouse(withSession(sessionModel(nil), func(s *session.Session) {
		s.Subagents = twoRunning
		s.Summary = &session.Summary{ModelName: "Opus 5.5", Effort: "high"}
	}))
	m.width, m.height = 120, 30
	x, y, ok := countZone(m)
	if !ok {
		t.Fatal("a wide dock should have a count zone")
	}
	if cell := ansi.Cut(strings.Split(ansi.Strip(m.View().Content), "\n")[y], x, x+1); cell != Icon.Subagent.Glyph {
		t.Fatalf("the zone starts at %q, want the glyph", cell)
	}
	m.width = 24
	if _, _, ok := countZone(m); ok {
		t.Fatal("a narrow dock draws no count, so it should have no zone")
	}
	if out := ansi.Strip(m.View().Content); strings.Contains(out, Icon.Subagent.Glyph) {
		t.Fatalf("a narrow dock should not show the glyph:\n%s", out)
	}
}

func TestClickingTheCountEndsVisualMode(t *testing.T) {
	m := withEntries(sessionModel(nil), []transcript.Entry{{ID: "t1", Kind: transcript.EntryText, Text: "alpha beta gamma"}})
	m = withMouse(withSession(m, func(s *session.Session) { s.Subagents = twoRunning }))
	m.width, m.height = 120, 30
	m = typeKeys(m, "v")
	if !m.visualIn(mainPane) {
		t.Fatal("setup: visual mode should be active")
	}
	x, y, ok := countZone(m)
	if !ok {
		t.Fatal("no count zone")
	}
	m, _ = click(m, x, y)
	if _, ok := m.popups.front().(subagentsPopup); !ok {
		t.Fatalf("the click should open the popup; front = %T", m.popups.front())
	}
	if m.visualIn(mainPane) {
		t.Fatal("opening the list should end visual mode")
	}
}
