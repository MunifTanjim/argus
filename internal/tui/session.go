package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// The session screen: a history region (transcript or detail drill-down) plus a
// prompt dock shown only while an interaction is pending. Tab moves focus; each
// pane keeps its own keys.

// handleSessionKey routes keys on the session screen by focus.
func (m model) handleSessionKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pending := m.sessionInteraction() != nil
	if !pending && m.focus == focusDock {
		m.focus = focusHistory // dock vanished; reclaim focus
	}

	switch {
	case key.Matches(msg, sessionKeys.Focus):
		if pending {
			if m.focus == focusHistory {
				m.focus = focusDock
			} else {
				m.focus = focusHistory
			}
		}
		return m, nil
	case key.Matches(msg, sessionKeys.Raw):
		s := m.sessions[m.selectedID]
		if !s.Controllable() {
			m.flash = string(s.Frontend) + " session: terminal control unavailable"
			return m, nil
		}
		return m.enterScreen(m.selectedID)
	}

	if m.focus == focusDock {
		// Read returns to reading; prompt stays pending.
		if key.Matches(msg, promptKeys.Read) {
			m.focus = focusHistory
			return m, nil
		}
		return m.handlePromptKey(msg)
	}

	// focus == history
	if key.Matches(msg, transcriptKeys.Back) {
		if m.historyView == histDetail {
			// A frame with a subID owns a subagent subscription: tear it down and
			// restore the stashed session stream. A leaf frame above a subagent frame
			// has empty subID and must pop normally first so the subagent frame isn't
			// torn down prematurely.
			if f := m.topFrame(); f != nil && f.subID != "" {
				cmd := m.unsubscribeCmd(f.subID)
				m.activeSub = m.sessionSub
				m.sessionSub = subRef{}
				// Re-subscribe to catch deltas missed while drilled in.
				have := len(m.transcriptCache[m.activeSub.key()].chunks)
				m.popDetail()
				return m, tea.Batch(cmd, m.subscribeCmd(m.activeSub, have))
			}
			if m.popDetail() { // popped the root → back to the card list
				m.historyView = histTranscript
			}
			return m, nil
		}
		var cmd tea.Cmd
		if m.activeSub.subID != "" {
			cmd = m.unsubscribeCmd(m.activeSub.subID)
			m.activeSub = subRef{}
		}
		m.sessionSub = subRef{} // clear stashed drill state on exit
		m.mode = modeList
		return m, cmd
	}
	if m.historyView == histDetail {
		return m.handleDetailKey(msg)
	}
	return m.handleTranscriptKey(msg)
}

// historyFocused reports whether the history region (not the dock) holds focus.
func (m model) historyFocused() bool {
	return m.focus == focusHistory
}

// sessionFooter is the key-hint line, varying by focused region and sub-view.
func (m model) sessionFooter() string {
	switch {
	case m.focus == focusDock:
		multi := m.isMultiQuestion()
		binds := []key.Binding{promptKeys.Up}
		if multi {
			binds = append(binds, promptKeys.TabPrev, promptKeys.Next)
		} else {
			binds = append(binds, promptKeys.Submit)
		}
		if m.dockScrolls() {
			binds = append(binds, promptKeys.HalfUp)
		}
		binds = append(binds, promptKeys.Read)
		if !multi {
			binds = append(binds, sessionKeys.Raw)
		}
		return m.footer(binds...)
	case m.historyView == histDetail:
		return m.footer(detailKeys.Up, detailKeys.Fold, detailKeys.Drill, detailKeys.Back, detailKeys.Raw)
	default:
		binds := []key.Binding{transcriptKeys.ScrollUp, transcriptKeys.TurnNext, transcriptKeys.Fold,
			transcriptKeys.Detail, transcriptKeys.Bottom, transcriptKeys.Back}
		if m.sessionInteraction() != nil {
			binds = append(binds, transcriptKeys.Answer)
		}
		return m.footer(binds...)
	}
}

// historyBody renders the top region (transcript or detail sub-view).
func (m model) historyBody() string {
	if m.historyView == histDetail {
		return m.detailBody()
	}
	return m.transcriptBody()
}

