package tui

import (
	"image"

	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

type region int

const (
	regTree region = iota
	regTreeDivider
	regMain
	regDock
	regRight
	regFilesDivider
	regPopup
	regTitle
)

func (r region) container() (container, bool) {
	switch r {
	case regTree:
		return leftSidebar, true
	case regMain:
		return mainPane, true
	case regDock:
		return sessionDock, true
	case regRight:
		return rightSidebar, true
	}
	return 0, false
}

type hitKind int

const (
	hitRow hitKind = iota
	hitTab
	hitFold
	hitHeader
	hitClose
)

type hitTarget struct {
	kind  hitKind
	index int
}

// rowSpan is the lines [top, bottom) of item index.
type rowSpan struct{ index, top, bottom int }

type hitZone struct {
	rect   uv.Rectangle
	target hitTarget
}

// hitArea rows and zones are relative to rect.Min.
type hitArea struct {
	region region
	rect   uv.Rectangle
	rows   []rowSpan
	zones  []hitZone
}

func (a *hitArea) target(x, y int) (hitTarget, bool) {
	p := image.Pt(x, y).Sub(a.rect.Min)
	for i := len(a.zones) - 1; i >= 0; i-- {
		if p.In(a.zones[i].rect) {
			return a.zones[i].target, true
		}
	}
	for _, s := range a.rows {
		if p.Y >= s.top && p.Y < s.bottom {
			return hitTarget{kind: hitRow, index: s.index}, true
		}
	}
	return hitTarget{}, false
}

// hitMap is rebuilt on every render, so a click acts on what the user saw.
type hitMap struct{ areas []*hitArea }

func (h *hitMap) reset() {
	if h != nil {
		h.areas = h.areas[:0]
	}
}

func (h *hitMap) add(r region, rect uv.Rectangle) *hitArea {
	if h == nil {
		return nil
	}
	a := &hitArea{region: r, rect: rect}
	h.areas = append(h.areas, a)
	return a
}

// at: a later area covers an earlier one.
func (h *hitMap) at(x, y int) (*hitArea, bool) {
	if h == nil {
		return nil, false
	}
	p := image.Pt(x, y)
	for i := len(h.areas) - 1; i >= 0; i-- {
		if p.In(h.areas[i].rect) {
			return h.areas[i], true
		}
	}
	return nil, false
}

func (c *ctx) recording() bool { return c.area != nil }

func (c *ctx) below(dy int) *ctx { return &ctx{m: c.m, area: c.area, dx: c.dx, dy: c.dy + dy} }

func (c *ctx) right(dx int) *ctx { return &ctx{m: c.m, area: c.area, dx: c.dx + dx, dy: c.dy} }

func (c *ctx) hitRows(spans ...rowSpan) {
	if c.area == nil {
		return
	}
	for _, s := range spans {
		c.area.rows = append(c.area.rows, rowSpan{index: s.index, top: s.top + c.dy, bottom: s.bottom + c.dy})
	}
}

func (c *ctx) hitZone(r uv.Rectangle, t hitTarget) {
	if c.area != nil {
		c.area.zones = append(c.area.zones, hitZone{rect: r.Add(image.Pt(c.dx, c.dy)), target: t})
	}
}

func (c *ctx) hitTabs(x, y, sep int, labels ...string) {
	for i, l := range labels {
		w := lipgloss.Width(l)
		c.hitZone(uv.Rect(x, y, w, 1), hitTarget{kind: hitTab, index: i})
		x += w + sep
	}
}

// hitRect is for a popup that centers itself while it draws.
func (c *ctx) hitRect(r uv.Rectangle) {
	if c.area != nil {
		c.area.rect = r
	}
}

// hitStarts records the rows of items that start at first[i] in total lines,
// of which lines [scroll, end) show.
func hitStarts(c *ctx, first []int, total, scroll, end int) {
	for i := range first {
		s, e := chunkSpan(i, first, total)
		if top, bottom := max(s, scroll), min(e, end); top < bottom {
			c.hitRows(rowSpan{index: i, top: top - scroll, bottom: bottom - scroll})
		}
	}
}
