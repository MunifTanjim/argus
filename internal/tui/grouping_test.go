package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// groupTree is a registry with the same project name on two hosts, a second
// workspace on alpha, and projects that sort before and after "other".
var groupTree = []api.ProjectNode{
	{ID: "a:p1", Name: "argus", NodeID: "a", NodeLabel: "alpha", Workspaces: []api.WorkspaceNode{
		{ID: "a:w1", Dir: "/src/argus", IsMain: true}, {ID: "a:w2", Dir: "/src/argus-feat"},
		{ID: "a:w5", Dir: "/src/.worktrees/aaa"}, {ID: "a:w6", Dir: "/src/.worktrees/zzz"},
	}},
	{ID: "b:p1", Name: "argus", NodeID: "b", NodeLabel: "beta", Workspaces: []api.WorkspaceNode{
		{ID: "b:w1", Dir: "/src/argus", IsMain: true},
	}},
	{ID: "a:p2", Name: "dotfiles", NodeID: "a", NodeLabel: "alpha", Workspaces: []api.WorkspaceNode{
		{ID: "a:w3", Dir: "/src/dotfiles", IsMain: true},
	}},
	{ID: "a:p3", Name: "zeta", NodeID: "a", NodeLabel: "alpha", Workspaces: []api.WorkspaceNode{
		{ID: "a:w4", Dir: "/src/zeta"},
	}},
}

func groupedModel(g groupBy, sessions ...session.Session) model {
	m := testModel()
	m.groupBy = g
	m.left.tree.data = groupTree
	m.sessions = map[string]session.Session{}
	for _, s := range sessions {
		m.sessions[s.ID] = s
	}
	m.reorder()
	return m
}

func gs(id, label, ws, agent string, st session.Status) session.Session {
	return session.Session{
		ID: id, NodeID: label, NodeLabel: label, WorkspaceID: ws, Agent: agent, Status: st,
		Tmux: session.TmuxLocation{PaneID: "%1"},
	}
}

func sectionTitles(m model) []string {
	var out []string
	for i, id := range m.order {
		sec := m.sessionSection(m.sessions[id])
		if i == 0 || !sec.same(m.sessionSection(m.sessions[m.order[i-1]])) {
			out = append(out, sec.title)
		}
	}
	return out
}

func TestSectionsByDimension(t *testing.T) {
	idle, wait := session.StatusIdle, session.StatusAwaitingInput
	cases := []struct {
		name      string
		by        groupBy
		sessions  []session.Session
		wantTitle []string
		wantOrder []string
	}{
		{"zero value is host", "", []session.Session{
			gs("b:1", "beta", "", "", idle), gs("a:1", "alpha", "", "", idle), gs("a:2", "alpha", "", "", wait),
		}, []string{"Needs you", "alpha", "beta"}, []string{"a:2", "a:1", "b:1"}},
		{"host", groupByHost, []session.Session{
			gs("b:1", "beta", "", "", idle), gs("a:1", "alpha", "", "", idle),
		}, []string{"alpha", "beta"}, nil},
		{"project merges by name across hosts; other last", groupByProject, []session.Session{
			gs("b:1", "beta", "b:w1", "", idle), gs("a:1", "alpha", "a:w1", "", idle),
			gs("a:3", "alpha", "a:w4", "", idle), gs("a:2", "alpha", "a:w3", "", idle),
			gs("a:9", "alpha", "", "", idle), gs("a:8", "alpha", "a:gone", "", idle),
		}, []string{"argus", "dotfiles", "zeta", "other"}, []string{"a:1", "b:1", "a:2", "a:3", "a:8", "a:9"}},
		{"workspace stays per host and names it on a gateway", groupByWorkspace, []session.Session{
			gs("b:1", "beta", "b:w1", "", idle), gs("a:1", "alpha", "a:w1", "", idle),
			gs("a:2", "alpha", "a:w2", "", idle), gs("a:9", "alpha", "", "", idle),
		}, []string{"argus / argus · alpha", "argus / argus · beta", "argus / argus-feat · alpha", "other"}, nil},
		{"workspace without a gateway", groupByWorkspace, []session.Session{
			gs("s1", "", "a:w1", "", idle),
		}, []string{"argus / argus"}, nil},
		{"workspaces of a project stay together, main first", groupByWorkspace, []session.Session{
			gs("s1", "", "a:w6", "", idle), gs("s2", "", "a:w3", "", idle), gs("s3", "", "a:w5", "", idle),
			gs("s4", "", "a:w2", "", idle), gs("s5", "", "a:w1", "", idle),
		}, []string{"argus / argus", "argus / aaa", "argus / argus-feat", "argus / zzz", "dotfiles / dotfiles"}, nil},
		{"agent", groupByAgent, []session.Session{
			gs("a:1", "alpha", "", "codex", idle), gs("a:2", "alpha", "", "claude", idle), gs("a:3", "alpha", "", "", idle),
		}, []string{"Claude", "Codex", "other"}, nil},
		{"status in lifecycle order after needs you", groupByStatus, []session.Session{
			gs("a:1", "alpha", "", "", session.StatusDead), gs("a:2", "alpha", "", "", idle),
			gs("a:3", "alpha", "", "", session.StatusWorking), gs("a:4", "alpha", "", "", session.StatusStarting),
			gs("a:5", "alpha", "", "", wait),
		}, []string{"Needs you", "starting", "working", "idle", "dead"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := groupedModel(c.by, c.sessions...)
			if got := sectionTitles(m); !slices.Equal(got, c.wantTitle) {
				t.Errorf("titles = %q, want %q", got, c.wantTitle)
			}
			if c.wantOrder != nil && !slices.Equal(m.order, c.wantOrder) {
				t.Errorf("order = %q, want %q", m.order, c.wantOrder)
			}
		})
	}
}

