package tui

import (
	"slices"
	"strings"

	"github.com/sahilm/fuzzy"
)

// paletteKind is what a palette item stands for. The empty-query list shows
// the kinds in this order.
type paletteKind int

const (
	paletteSession paletteKind = iota
	paletteWorkspace
	paletteProject
	paletteNode
)

// paletteItem is one row of the palette. parent is the id of the item it is
// under; "" is the root. The first action runs on enter.
type paletteItem struct {
	id      string
	kind    paletteKind
	marker  string // styled glyph that starts the row
	label   string
	detail  string
	parent  string
	actions []paletteAction
}

func (it paletteItem) filter() string { return it.label + " " + it.detail }

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
	slices.SortStableFunc(items, func(a, b paletteItem) int { return int(a.kind) - int(b.kind) })
	s := paletteSnapshot{items: items, index: map[string]int{}, kids: map[string]bool{}}
	for i, it := range items {
		s.index[it.id] = i
		if it.parent != "" {
			s.kids[it.parent] = true
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

// matchPalette fuzzy-matches items against q, best match first. Equal scores
// keep the order of items.
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
