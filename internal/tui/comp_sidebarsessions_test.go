package tui

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
)

func sidebarSessionsCtx() *ctx {
	m := projectsTestModel()
	m.width, m.height = 160, 30
	m.sessions = map[string]session.Session{
		"n1:a": {ID: "n1:a", Name: "alpha", WorkspaceID: "n1:w1", Status: session.StatusAwaitingInput},
		"n1:b": {ID: "n1:b", Name: "beta", WorkspaceID: "n1:w2", Status: session.StatusWorking},
		"n1:c": {ID: "n1:c", Name: "gamma", WorkspaceID: "n1:w1", Status: session.StatusIdle},
		"n1:d": {ID: "n1:d", Name: "delta", Status: session.StatusDead},
	}
	m.left.tab = leftSessions
	m = withFocus(m, leftSidebar)
	return &ctx{m: &m}
}

func TestSidebarSessionsKeys(t *testing.T) {
	c := sidebarSessionsCtx()
	var comp component = sidebarSessionsComp{}
	comp, _, ok := comp.handleKey(c, keyMsg("j"))
	if s := comp.(sidebarSessionsComp); !ok || s.sel != "n1:b" {
		t.Fatalf("j moves to the second session: ok=%v sel=%q", ok, s.sel)
	}
	comp, _, _ = comp.handleKey(c, keyMsg("G"))
	if s := comp.(sidebarSessionsComp); s.sel != "n1:c" {
		t.Fatalf("G moves to the last live session, got %q", s.sel)
	}
	comp, _, _ = comp.handleKey(c, keyMsg("enter"))
	if len(c.actions) != 1 || c.actions[0].kind != actOpenSession || c.actions[0].id != "n1:c" {
		t.Fatalf("enter opens the session under the cursor: %v", actionKinds(c))
	}
	c.actions = nil
	if _, _, ok := comp.handleKey(c, keyMsg("esc")); !ok || len(c.actions) != 1 || c.actions[0].focus != mainPane {
		t.Errorf("esc focuses the main pane: %v", actionKinds(c))
	}
	if comp.section() != "sidebar-sessions" {
		t.Errorf("section = %q", comp.section())
	}
}

func TestSidebarSessionsCursorFollowsSession(t *testing.T) {
	c := sidebarSessionsCtx()
	s := sidebarSessionsComp{sel: "n1:c", at: 2}
	// beta finishes and leaves Working; gamma moves up but stays selected.
	b := c.m.sessions["n1:b"]
	b.Status = session.StatusDead
	c.m.sessions["n1:b"] = b
	ids := sidebarSessionIDs(sidebarSessionGroups(c.m.sessions))
	if i := s.index(ids); ids[i] != "n1:c" {
		t.Fatalf("cursor on %q, want gamma", ids[i])
	}
	// gamma dies: the cursor keeps its place, clamped to the list.
	g := c.m.sessions["n1:c"]
	g.Status = session.StatusDead
	c.m.sessions["n1:c"] = g
	ids = sidebarSessionIDs(sidebarSessionGroups(c.m.sessions))
	if i := s.index(ids); i != len(ids)-1 {
		t.Fatalf("index = %d, want the last row %d", i, len(ids)-1)
	}
	if i := s.index(nil); i != 0 {
		t.Fatalf("empty list index = %d, want 0", i)
	}
}

func TestSidebarSessionsView(t *testing.T) {
	c := sidebarSessionsCtx()
	out := sidebarSessionsComp{}.view(c, 34, 20)
	for _, want := range []string{"Needs you", "alpha", "Working", "beta", "Idle", "gamma", "argus / repo", "argus / repo-feat"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"delta", "Offline"} {
		if strings.Contains(out, gone) {
			t.Errorf("view shows %q:\n%s", gone, out)
		}
	}
	if strings.Index(out, "alpha") > strings.Index(out, "beta") || strings.Index(out, "beta") > strings.Index(out, "gamma") {
		t.Errorf("sections out of order:\n%s", out)
	}
	c.m.sessions = nil
	if out := (sidebarSessionsComp{}).view(c, 34, 20); !strings.Contains(out, "no live sessions") {
		t.Errorf("empty view = %q", out)
	}
}

