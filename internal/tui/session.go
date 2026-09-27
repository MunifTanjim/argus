package tui

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// A live transcript is a history region over a prompt dock that shows only
// while an interaction is pending.

// historyFocused reports whether the history region (not the dock) holds focus.
func (m model) historyFocused() bool {
	return m.focused != sessionDock
}

// historyBody renders the top region (transcript or detail sub-view).
func (m tview) historyBody() string {
	if m.historyView == histDetail {
		return m.detailBody()
	}
	return m.transcriptBody()
}

// sessionLayout returns the history-region and dock heights. dockH is 0 with no
// pending interaction; an unfocused dock collapses to rule + summary line
// (dockH == 2); only the focused dock expands to the full option panel.
func (m model) sessionLayout() (historyH, dockH int) {
	avail := m.sessionRows()
	if m.sessionInteraction() == nil {
		return avail, 0
	}
	dockH = m.dock.dockHeight(&ctx{m: &m}, avail)
	return max(1, avail-dockH), dockH
}

// sessionRows is the height the history region and the dock share: the body
// less the header, the blank under it, and the footer rows.
func (m model) sessionRows() int { return max(1, m.bodyHeight()-4) }

// dockHeight is the dock's height out of avail rows, its rule included.
func (d dockComp) dockHeight(c *ctx, avail int) int {
	if c.m.focused != sessionDock {
		return 2
	}
	return min(max(d.dockContentLines(c)+1, 3), avail-1)
}

// dockSummary is the one-line description shown in the collapsed dock.
func dockSummary(ix *session.Interaction) string {
	switch ix.Kind {
	case session.InteractionQuestion:
		if len(ix.Questions) > 1 {
			return fmt.Sprintf("%d questions", len(ix.Questions))
		}
		if len(ix.Questions) == 1 && ix.Questions[0].Question != "" {
			return ix.Questions[0].Question
		}
		return "Question"
	case session.InteractionPermission:
		if ix.ToolName != "" {
			return "Allow " + toolDisplayName(ix.ToolName) + "?"
		}
		return "Permission request"
	case session.InteractionPlan:
		return "Review plan"
	default: // idle
		if ix.Message != "" {
			return ix.Message
		}
		return "Waiting for input"
	}
}

// dockSummaryLine renders the collapsed dock body: accent marker + summary left,
// dim "Tab to answer" hint right.
func (m model) dockSummaryLine(width int) string {
	ix := m.interaction()
	if ix == nil {
		return ""
	}
	hint := StyleDim.Render("⇥ Tab to answer")
	leftW := max(1, width-lipgloss.Width(hint)-1)
	left := Icon.Collapsed.WithColor(ColorAccent) + " " + dockSummary(ix)
	leftBlock := lipgloss.NewStyle().Width(leftW).Render(truncateLine(left, leftW))
	return lipgloss.JoinHorizontal(lipgloss.Top, leftBlock, " ", hint)
}

// dockContentWidth is the dock body width after horizontal padding (contentPadX),
// keeping the dock body aligned with the transcript cards above.
func (m model) dockContentWidth() int { return max(1, m.containerWidth()-2*contentPadX) }

// dockWidths splits the dock into an option-list column and a preview column.
// side is false (single full-width column) when there's no preview or the
// terminal is too narrow to split.
func (d dockComp) dockWidths(c *ctx) (leftW, rightW int, side bool) {
	W := c.m.dockContentWidth()
	if d.focusedOptionPreview(c) == "" {
		return W, 0, false
	}
	leftW = W * 2 / 5
	rightW = W - leftW - 1 // 1-column gap
	if leftW < 24 || rightW < 24 {
		return W, 0, false
	}
	return leftW, rightW, true
}

// dockContentLines is the unclamped line count of the dock body: the option
// list, plus any preview (beside it = taller column; stacked = sum).
func (d dockComp) dockContentLines(c *ctx) int {
	preview := d.focusedOptionPreview(c)
	leftW, _, side := d.dockWidths(c)
	leftLines, _, _ := d.promptLinesWidth(c, leftW)
	if preview == "" {
		return len(leftLines)
	}
	previewLines := strings.Count(preview, "\n") + 1 + 2 // + border rows
	if side {
		return max(len(leftLines), previewLines)
	}
	return len(leftLines) + previewLines // stacked
}

// dockBody composes the dock body within height rows: a single option column,
// or options + boxed preview (side-by-side, or stacked when too narrow). The
// option column is windowed around its active control so controls stay visible.
func (d dockComp) dockBody(c *ctx, height int) string {
	preview := d.focusedOptionPreview(c)
	leftW, rightW, side := d.dockWidths(c)
	lines, anchor, ctrlStart := d.promptLinesWidth(c, leftW)
	left := d.dockScrollBody(lines, height, anchor, ctrlStart, leftW)

	switch {
	case preview == "":
		return left
	case side:
		right := previewBox(preview, rightW, height)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	default: // too narrow: stack a compact preview box under the options
		stacked := strings.Join(lines, "\n") + "\n" + previewBox(preview, leftW, max(3, height/2))
		return windowLines(stacked, height, anchor)
	}
}