func TestGroupByCycle(t *testing.T) {
	g := groupByHost
	var seen []groupBy
	for range groupByCycle {
		g = g.next()
		seen = append(seen, g)
	}
	want := []groupBy{groupByProject, groupByWorkspace, groupByAgent, groupByStatus, groupByHost}
	if !slices.Equal(seen, want) {
		t.Fatalf("cycle = %q, want %q", seen, want)
	}
	if groupBy("").next() != groupByProject {
		t.Fatal("the zero value is host, so it should advance to project")
	}
	if _, ok := parseGroupBy("folder"); ok {
		t.Fatal("unknown dimension should not parse")
	}
	if g, ok := parseGroupBy("agent"); !ok || g != groupByAgent {
		t.Fatalf("parseGroupBy(agent) = %q, %v", g, ok)
	}
}

func TestProjectTreeLoadRegroups(t *testing.T) {
	m := groupedModel(groupByProject, gs("a:1", "alpha", "a:w1", "", session.StatusIdle))
	m.left.tree.data = nil
	m.reorder()
	if got := sectionTitles(m); !slices.Equal(got, []string{"other"}) {
		t.Fatalf("without a tree the session is in other, got %q", got)
	}
	res, _ := m.Update(projectsTreeMsg{seq: m.left.tree.fetchSeq, tree: groupTree})
	if got := sectionTitles(res.(model)); !slices.Equal(got, []string{"argus"}) {
		t.Fatalf("after the tree loads the session moves to its project, got %q", got)
	}
}

func TestProjectSectionsRender(t *testing.T) {
	m := groupedModel(groupByProject,
		gs("a:1", "alpha", "a:w1", "", session.StatusIdle),
		gs("b:1", "beta", "b:w1", "", session.StatusIdle),
	)
	out := ansi.Strip(paneView(m))
	if strings.Count(out, "\n"+Icon.Repo.Glyph+" argus") != 1 {
		t.Fatalf("one merged argus header expected:\n%s", out)
	}
	if strings.Contains(out, hostHeader("alpha")) || strings.Contains(out, hostHeader("beta")) {
		t.Fatalf("project grouping draws no host headers:\n%s", out)
	}
	// The section does not name the host, so each card does.
	for _, h := range []string{"alpha", "beta"} {
		if !strings.Contains(out, Icon.Node.Glyph+" "+h) {
			t.Errorf("card should name host %s:\n%s", h, out)
		}
	}
}

func TestNonHostSectionsHaveHeadersWithoutGateway(t *testing.T) {
	m := groupedModel(groupByProject, gs("s1", "", "a:w1", "", session.StatusIdle))
	out := ansi.Strip(paneView(m))
	if !strings.Contains(out, "\n"+Icon.Repo.Glyph+" argus") {
		t.Fatalf("project header expected without a gateway:\n%s", out)
	}
	if strings.Contains(out, Icon.Node.Glyph) {
		t.Fatalf("no host on a single node:\n%s", out)
	}
}

func TestHostSectionCardsOmitHost(t *testing.T) {
	m := groupedModel(groupByHost, gs("a:1", "alpha", "", "", session.StatusIdle))
	out := ansi.Strip(paneView(m))
	if strings.Count(out, Icon.Node.Glyph+" alpha") != 1 {
		t.Fatalf("only the host header names alpha:\n%s", out)
	}
}

func TestLocalSectionAfterNeedsYouKeepsHeader(t *testing.T) {
	// On a gateway with a local session, Needs you and the local host section
	// both have an empty key; the header check must tell them apart by rank.
	m := groupedModel(groupByHost,
		gs("a:1", "alpha", "", "", session.StatusIdle),
		gs("s1", "", "", "", session.StatusAwaitingInput),
		gs("s2", "", "", "", session.StatusIdle),
	)
	out := ansi.Strip(paneView(m))
	ny := indexOf(t, out, "Needs you")
	if local := indexOf(t, out, hostHeader("local")); local < ny {
		t.Fatalf("the local header should follow Needs you:\n%s", out)
	}
}