func TestSidebarSessionsClick(t *testing.T) {
	c := sidebarSessionsCtx()
	comp, _ := sidebarSessionsComp{}.click(c, hitTarget{kind: hitRow, index: 1}, true)
	if s := comp.(sidebarSessionsComp); s.sel != "n1:b" || len(c.actions) != 0 {
		t.Fatalf("a click on another row selects it: sel=%q actions=%v", s.sel, actionKinds(c))
	}
	comp, _ = comp.(sidebarSessionsComp).click(c, hitTarget{kind: hitRow, index: 1}, true)
	if len(c.actions) != 1 || c.actions[0].kind != actOpenSession || c.actions[0].id != "n1:b" {
		t.Fatalf("a click on the selected row opens it: %v", actionKinds(c))
	}
}

func TestSidebarSessionsRowsTruncateByWidth(t *testing.T) {
	c := sidebarSessionsCtx()
	a := c.m.sessions["n1:a"]
	a.Name = "日本語のセッション名前がとても長い"
	c.m.sessions["n1:a"] = a
	const w = 14
	out := sidebarSessionsComp{}.view(c, w, 20)
	if !utf8.ValidString(out) {
		t.Fatalf("view is not valid UTF-8:\n%q", out)
	}
	for _, ln := range strings.Split(ansi.Strip(out), "\n") {
		if got := lipgloss.Width(ln); got > screenMargin+w {
			t.Errorf("line %q is %d wide, want at most %d", ln, got, screenMargin+w)
		}
	}
}

func TestSidebarSessionsAgentSuffixCountsListedSessions(t *testing.T) {
	c := sidebarSessionsCtx()
	for id, s := range c.m.sessions {
		s.Agent = "claude"
		c.m.sessions[id] = s
	}
	d := c.m.sessions["n1:d"]
	d.Agent = "codex"
	c.m.sessions["n1:d"] = d
	if out := (sidebarSessionsComp{}).view(c, 40, 20); strings.Contains(out, "Claude") {
		t.Errorf("a dead session's agent must not trigger the suffix:\n%s", out)
	}
	b := c.m.sessions["n1:b"]
	b.Agent = "codex"
	c.m.sessions["n1:b"] = b
	out := (sidebarSessionsComp{}).view(c, 40, 20)
	if !strings.Contains(out, "Claude") || !strings.Contains(out, "Codex") {
		t.Errorf("listed sessions mix agents, the suffix should show:\n%s", out)
	}
}

func TestSidebarSessionsGolden(t *testing.T) {
	m := projectsTestModel()
	m.width, m.height = 160, 30
	m.sessions = map[string]session.Session{
		"n1:a": {ID: "n1:a", Name: "fix-login", Agent: "claude", WorkspaceID: "n1:w1", Status: session.StatusAwaitingInput},
		"n1:b": {ID: "n1:b", Name: "refactor-api", Agent: "codex", WorkspaceID: "n1:w2", Status: session.StatusWorking},
		"n1:c": {ID: "n1:c", Name: "write-docs", Agent: "claude", Repo: "notes", Status: session.StatusIdle},
		"n1:d": {ID: "n1:d", Name: "old-run", Agent: "claude", WorkspaceID: "n1:w1", Status: session.StatusDead},
		"n1:e": {ID: "n1:e", Name: "remote-job", Agent: "codex", WorkspaceID: "n1:w1", Status: session.StatusIdle, Offline: true},
	}
	m.order = []string{"n1:a", "n1:b", "n1:c", "n1:e"}
	m = openLive(m, "n1:a")
	m = withEntries(m, sampleEntries())
	m.left.tab = leftSessions
	m = withFocus(m, leftSidebar)
	assertGolden(t, "sidebar-sessions", m)
}