// sessionLayout returns the history-region and dock heights. dockH is 0 with no
// pending interaction; an unfocused dock collapses to rule + summary line
// (dockH == 2); only the focused dock expands to the full option panel.
// Chrome sessionView always draws: header(1) + 2 body-surrounding blanks +
// footer(1) = 4. The history/dock rule is part of dockH, not chrome.
func (m model) sessionLayout() (historyH, dockH int) {
	if m.sessionInteraction() == nil {
		return max(1, m.height-4), 0
	}
	avail := max(1, m.height-4) // header + 2 surrounding blanks + footer
	if m.focus != focusDock {
		return max(1, avail-2), 2
	}
	capH := avail - 1                         // focused: expand to fit, history keeps ≥1 line
	dockH = min(m.dockContentLines()+1, capH) // +1 for the focus rule
	if dockH < 3 {
		dockH = 3
	}
	if dockH > avail-1 {
		dockH = avail - 1
	}
	return max(1, avail-dockH), dockH
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
func (m model) dockWidths() (leftW, rightW int, side bool) {
	W := m.dockContentWidth()
	if m.focusedOptionPreview() == "" {
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
func (m model) dockContentLines() int {
	preview := m.focusedOptionPreview()
	leftW, _, side := m.dockWidths()
	leftLines, _, _ := m.promptLinesWidth(leftW)
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
func (m model) dockBody(height int) string {
	preview := m.focusedOptionPreview()
	leftW, rightW, side := m.dockWidths()
	lines, anchor, ctrlStart := m.promptLinesWidth(leftW)
	left := m.dockScrollBody(lines, height, anchor, ctrlStart, leftW)

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
// m.prompt.scroll, with a ▲/▼ overflow hint. When the body fits it's returned
// whole; when the controls themselves overflow it falls back to anchor windowing.
func (m model) dockScrollBody(lines []string, height, anchor, ctrlStart, width int) string {
	body, ctrl, bodyH, ok := dockGeom(lines, height, ctrlStart)
	if !ok {
		if len(lines) <= height {
			return strings.Join(lines, "\n")
		}
		return windowLines(strings.Join(lines, "\n"), height, anchor)
	}
	scroll := max(0, min(m.prompt.scroll, len(body)-bodyH))
	end := scroll + bodyH
	out := strings.Join(body[scroll:end], "\n")
	if bodyH < height-len(ctrl) { // a hint row was reserved
		out += "\n" + scrollHint(scroll, len(body)-end, width)
	}
	return out + "\n" + strings.Join(ctrl, "\n")
}

// dockScrollGeom returns the max scroll offset and the half-page step for the
// focused dock body at the given total height (0 / 1 when the body doesn't scroll).
func (m model) dockScrollGeom(height int) (maxScroll, page int) {
	leftW, _, _ := m.dockWidths()
	lines, _, ctrlStart := m.promptLinesWidth(leftW)
	body, _, bodyH, ok := dockGeom(lines, height, ctrlStart)
	if !ok {
		return 0, 1
	}
	return max(0, len(body)-bodyH), max(1, bodyH/2)
}

// dockScrolls reports whether the focused dock body currently overflows (so the
// footer should advertise the scroll keys).
func (m model) dockScrolls() bool {
	if m.focus != focusDock || m.sessionInteraction() == nil {
		return false
	}
	_, dockH := m.sessionLayout()
	maxScroll, _ := m.dockScrollGeom(dockH - 1)
	return maxScroll > 0
}

// scrollDock moves the dock body scroll offset by dir half-pages, clamped.
func (m model) scrollDock(dir int) model {
	_, dockH := m.sessionLayout()
	maxScroll, page := m.dockScrollGeom(dockH - 1)
	cur := min(m.prompt.scroll, maxScroll) // re-clamp first: a resize may have shrunk the body
	m.prompt.scroll = max(0, min(cur+dir*page, maxScroll))
	return m
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

// sessionView composes the session screen: header, history region, and a
// conditional prompt dock separated by a rule that turns accent-colored while the
// dock holds focus.
func (m model) sessionView() string {
	s := m.sessions[m.selectedID]
	name := s.Name
	if name == "" {
		name = s.Tmux.SessionName
	}
	parts := []string{"argus"}
	if s.Repo != "" {
		parts = append(parts, s.Repo)
	}
	if name != "" {
		parts = append(parts, name)
	}
	header := headerStyle.Render(strings.Join(parts, " · ")) +
		dimStyle.Render(fmt.Sprintf("  [%s] %s", paneTag(s), statusWord(s)))
	header = centerBlock(indentBlock(header, strings.Repeat(" ", contentPadX)), m.containerWidth(), m.width)

	body := m.historyBody()
	if m.sessionInteraction() != nil {
		_, dockH := m.sessionLayout()
		ruleColor := ColorBorder
		if m.focus == focusDock {
			ruleColor = ColorAccent
		}
		rule := lipgloss.NewStyle().Foreground(ruleColor).
			Render(strings.Repeat("─", m.containerWidth()))
		// Rule takes one dock line; focused gets the windowed body, unfocused a
		// single summary line. Body is inset from the rule by contentPadX; centered
		// to align with the transcript above.
		dockBody := m.dockSummaryLine(m.dockContentWidth())
		if m.focus == focusDock {
			dockBody = m.dockBody(dockH - 1)
		}
		dock := rule + "\n" + indentBlock(dockBody, strings.Repeat(" ", contentPadX))
		body = body + "\n" + centerBlock(dock, m.containerWidth(), m.width)
	}
	return pinFooter(header+"\n\n"+body, m.sessionFooter(), m.width, m.height)
}