func TestOfflineNonHostSection(t *testing.T) {
	off := gs("a:1", "alpha", "", "claude", session.StatusIdle)
	off.Offline = true
	m := groupedModel(groupByAgent, off)
	out := ansi.Strip(paneView(m))
	if !strings.Contains(out, "\n"+glyphRobotSolid+" Claude  (offline)") {
		t.Fatalf("an all-offline section should be flagged:\n%s", out)
	}
}

func TestCycleGroupByKey(t *testing.T) {
	m := homeTestModel()
	m.statePath = filepath.Join(t.TempDir(), "tui.json")
	m.reorder()
	m = typeKeys(m, "zg")
	if m.groupBy != groupByProject || m.flash != "grouped by project" {
		t.Fatalf("groupBy = %q, flash = %q", m.groupBy, m.flash)
	}
	if g, _ := loadGroupBy(m.statePath); g != groupByProject {
		t.Fatalf("saved %q, want project", g)
	}
	for range 4 {
		m = typeKeys(m, "zg")
	}
	if m.groupBy != groupByHost {
		t.Fatalf("five presses should return to host, got %q", m.groupBy)
	}
}

func TestCycleGroupByKeepsSelection(t *testing.T) {
	m := homeTestModel() // n1:s1 and n1:s3 in /repo, n1:s2 in /repo-feat
	m.reorder()
	m = typeKeys(m, "j") // n1:s2
	if m.rootSessionID() != "n1:s2" {
		t.Fatalf("setup: selected %q", m.rootSessionID())
	}
	m = typeKeys(m, "zgzg") // workspace: s1, s3, then s2
	if m.groupBy != groupByWorkspace || m.rootSessionID() != "n1:s2" {
		t.Fatalf("groupBy = %q, selected %q, order %q", m.groupBy, m.rootSessionID(), m.order)
	}
}

func TestReorderKeepsSelectionWhenSectionsShift(t *testing.T) {
	m := groupedModel(groupByStatus,
		gs("a:1", "alpha", "", "", session.StatusIdle),
		gs("a:2", "alpha", "", "", session.StatusIdle),
		gs("a:3", "alpha", "", "", session.StatusWorking),
	) // working a:3, then idle a:1, a:2
	m.main = m.main.replaceAt(0, homeComp{cursor: 1})
	if m.rootSessionID() != "a:1" {
		t.Fatalf("setup: selected %q in %q", m.rootSessionID(), m.order)
	}
	s := m.sessions["a:3"]
	s.Status = session.StatusIdle
	m.sessions["a:3"] = s
	m.reorder() // idle a:1, a:2, a:3: a:1 moves up a row
	if m.rootSessionID() != "a:1" {
		t.Fatalf("selection moved to %q, order %q", m.rootSessionID(), m.order)
	}
}

func TestSectionHeaderIcons(t *testing.T) {
	idle := session.StatusIdle
	cases := []struct {
		name     string
		by       groupBy
		sessions []session.Session
		want     []string
	}{
		{"needs you", groupByHost, []session.Session{gs("s1", "", "", "", session.StatusAwaitingInput)},
			[]string{statusGlyph(session.StatusAwaitingInput) + " Needs you"}},
		{"project and other", groupByProject, []session.Session{gs("s1", "", "a:w1", "", idle), gs("s2", "", "", "", idle)},
			[]string{Icon.Repo.Glyph + " argus", Icon.Repo.Glyph + " other"}},
		{"main workspace and worktree", groupByWorkspace, []session.Session{gs("s1", "", "a:w1", "", idle), gs("s2", "", "a:w2", "", idle)},
			[]string{Icon.Folder.Glyph + " argus / argus", Icon.Branch.Glyph + " argus / argus-feat"}},
		{"agent and other", groupByAgent, []session.Session{gs("s1", "", "", "claude", idle), gs("s2", "", "", "", idle)},
			[]string{glyphRobotSolid + " Claude", glyphRobotSolid + " other"}},
		{"status", groupByStatus, []session.Session{gs("s1", "", "", "", idle), gs("s2", "", "", "", session.StatusWorking)},
			[]string{statusGlyph(session.StatusWorking) + " working", statusGlyph(idle) + " idle"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := ansi.Strip(paneView(groupedModel(c.by, c.sessions...)))
			for _, h := range c.want {
				if !strings.Contains(out, "\n"+h) {
					t.Errorf("header %q missing:\n%s", h, out)
				}
			}
		})
	}
}
