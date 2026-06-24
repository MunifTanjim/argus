package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// The session screen is a composite view: a full-height history region (the
// transcript card list, or a chunk's detail drill-down) plus a conditional
// prompt dock that appears only while the session has a pending interaction.
// Focus moves between the two panes with Tab; each pane keeps its own keys, and
// a single footer + the dock's rule color reflect which pane is focused.

// handleSessionKey routes keys on the composite session screen by focus.
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
		m.mode = modeScreen
		m.screen, m.screenErr = "", nil
		return m, tea.Batch(m.fetchCapture(m.selectedID), screenTickCmd())
	}

	if m.focus == focusDock {
		// tab is consumed above; only esc reaches here via Read → back to reading.
		if key.Matches(msg, promptKeys.Read) {
			m.focus = focusHistory // return to reading; prompt stays pending
			return m, nil
		}
		return m.handlePromptKey(msg)
	}

	// focus == history
	if key.Matches(msg, transcriptKeys.Back) {
		if m.historyView == histDetail {
			// If the frame being popped owns a subagent subscription (identified by
			// its subID field), tear it down and restore the stashed session stream.
			// A leaf frame above a subagent frame has an empty subID and must pop
			// normally first so the subagent frame is NOT prematurely torn down.
			if f := m.topFrame(); f != nil && f.subID != "" {
				cmd := m.unsubscribeCmd(f.subID)
				m.activeSub = m.sessionSub // restore the original session subRef
				m.sessionSub = subRef{}    // clear the stash
				// Re-subscribe the session stream to catch any deltas missed while drilled in.
				have := len(m.transcriptCache[m.activeSub.key()].chunks)
				m.popDetail()
				return m, tea.Batch(cmd, m.subscribeCmd(m.activeSub, have))
			}
			if m.popDetail() { // was the root frame → back to the card list
				m.historyView = histTranscript
			}
			return m, nil
		}
		var cmd tea.Cmd
		if m.activeSub.subID != "" {
			cmd = m.unsubscribeCmd(m.activeSub.subID)
			m.activeSub = subRef{}
		}
		m.sessionSub = subRef{} // clear any stashed drill state on session exit
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

// sessionFooter is the single key-hint line for the whole session screen; it
// reflects the focused region and sub-view.
func (m model) sessionFooter() string {
	switch {
	case m.focus == focusDock:
		if m.isMultiQuestion() {
			return m.footer(promptKeys.Up, promptKeys.TabPrev, promptKeys.Next, promptKeys.Read)
		}
		return m.footer(promptKeys.Up, promptKeys.Submit, promptKeys.Read, sessionKeys.Raw)
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

// sessionLayout returns the history-region height and dock height for the current
// viewport. dockH is 0 when no interaction is pending. When unfocused the dock
// collapses to a rule + one summary line (dockH == 2) so the transcript keeps the
// space; only the focused dock expands to the full option panel.
//
// Fixed chrome that sessionView always draws: header(1) + blank lines above and
// below the body(2) + footer(1) = 4 lines. When the dock is present there is one
// extra line for the rule between the history body and the dock.
func (m model) sessionLayout() (historyH, dockH int) {
	if m.sessionInteraction() == nil {
		return max(1, m.height-4), 0
	}
	avail := max(1, m.height-5) // chrome(4) + history/dock join(1)
	// Unfocused: collapse to rule + one summary line; the full panel is only
	// shown while answering (focused).
	if m.focus != focusDock {
		return max(1, avail-2), 2
	}
	capH := avail - 1                         // focused = answering: expand to fit; history keeps ≥1 line
	dockH = min(m.dockContentLines()+1, capH) // +1 for the focus rule
	if dockH < 3 {
		dockH = 3
	}
	if dockH > avail-1 {
		dockH = avail - 1
	}
	return max(1, avail-dockH), dockH
}

// dockSummary is the one-line description of the pending interaction shown in the
// collapsed (unfocused) dock.
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
			return "Allow " + ix.ToolName + "?"
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

// dockSummaryLine renders the collapsed dock body: an accent marker + interaction
// summary on the left (truncated to fit), a dim "Tab to answer" hint right-aligned.
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

// dockWidths splits the dock into a left column (the option list) and a right
// column (the focused option's preview) for the side-by-side layout. side is false
// when there is no preview or the terminal is too narrow to split — then leftW is
// the full width and the dock stays a single column.
func (m model) dockWidths() (leftW, rightW int, side bool) {
	W := m.containerWidth()
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

// dockContentLines is the natural (unclamped) line count of the dock body: the
// option list, plus any preview either beside it (side-by-side: the taller column)
// or stacked below it (narrow terminal).
func (m model) dockContentLines() int {
	preview := m.focusedOptionPreview()
	leftW, _, side := m.dockWidths()
	leftLines, _ := m.promptLinesWidth(leftW)
	if preview == "" {
		return len(leftLines)
	}
	previewLines := strings.Count(preview, "\n") + 1 + 2 // + border rows
	if side {
		return max(len(leftLines), previewLines)
	}
	return len(leftLines) + previewLines // stacked
}

// dockBody composes the prompt dock body within height rows: a single option
// column, or — when the focused option has a preview — options on the left with the
// preview boxed on the right (side-by-side), falling back to a stacked preview box
// on terminals too narrow to split. The option column is windowed around its active
// control so the controls never scroll out of view.
func (m model) dockBody(height int) string {
	preview := m.focusedOptionPreview()
	leftW, rightW, side := m.dockWidths()
	lines, anchor := m.promptLinesWidth(leftW)
	left := windowLines(strings.Join(lines, "\n"), height, anchor)

	switch {
	case preview == "":
		return left
	case side:
		right := previewBox(preview, rightW, height)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	default: // narrow terminal: stack a compact preview box under the options
		stacked := strings.Join(lines, "\n") + "\n" + previewBox(preview, leftW, max(3, height/2))
		return windowLines(stacked, height, anchor)
	}
}

// windowLines returns at most height lines from s, scrolled so the anchor line
// stays visible: it keeps the top (and thus the heading) while the anchor fits,
// otherwise it slides down so the anchor is the last visible line. This keeps the
// dock's interactive controls on screen even when the descriptive context is tall.
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

// sessionView composes the session screen: a single header, the history region,
// and a conditional prompt dock separated by a rule that turns accent-colored
// while the dock holds focus. The history region is sliced by viewportHeight
// (which returns the layout's history height) and the dock body is windowed around
// its active control so the two regions together never overflow the terminal.
func (m model) sessionView() string {
	s := m.sessions[m.selectedID]
	header := headerStyle.Render("argus · "+s.Tmux.SessionName) +
		dimStyle.Render(fmt.Sprintf("  [%s] %s", s.Tmux.PaneID, statusWord(s)))

	body := m.historyBody()
	if m.sessionInteraction() != nil {
		_, dockH := m.sessionLayout()
		ruleColor := ColorBorder
		if m.focus == focusDock {
			ruleColor = ColorAccent
		}
		rule := lipgloss.NewStyle().Foreground(ruleColor).
			Render(strings.Repeat("─", m.containerWidth()))
		// The rule takes one of the dock's lines. Focused: the rest holds the
		// (possibly side-by-side) dock body, windowed so the controls never scroll
		// off. Unfocused: a single summary line. Centered to line up with the
		// centered transcript above.
		dockBody := m.dockSummaryLine(m.containerWidth())
		if m.focus == focusDock {
			dockBody = m.dockBody(dockH - 1)
		}
		dock := rule + "\n" + dockBody
		body = body + "\n" + centerBlock(dock, m.containerWidth(), m.width)
	}
	return header + "\n\n" + body + "\n\n" + m.sessionFooter()
}
