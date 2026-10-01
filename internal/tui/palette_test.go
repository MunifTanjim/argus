package tui

import (
	"slices"
	"testing"
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