// splitDock divides the dock lines into a scrollable body (above the controls)
// and the pinned control block, for a dock of totalH rows. ok is false when the
// content all fits, or the controls alone fill the dock (no room to pin and
// scroll) — callers then fall back to the non-scrolling windowLines path.
func splitDock(lines []string, totalH, ctrlStart int) (body, ctrl []string, ok bool) {
	if totalH <= 0 || len(lines) <= totalH {
		return nil, nil, false
	}
	if ctrlStart < 0 || ctrlStart > len(lines) {
		ctrlStart = len(lines)
	}
	ctrl = lines[ctrlStart:]
	// Empty controls = nothing to pin; controls filling the dock = nothing left to
	// scroll. Either way, fall through to the non-scrolling path.
	if len(ctrl) == 0 || len(ctrl) >= totalH {
		return nil, nil, false
	}
	return lines[:ctrlStart], ctrl, true
}

// dockGeom resolves splitDock plus the visible body height: bodyH is the rows
// available to the scrolling body, one fewer than the region when a scroll-hint
// row is reserved (region >= 2). Shared by rendering and scroll-offset math so
// the two can't drift. ok mirrors splitDock (false = body doesn't scroll).
func dockGeom(lines []string, totalH, ctrlStart int) (body, ctrl []string, bodyH int, ok bool) {
	body, ctrl, ok = splitDock(lines, totalH, ctrlStart)
	if !ok {
		return nil, nil, 0, false
	}
	region := totalH - len(ctrl) // >= 1 (splitDock guarantees len(ctrl) < totalH)
	bodyH = region
	if region >= 2 {
		bodyH = region - 1 // reserve a row for the scroll hint
	}
	return body, ctrl, bodyH, true
}

// dockScrollBody renders the focused dock within height rows: the control block
// (options / reply field) pins to the bottom while the body above scrolls by
// d.scroll, with a ▲/▼ overflow hint. When the body fits it's returned
// whole; when the controls themselves overflow it falls back to anchor windowing.
func (d dockComp) dockScrollBody(lines []string, height, anchor, ctrlStart, width int) string {
	body, ctrl, bodyH, ok := dockGeom(lines, height, ctrlStart)
	if !ok {
		if len(lines) <= height {
			return strings.Join(lines, "\n")
		}
		return windowLines(strings.Join(lines, "\n"), height, anchor)
	}
	scroll := max(0, min(d.scroll, len(body)-bodyH))
	end := scroll + bodyH
	out := strings.Join(body[scroll:end], "\n")
	if bodyH < height-len(ctrl) { // a hint row was reserved
		out += "\n" + scrollHint(scroll, len(body)-end, width)
	}
	return out + "\n" + strings.Join(ctrl, "\n")
}

// dockScrollGeom returns the max scroll offset and the half-page step for the
// focused dock body at the given total height (0 / 1 when the body doesn't scroll).
func (d dockComp) dockScrollGeom(c *ctx, height int) (maxScroll, page int) {
	leftW, _, _ := d.dockWidths(c)
	lines, _, ctrlStart := d.promptLinesWidth(c, leftW)
	body, _, bodyH, ok := dockGeom(lines, height, ctrlStart)
	if !ok {
		return 0, 1
	}
	return max(0, len(body)-bodyH), max(1, bodyH/2)
}

// dockScrolls reports whether the focused dock body currently overflows (so the
// footer should advertise the scroll keys).
func (d dockComp) dockScrolls(c *ctx) bool {
	if c.m.focused != sessionDock || c.m.sessionInteraction() == nil {
		return false
	}
	maxScroll, _ := d.dockScrollGeom(c, d.dockHeight(c, c.m.sessionRows())-1)
	return maxScroll > 0
}

// scrollDock moves the dock body scroll offset by dir half-pages, clamped.
func (d dockComp) scrollDock(c *ctx, dir int) dockComp {
	maxScroll, page := d.dockScrollGeom(c, d.dockHeight(c, c.m.sessionRows())-1)
	cur := min(d.scroll, maxScroll) // re-clamp first: a resize may have shrunk the body
	d.scroll = max(0, min(cur+dir*page, maxScroll))
	return d
}

// windowLines returns at most height lines from s, scrolled so the anchor stays
// visible: keeps the top (and heading) while the anchor fits, else slides down to
// make the anchor the last visible line. Keeps controls on screen when context is tall.
func windowLines(s string, height, anchor int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= height {
		return s
	}
	offset := 0
	if anchor >= height {
		offset = anchor - height + 1
	}
	if maxOffset := len(lines) - height; offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	return strings.Join(lines[offset:offset+height], "\n")
}

// sessionHeader is the header over session s's live transcript, centered in a
// main pane w wide, with its title in style title.
func sessionHeader(s session.Session, w int, title lipgloss.Style) string {
	name := s.Name
	if name == "" {
		name = s.Tmux.SessionName
	}
	var parts []string
	if s.Repo != "" {
		parts = append(parts, s.Repo)
	}
	if name != "" {
		parts = append(parts, name)
	}
	header := title.Render(strings.Join(parts, " · "))
	if s.Branch != "" {
		branch := Icon.Branch.Render() + lipgloss.NewStyle().Foreground(ColorGitBranch).Render(" "+s.Branch)
		header += title.Render(" · ") + branch
	}
	header += dimStyle.Render(fmt.Sprintf("  [%s] %s", paneTag(s), statusWord(s)))
	return centerBlock(indentBlock(header, strings.Repeat(" ", contentPadX)), containerWidthOf(w), w)
}

// startingNotice renders the startup-gate message centered in place of the
// absent transcript.
func startingNotice(m model) string {
	lines := []string{
		"Session is starting or waiting at a startup prompt.",
		"Press " + m.keyText(sessionKeys.Raw) + " to open the live screen and continue.",
	}
	return m.center(dimStyle.Render(strings.Join(lines, "\n")), m.containerWidth())
}
