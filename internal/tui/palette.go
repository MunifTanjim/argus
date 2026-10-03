package tui

import (
	"slices"
	"strings"

	"github.com/sahilm/fuzzy"
)

// paletteKind is what a palette item stands for. The empty-query list shows
// the kinds in this order. paletteCommand stays last: boost counts down to it.
type paletteKind int

const (
	paletteSession paletteKind = iota
	paletteWorkspace
	paletteProject
	paletteNode
	paletteCommand
)

// paletteItem is one row of the palette. parent is the id of the item it is
// under; "" is the root. The first action runs on enter.
type paletteItem struct {
	id      string
	kind    paletteKind
	marker  string // styled glyph that starts the row
	label   string
	detail  string
	hint    string // dim text after the detail that matching ignores
	waiting bool   // a session that waits for input
	name    string // what an exact query equals; "" means the label
	parent  string
	pname   string // the parent's name, set by the snapshot
	actions []paletteAction
}

func (it paletteItem) filter() string { return it.label + " " + it.detail }

func (it paletteItem) exactName() string {
	if it.name != "" {
		return it.name
	}
	return it.label
}

// exact ranks a query that equals the item's name above one that equals its
// parent's name, and both above any fuzzy score.
func (it paletteItem) exact(q string) int {
	switch {
	case strings.EqualFold(q, it.exactName()), strings.EqualFold(q, it.label):
		return 2
	case it.pname != "" && strings.EqualFold(q, it.pname):
		return 1
	}
	return 0
}

// boost ranks the kinds: a session that waits for input first, commands last.
// One step is worth one separator match, so a clearly better match in a lower
// kind still comes first.
func (it paletteItem) boost() int {
	const step = 20
	b := step * int(paletteCommand-it.kind)
	if it.waiting {
		b += step
	}
	return b
}

// run queues its changes on c.
type paletteAction struct {
	name string
	run  func(c *ctx)
}

type paletteSource interface {
	items(m model) []paletteItem
}

// paletteMode is what the palette searches while the query starts with
// prefix. The mode with no prefix is the default.
type paletteMode struct {
	prefix      string
	title       string
	placeholder string
	sources     []paletteSource
	scoped      bool
}

func modeFor(modes []paletteMode, typed string) (int, string) {
	def := 0
	for i, md := range modes {
		if md.prefix == "" {
			def = i
			continue
		}
		if q, ok := strings.CutPrefix(typed, md.prefix); ok {
			return i, q
		}
	}
	return def, typed
}

// paletteSnapshot is a mode's items, taken one time when the palette enters
// the mode, in the empty-query order.
type paletteSnapshot struct {
	items []paletteItem
	index map[string]int
	kids  map[string]bool
}

func newPaletteSnapshot(items []paletteItem) paletteSnapshot {
	items = slices.Clone(items)
	slices.SortStableFunc(items, func(a, b paletteItem) int { return b.boost() - a.boost() })
	s := paletteSnapshot{items: items, index: map[string]int{}, kids: map[string]bool{}}
	for i, it := range items {
		s.index[it.id] = i
		if it.parent != "" {
			s.kids[it.parent] = true
		}
	}
	for i, it := range items {
		if p, ok := s.item(it.parent); ok {
			items[i].pname = p.exactName()
		}
	}
	return s
}

func takeSnapshot(m model, md paletteMode) paletteSnapshot {
	var items []paletteItem
	for _, src := range md.sources {
		items = append(items, src.items(m)...)
	}
	return newPaletteSnapshot(items)
}

func (s paletteSnapshot) item(id string) (paletteItem, bool) {
	i, ok := s.index[id]
	if !ok {
		return paletteItem{}, false
	}
	return s.items[i], true
}

func (s paletteSnapshot) hasKids(id string) bool { return s.kids[id] }

func (s paletteSnapshot) parent(scope string) string {
	it, _ := s.item(scope)
	return it.parent
}

// chain is scope and the scopes above it, root first, without the root.
func (s paletteSnapshot) chain(scope string) []paletteItem {
	var out []paletteItem
	for id := scope; id != ""; {
		it, ok := s.item(id)
		if !ok {
			break
		}
		out = append(out, it)
		id = it.parent
	}
	slices.Reverse(out)
	return out
}

// under is the items below scope at any depth, without scope itself.
func (s paletteSnapshot) under(scope string) []paletteItem {
	if scope == "" {
		return s.items
	}
	var out []paletteItem
	for _, it := range s.items {
		for p := it.parent; p != ""; p = s.parent(p) {
			if p == scope {
				out = append(out, it)
				break
			}
		}
	}
	return out
}

// paletteMatch is an item that matches the query, and the byte positions of
// the matched characters in its label and in its detail.
type paletteMatch struct {
	paletteItem
	labelHits, detailHits []int
}

type paletteFilters []paletteItem

func (f paletteFilters) String(i int) string { return f[i].filter() }
func (f paletteFilters) Len() int            { return len(f) }

// matchPalette fuzzy-matches items against q: exact matches first, then the
// best boosted score. An exact parent match shows even when the fuzzy match
// misses it: a worktree session's detail has the main repository's name, not
// its workspace's name.
func matchPalette(items []paletteItem, q string) []paletteMatch {
	q = strings.TrimSpace(q)
	if q == "" {
		out := make([]paletteMatch, len(items))
		for i, it := range items {
			out[i] = paletteMatch{paletteItem: it}
		}
		return out
	}
	found := fuzzy.FindFrom(q, paletteFilters(items))
	seen := make(map[int]bool, len(found))
	for _, f := range found {
		seen[f.Index] = true
	}
	for i, it := range items {
		if !seen[i] && it.exact(q) > 0 {
			found = append(found, fuzzy.Match{Index: i})
		}
	}
	slices.SortStableFunc(found, func(a, b fuzzy.Match) int {
		ia, ib := items[a.Index], items[b.Index]
		if ea, eb := ia.exact(q), ib.exact(q); ea != eb {
			return eb - ea
		}
		return b.Score + ib.boost() - a.Score - ia.boost()
	})
	out := make([]paletteMatch, len(found))
	for i, f := range found {
		it := items[f.Index]
		pm := paletteMatch{paletteItem: it}
		for _, h := range f.MatchedIndexes {
			switch {
			case h < len(it.label):
				pm.labelHits = append(pm.labelHits, h)
			case h > len(it.label):
				pm.detailHits = append(pm.detailHits, h-len(it.label)-1)
			}
		}
		out[i] = pm
	}
	return out
}