func sidebarFilterModel() model {
	m := projectsTestModel()
	m.width, m.height = 160, 30
	m.left.tab = leftSessions
	m.sessions = map[string]session.Session{
		"n1:a": {ID: "n1:a", Name: "alpha", WorkspaceID: "n1:w1", Status: session.StatusAwaitingInput},
		"n1:b": {ID: "n1:b", Name: "beta", WorkspaceID: "n1:w2", Status: session.StatusWorking},
		"n1:c": {ID: "n1:c", Name: "gamma", WorkspaceID: "n1:w1", Status: session.StatusIdle},
	}
	return m
}

func sidebarListed(m model) []string {
	return m.left.sessions.ids(&ctx{m: &m})
}

func TestSidebarSessionsFilter(t *testing.T) {
	m := sidebarFilterModel()
	m = typeKeys(m, "/")
	if !m.focusedComp().raw(&ctx{m: &m}) {
		t.Fatal("/ opens the filter prompt")
	}
	m = typeKeys(m, "ga")
	if got := sidebarListed(m); !slices.Equal(got, []string{"n1:c"}) {
		t.Fatalf("typing ga lists %v, want gamma", got)
	}
	m, _ = upd(m, keyMsg("enter"))
	if m.focusedComp().raw(&ctx{m: &m}) || m.left.sessions.query != "ga" {
		t.Fatalf("enter keeps the filter and closes the prompt: query=%q", m.left.sessions.query)
	}
	if m.sessionFilter != "" {
		t.Errorf("the home list's filter changed to %q", m.sessionFilter)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Projects  Sessions  /ga") {
		t.Errorf("the strip does not name the filter")
	}
	// Esc clears the filter before it leaves the sidebar.
	m, _ = upd(m, keyMsg("esc"))
	if m.left.sessions.query != "" || m.focused != leftSidebar {
		t.Fatalf("esc clears the filter first: query=%q focused=%v", m.left.sessions.query, m.focused)
	}
	m, _ = upd(m, keyMsg("esc"))
	if m.focused != mainPane {
		t.Fatalf("esc without a filter focuses the main pane, got %v", m.focused)
	}
}

func TestSidebarSessionsFilterPromptEscClears(t *testing.T) {
	m := typeKeys(sidebarFilterModel(), "/be")
	if got := sidebarListed(m); !slices.Equal(got, []string{"n1:b"}) {
		t.Fatalf("typing be lists %v", got)
	}
	m, _ = upd(m, keyMsg("esc"))
	if m.left.sessions.query != "" || len(sidebarListed(m)) != 3 {
		t.Fatalf("esc in the prompt clears it: query=%q", m.left.sessions.query)
	}
}

func TestSidebarSessionsFilterMatchesPlace(t *testing.T) {
	m, _ := upd(typeKeys(sidebarFilterModel(), "/repo-feat"), keyMsg("enter"))
	if got := sidebarListed(m); !slices.Equal(got, []string{"n1:b"}) {
		t.Fatalf("filtering by workspace lists %v, want beta", got)
	}
}

func TestSidebarSessionsFilterNoMatch(t *testing.T) {
	m, _ := upd(typeKeys(sidebarFilterModel(), "/zzz"), keyMsg("enter"))
	c := &ctx{m: &m}
	out := ansi.Strip(m.left.sessions.view(c, 34, 20))
	if !strings.Contains(out, "no sessions match /zzz") {
		t.Fatalf("no-match view = %q", out)
	}
}

func TestSidebarSessionsFilterClampsCursor(t *testing.T) {
	m := typeKeys(sidebarFilterModel(), "G")
	if m.left.sessions.sel != "n1:c" {
		t.Fatalf("G selects gamma, got %q", m.left.sessions.sel)
	}
	m, _ = upd(typeKeys(m, "/be"), keyMsg("enter"))
	ids := sidebarListed(m)
	if i := m.left.sessions.index(ids); ids[i] != "n1:b" {
		t.Fatalf("cursor on %q, want the only match beta", ids[i])
	}
}
