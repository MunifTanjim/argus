package tui

import (
	"slices"
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func pi(id string, kind paletteKind, label, detail, parent string) paletteItem {
	return paletteItem{id: id, kind: kind, label: label, detail: detail, parent: parent}
}

func testSnap() paletteSnapshot {
	return newPaletteSnapshot([]paletteItem{
		pi("node:n1", paletteNode, "home", "node", ""),
		pi("project:p1", paletteProject, "argus", "home", "node:n1"),
		pi("ws:w1", paletteWorkspace, "repo main", "argus", "project:p1"),
		pi("session:s1", paletteSession, "fix-login", "repo", "ws:w1"),
		pi("session:s9", paletteSession, "loose", "", ""),
	})
}

func itemIDs(items []paletteItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out
}

func matchIDs(ms []paletteMatch) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.id
	}
	return out
}

func TestPaletteSnapshotOrdersByKind(t *testing.T) {
	got := itemIDs(testSnap().items)
	want := []string{"session:s1", "session:s9", "ws:w1", "project:p1", "node:n1"}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestPaletteUnderScope(t *testing.T) {
	s := testSnap()
	cases := []struct {
		scope string
		want  []string
	}{
		{"", []string{"session:s1", "session:s9", "ws:w1", "project:p1", "node:n1"}},
		{"node:n1", []string{"session:s1", "ws:w1", "project:p1"}},
		{"project:p1", []string{"session:s1", "ws:w1"}},
		{"ws:w1", []string{"session:s1"}},
		{"session:s1", nil},
		{"ws:missing", nil},
	}
	for _, c := range cases {
		if got := itemIDs(s.under(c.scope)); !slices.Equal(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("under(%q) = %v, want %v", c.scope, got, c.want)
		}
	}
}

func TestPaletteChainParentAndKids(t *testing.T) {
	s := testSnap()
	var labels []string
	for _, it := range s.chain("ws:w1") {
		labels = append(labels, it.label)
	}
	if want := []string{"home", "argus", "repo main"}; !slices.Equal(labels, want) {
		t.Errorf("chain(ws:w1) = %v, want %v", labels, want)
	}
	if got := s.chain(""); len(got) != 0 {
		t.Errorf("chain(root) = %v, want none", got)
	}
	if got := s.parent("ws:w1"); got != "project:p1" {
		t.Errorf("parent(ws:w1) = %q", got)
	}
	if got := s.parent("node:n1"); got != "" {
		t.Errorf("parent(node:n1) = %q, want the root", got)
	}
	if !s.hasKids("ws:w1") || s.hasKids("session:s1") {
		t.Error("ws:w1 has a session under it; session:s1 has nothing")
	}
}

func TestModeForPicksThePrefix(t *testing.T) {
	modes := []paletteMode{{prefix: ">"}, {prefix: ""}}
	if i, q := modeFor(modes, ">open"); i != 0 || q != "open" {
		t.Errorf("modeFor(>open) = %d %q, want 0 \"open\"", i, q)
	}
	if i, q := modeFor(modes, "fix"); i != 1 || q != "fix" {
		t.Errorf("modeFor(fix) = %d %q, want the default mode 1", i, q)
	}
}

func TestMatchPaletteSplitsHits(t *testing.T) {
	ms := matchPalette([]paletteItem{pi("session:s1", paletteSession, "fix-login", "repo", "")}, "fxr")
	if len(ms) != 1 {
		t.Fatalf("matches = %v, want one", matchIDs(ms))
	}
	if !slices.Equal(ms[0].labelHits, []int{0, 2}) || !slices.Equal(ms[0].detailHits, []int{0}) {
		t.Errorf("hits = label %v detail %v, want label [0 2] detail [0]", ms[0].labelHits, ms[0].detailHits)
	}
}

func TestMatchPaletteKeepsOrder(t *testing.T) {
	items := []paletteItem{
		pi("a", paletteSession, "same", "", ""),
		pi("b", paletteSession, "same", "", ""),
		pi("c", paletteSession, "other", "", ""),
	}
	if got := matchIDs(matchPalette(items, "")); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("empty query = %v, want every item in order", got)
	}
	if got := matchIDs(matchPalette(items, "same")); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("equal scores = %v, want [a b]", got)
	}
	if got := matchPalette(items, "zzz"); len(got) != 0 {
		t.Errorf("no match = %v", matchIDs(got))
	}
}

func TestMatchPaletteIgnoresTheHint(t *testing.T) {
	it := pi("command:x", paletteCommand, "abc", "", "")
	it.hint = "zz"
	if got := matchPalette([]paletteItem{it}, "zz"); len(got) != 0 {
		t.Errorf("the hint matched: %v", matchIDs(got))
	}
}

func TestPaletteSnapshotPutsWaitingSessionsFirst(t *testing.T) {
	waiting := pi("session:s2", paletteSession, "deploy", "", "")
	waiting.waiting = true
	s := newPaletteSnapshot([]paletteItem{pi("session:s1", paletteSession, "fix-login", "", ""), waiting})
	if got := itemIDs(s.items); !slices.Equal(got, []string{"session:s2", "session:s1"}) {
		t.Fatalf("order = %v, want the waiting session first", got)
	}
}

func TestMatchPaletteBoostsByKind(t *testing.T) {
	waiting := pi("session:wait", paletteSession, "fix-login", "repo", "")
	waiting.waiting = true
	cases := []struct {
		name  string
		items []paletteItem
		q     string
		want  []string
	}{
		{"a waiting session over a closer session match", []paletteItem{
			pi("session:idle", paletteSession, "login-page", "repo", ""), waiting,
		}, "login", []string{"session:wait", "session:idle"}},
		{"a session over a closer workspace match", []paletteItem{
			pi("ws:w1", paletteWorkspace, "repo main", "argus", ""),
			pi("session:s1", paletteSession, "fix-login", "repo · main", ""),
		}, "repo", []string{"session:s1", "ws:w1"}},
		{"a much closer workspace match over a session", []paletteItem{
			pi("session:s1", paletteSession, "lo go in", "", ""),
			pi("ws:w1", paletteWorkspace, "login", "", ""),
		}, "login", []string{"ws:w1", "session:s1"}},
	}
	for _, c := range cases {
		if got := matchIDs(matchPalette(c.items, c.q)); !slices.Equal(got, c.want) {
			t.Errorf("%s: %q = %v, want %v", c.name, c.q, got, c.want)
		}
	}
}

func TestMatchPaletteRanksExactMatchesFirst(t *testing.T) {
	s := newPaletteSnapshot([]paletteItem{
		pi("project:p1", paletteProject, "argus", "home", ""),
		{id: "ws:w1", kind: paletteWorkspace, label: "repo main", name: "repo", detail: "argus", parent: "project:p1"},
		pi("session:s1", paletteSession, "repo-sync", "repo · main", "ws:w1"),
		pi("session:s2", paletteSession, "fix-login", "repo · main", "ws:w1"),
		pi("command:x", paletteCommand, "Repo", "", ""),
	})
	cases := []struct {
		q    string
		want []string
	}{
		{"repo", []string{"ws:w1", "command:x", "session:s1", "session:s2"}},
		{"ARGUS", []string{"project:p1", "ws:w1"}},
	}
	for _, c := range cases {
		if got := matchIDs(matchPalette(s.items, c.q)); !slices.Equal(got, c.want) {
			t.Errorf("%q = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestMatchPaletteExactMatchEdgeCases(t *testing.T) {
	s := newPaletteSnapshot([]paletteItem{
		pi("project:p1", paletteProject, "argus", "home", ""),
		{id: "ws:w1", kind: paletteWorkspace, label: "repo main", name: "repo", detail: "argus", parent: "project:p1"},
		{id: "ws:w2", kind: paletteWorkspace, label: "argus-feat feat", name: "argus-feat", detail: "argus", parent: "project:p1"},
		pi("session:s1", paletteSession, "fix-login", "repo · main", "ws:w1"),
		pi("session:wt", paletteSession, "deploy", "argus · feat", "ws:w2"),
		pi("session:orphan", paletteSession, "loose", "", "ws:gone"),
	})
	cases := []struct {
		name  string
		items []paletteItem
		q     string
		want  []string
	}{
		{"a worktree session that the fuzzy match misses", s.items, "argus-feat", []string{"ws:w2", "session:wt"}},
		{"the full label", s.items, "repo main", []string{"ws:w1", "session:s1"}},
		{"spaces around the query", s.items, "  ARGUS  ", []string{"project:p1", "ws:w1", "ws:w2", "session:wt"}},
		{"inside a scope", s.under("ws:w1"), "repo", []string{"session:s1"}},
		{"a parent missing from the snapshot", s.items, "gone", nil},
	}
	for _, c := range cases {
		if got := matchIDs(matchPalette(c.items, c.q)); !slices.Equal(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("%s: %q = %v, want %v", c.name, c.q, got, c.want)
		}
	}
}

func TestOfflineSessionIsNotWaiting(t *testing.T) {
	m := homeTestModel()
	s := m.sessions["n1:s1"]
	s.Status, s.Offline = session.StatusAwaitingInput, true
	m.sessions["n1:s1"] = s
	for _, it := range (sessionsSource{}).items(m) {
		if it.id == "session:n1:s1" && it.waiting {
			t.Error("an offline session must not rank as waiting")
		}
	}
}
