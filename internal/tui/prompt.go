package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// Compose-then-submit prompt dock: nothing is sent to Claude until Enter. On
// submit the node resolves the parked PermissionRequest hook structurally
// (decision / answers), so the prompt never appears in Claude's pane. Idle
// replies go through pane input; ctrl+s drops to the raw screen view.
//
// AskUserQuestion with several questions renders as a tabbed panel + trailing
// "Submit" review tab; a single question hides the tabs and submits on Enter.

// otherLabel is the synthetic "type your own" option (matches Claude's UI).
const otherLabel = "✎ type your own…"

// promptState is the prompt dock draft. Questions use the per-question slices;
// permission/plan/idle use the scalar drafts.
type promptState struct {
	tab         int            // active tab: 0..len-1 question, ==len → Submit tab
	sel         []int          // highlighted option index per question (navigation only)
	chosen      []int          // committed single-select option per question (-1 = unanswered)
	toggles     []map[int]bool // multi-select toggles per question
	text        []string       // "type your own" draft per question
	submitSel   int            // 0=Submit, 1=Cancel on the Submit tab
	decisionSel int            // permission/plan option index (Allow/Deny)
	reasonText  string         // permission/plan deny reason + idle reply buffer
	key         string         // identity of the interaction the draft belongs to
}

// -- Interaction / question accessors -----------------------------------------

func (m model) interaction() *session.Interaction {
	return m.sessions[m.selectedID].Interaction
}

func (m model) numQuestions() int {
	ix := m.interaction()
	if ix == nil {
		return 0
	}
	return len(ix.Questions)
}

// isMultiQuestion reports whether the prompt has >1 question (so it gets a tab bar + Submit tab).
func (m model) isMultiQuestion() bool { return m.numQuestions() > 1 }

// onSubmitTab reports whether the active tab is the trailing Submit/review tab.
func (m model) onSubmitTab() bool {
	return m.isMultiQuestion() && m.prompt.tab >= m.numQuestions()
}

// activeQuestion returns the question for the active tab, or nil (Submit tab / non-question).
func (m model) activeQuestion() *session.QuestionSpec {
	ix := m.interaction()
	if ix == nil || ix.Kind != session.InteractionQuestion {
		return nil
	}
	if m.prompt.tab < 0 || m.prompt.tab >= len(ix.Questions) {
		return nil
	}
	return &ix.Questions[m.prompt.tab]
}

// decisionOptions returns the server-supplied option labels for a permission/plan decision.
func decisionOptions(ix *session.Interaction) []string {
	labels := make([]string, len(ix.Options))
	for i, o := range ix.Options {
		labels[i] = o.Label
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

// decisionRejecting reports whether the highlighted option is the reject choice
// (deny / keep planning), which surfaces the reason field.
func (m model) decisionRejecting(ix *session.Interaction) bool {
	sel := m.prompt.decisionSel
	return sel >= 0 && sel < len(ix.Options) && ix.Options[sel].Reject
}

// questionOptions returns a question's option labels plus the "type your own" entry.
func questionOptions(q *session.QuestionSpec) []string {
	return append(append([]string{}, q.Options...), otherLabel)
}

// otherIndex is the index of a question's "type your own" entry.
func otherIndex(q *session.QuestionSpec) int { return len(q.Options) }

// -- Per-question draft state -------------------------------------------------

func (m *model) resetPromptState() {
	m.prompt.tab, m.prompt.submitSel, m.prompt.decisionSel = 0, 0, 0
	m.prompt.reasonText = ""
	m.prompt.sel, m.prompt.chosen, m.prompt.toggles, m.prompt.text = nil, nil, nil, nil
}

// ensurePromptState sizes the per-question slices to n (preserving entries) and
// clamps the active tab. chosen defaults to -1 (unanswered).
func (m *model) ensurePromptState(n int) {
	if n < 0 {
		n = 0
	}
	if len(m.prompt.sel) != n {
		sel := make([]int, n)
		chosen := make([]int, n)
		tog := make([]map[int]bool, n)
		txt := make([]string, n)
		for i := 0; i < n; i++ {
			chosen[i] = -1
			if i < len(m.prompt.sel) {
				sel[i] = m.prompt.sel[i]
			}
			if i < len(m.prompt.chosen) {
				chosen[i] = m.prompt.chosen[i]
			}
			if i < len(m.prompt.toggles) && m.prompt.toggles[i] != nil {
				tog[i] = m.prompt.toggles[i]
			} else {
				tog[i] = map[int]bool{}
			}
			if i < len(m.prompt.text) {
				txt[i] = m.prompt.text[i]
			}
		}
		m.prompt.sel, m.prompt.chosen, m.prompt.toggles, m.prompt.text = sel, chosen, tog, txt
	}
	maxTab := n - 1
	if n > 1 {
		maxTab = n // Submit tab
	}
	if m.prompt.tab > maxTab {
		m.prompt.tab = maxTab
	}
	if m.prompt.tab < 0 {
		m.prompt.tab = 0
	}
}

// Bounds-checked getters so rendering never panics if state isn't sized yet.
func (m model) qSel(tab int) int {
	if tab >= 0 && tab < len(m.prompt.sel) {
		return m.prompt.sel[tab]
	}
	return 0
}

func (m model) qToggles(tab int) map[int]bool {
	if tab >= 0 && tab < len(m.prompt.toggles) && m.prompt.toggles[tab] != nil {
		return m.prompt.toggles[tab]
	}
	return map[int]bool{}
}

func (m model) qText(tab int) string {
	if tab >= 0 && tab < len(m.prompt.text) {
		return m.prompt.text[tab]
	}
	return ""
}

// qChosen returns the committed single-select option index, or -1 (unanswered).
func (m model) qChosen(tab int) int {
	if tab >= 0 && tab < len(m.prompt.chosen) {
		return m.prompt.chosen[tab]
	}
	return -1
}

// qAnswered reports whether the question at tab has an explicit answer (committed
// selection / toggles, never the navigation highlight).
func (m model) qAnswered(tab int) bool {
	ix := m.interaction()
	if ix == nil || tab < 0 || tab >= len(ix.Questions) {
		return false
	}
	_, ok := m.questionAnswer(&ix.Questions[tab], tab)
	return ok
}

// questionAnswer returns the committed answer (string for single-select, []string
// for multi) and whether it is answered. The navigation highlight never affects this.
func (m model) questionAnswer(q *session.QuestionSpec, tab int) (any, bool) {
	oIdx := otherIndex(q)
	custom := strings.TrimSpace(m.qText(tab))
	if q.MultiSelect {
		var labels []string
		for i, o := range q.Options {
			if m.qToggles(tab)[i] {
				labels = append(labels, o)
			}
		}
		if m.qToggles(tab)[oIdx] && custom != "" {
			labels = append(labels, custom)
		}
		if len(labels) == 0 {
			return nil, false
		}
		return labels, true
	}
	sel := m.qChosen(tab)
	if sel < 0 {
		return nil, false
	}
	if sel == oIdx {
		if custom == "" {
			return nil, false
		}
		return custom, true
	}
	if sel < len(q.Options) {
		return q.Options[sel], true
	}
	return nil, false
}

// otherActive reports whether the "type your own" entry is selected (single) or
// toggled (multi), i.e. the free-text field should accept input.
func (m model) otherActive(q *session.QuestionSpec, tab int) bool {
	oi := otherIndex(q)
	if q.MultiSelect {
		return m.qToggles(tab)[oi]
	}
	return m.qSel(tab) == oi
}

// focusedOptionPreview returns the preview markdown for the active question's
// highlighted option, or "" when there is none.
func (m model) focusedOptionPreview() string {
	q := m.activeQuestion()
	if q == nil || q.MultiSelect {
		return ""
	}
	sel := m.qSel(m.prompt.tab)
	if sel < 0 || sel >= len(q.OptionPreviews) { // also excludes the otherIndex row
		return ""
	}
	return strings.TrimSpace(q.OptionPreviews[sel])
}

func (m model) respondCmd(id string, p api.RespondParams) tea.Cmd {
	p.SessionID = id
	client := m.client
	return func() tea.Msg {
		_ = client.Call(api.MethodSessionRespond, p, nil)
		return nil
	}
}

// -- Rendering ----------------------------------------------------------------

// promptBody renders the dock body as a single string.
func (m model) promptBody() string {
	lines, _ := m.promptLines()
	return strings.Join(lines, "\n")
}

// promptLines renders the dock body at the container width.
func (m model) promptLines() ([]string, int) {
	return m.promptLinesWidth(m.containerWidth())
}

// promptLinesWidth renders the dock body wrapped to width and returns the anchor
// line index (the active control) that the dock windows around to keep visible.
func (m model) promptLinesWidth(width int) ([]string, int) {
	ix := m.interaction()
	if ix == nil {
		return []string{dimStyle.Render("(no pending interaction)")}, 0
	}

	// Paneless idle session: argus has no pane to deliver input to, so show a
	// static "respond elsewhere" indicator instead of an editable composer.
	if s := m.sessions[m.selectedID]; ix.Kind == session.InteractionIdle && !s.Controllable() {
		label := StyleAccentBold.Render(Icon.System.Glyph + " " + respondElsewhereLabel(s.Frontend))
		sub := dimStyle.Render("argus can't send input to this session")
		return strings.Split(label+"\n"+sub, "\n"), 0
	}

	switch ix.Kind {
	case session.InteractionQuestion:
		return m.questionLines(ix, width)
	case session.InteractionIdle:
		return m.idleLines(ix, width)
	default: // permission / plan
		return m.decisionLines(ix, width)
	}
}

func splitAnchor(b *strings.Builder, anchor int) ([]string, int) {
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if anchor >= len(lines) {
		anchor = len(lines) - 1
	}
	if anchor < 0 {
		anchor = 0
	}
	return lines, anchor
}

// optionMarks bundles the per-option selection affordances: multi-select
// checkboxes, single-select radio buttons, or none (permission/plan decisions).
type optionMarks struct {
	multi   bool
	toggles map[int]bool
	radio   bool // single-select questions: show ◉/○ for the committed choice
	chosen  int  // committed option index (radio); -1 = none
}

// renderOptions renders a selectable option list, returning the block and the
// highlighted row's line index within it. The highlight is navigation only; the
// committed selection shows via checkbox/radio marks.
func (m model) renderOptions(opts []string, sel int, marks optionMarks, otherIdx int, otherText string, otherActive bool, descs []string, width int) (string, int) {
	var b strings.Builder
	anchor := 0
	for i, opt := range opts {
		selected := i == sel
		marker := "  "
		if selected {
			marker = cursorStyle.Render("▸ ")
		}
		check := ""
		switch {
		case marks.multi:
			if marks.toggles[i] {
				check = "[x] "
			} else {
				check = "[ ] "
			}
		case marks.radio:
			if i == marks.chosen {
				check = lipgloss.NewStyle().Foreground(ColorAccent).Render("◉") + " "
			} else {
				check = StyleDim.Render("○") + " "
			}
		}
		// The "type your own" row is itself the editable field: typing fills it in place.
		label := StyleSecondary.Render(opt)
		if i == otherIdx && (otherActive || otherText != "") {
			text := "✎ " + otherText
			if otherActive {
				text += "▏"
			}
			if selected {
				label = StylePrimaryBold.Render(text)
			} else {
				label = StyleSecondary.Render(text)
			}
		} else if selected {
			label = StylePrimaryBold.Render(opt)
		}
		if selected {
			anchor = strings.Count(b.String(), "\n")
		}
		b.WriteString(marker + check + label + "\n")

		// Dimmed description under the label. Indent = cursor(2) + check-mark column width.
		if i != otherIdx && i < len(descs) {
			if desc := strings.TrimSpace(descs[i]); desc != "" {
				indent := "  "
				switch {
				case marks.multi:
					indent += "    " // "[x] "
				case marks.radio:
					indent += "  " // "◉ "
				}
				wrapped := wrapDim(desc, width-len(indent))
				for _, line := range strings.Split(wrapped, "\n") {
					b.WriteString(indent + line + "\n")
				}
			}
		}
	}
	return strings.TrimRight(b.String(), "\n"), anchor
}

// chatHint is the footer affordance for the "Chat about this" action.
func chatHint() string { return StyleDim.Render("c · chat about this") }

// respondElsewhereLabel points a paneless idle session's user to where it lives.
func respondElsewhereLabel(f session.Frontend) string {
	if f == session.FrontendVSCode {
		return "Respond in VSCode"
	}
	return "Respond in your terminal"
}

// questionLines renders the tabbed question panel (or the active single question).
func (m model) questionLines(ix *session.Interaction, width int) ([]string, int) {
	var b strings.Builder

	if m.isMultiQuestion() {
		b.WriteString(m.promptTabs(width) + "\n\n")
	}

	if m.onSubmitTab() {
		base := strings.Count(b.String(), "\n")
		body, a := m.submitTabBody(ix, width)
		b.WriteString(body)
		b.WriteString("\n\n" + chatHint())
		return splitAnchor(&b, base+a)
	}

	tab := m.prompt.tab
	if tab >= len(ix.Questions) {
		tab = len(ix.Questions) - 1
	}
	q := &ix.Questions[tab]

	if !m.isMultiQuestion() {
		b.WriteString(m.questionHeading(q) + "\n\n")
	}
	if q.Question != "" {
		b.WriteString(m.renderMD(q.Question, width-2) + "\n\n")
	}

	opts := questionOptions(q)
	marks := optionMarks{multi: q.MultiSelect, toggles: m.qToggles(tab),
		radio: !q.MultiSelect, chosen: m.qChosen(tab)}
	base := strings.Count(b.String(), "\n")
	block, a := m.renderOptions(opts, m.qSel(tab), marks,
		otherIndex(q), m.qText(tab), m.otherActive(q, tab), q.OptionDescriptions, width)
	b.WriteString(block)
	b.WriteString("\n\n" + chatHint())
	return splitAnchor(&b, base+a)
}

// decisionLines renders a permission/plan allow-deny prompt with a deny reason.
func (m model) decisionLines(ix *session.Interaction, width int) ([]string, int) {
	var b strings.Builder
	b.WriteString(promptHeading(ix) + "\n\n")
	if body := interactionBody(m, ix, width); body != "" {
		b.WriteString(body + "\n\n")
	}
	opts := decisionOptions(ix)
	base := strings.Count(b.String(), "\n")
	block, a := m.renderOptions(opts, m.prompt.decisionSel, optionMarks{chosen: -1}, -1, "", false, nil, width)
	b.WriteString(block)
	anchor := base + a
	// The reason field appears only on the reject choice.
	if m.decisionRejecting(ix) {
		anchor = strings.Count(b.String(), "\n") + 1
		b.WriteString("\n" + m.rejectInput(ix))
	}
	return splitAnchor(&b, anchor)
}

// rejectInput renders the reject feedback field, or the option's placeholder when empty.
func (m model) rejectInput(ix *session.Interaction) string {
	ph := "reason (for deny)"
	sel := m.prompt.decisionSel
	if sel >= 0 && sel < len(ix.Options) && ix.Options[sel].Placeholder != "" {
		ph = ix.Options[sel].Placeholder
	}
	prefix := userStyle.Render("> ")
	if m.prompt.reasonText == "" {
		return prefix + "▏" + dimStyle.Render(ph)
	}
	return prefix + m.prompt.reasonText + "▏"
}

// idleLines renders the free-text composer for an idle interaction.
func (m model) idleLines(ix *session.Interaction, width int) ([]string, int) {
	var b strings.Builder
	b.WriteString(promptHeading(ix) + "\n\n")
	if body := interactionBody(m, ix, width); body != "" {
		b.WriteString(body + "\n\n")
	}
	anchor := strings.Count(b.String(), "\n")
	b.WriteString(hardWrap(userStyle.Render("> ")+m.prompt.reasonText+"▏", width))
	return splitAnchor(&b, anchor)
}

// promptTabs renders the header tab row (+ trailing Submit tab) for a
// multi-question prompt, falling back to a compact "Question i/N" when too wide.
func (m model) promptTabs(width int) string {
	ix := m.interaction()
	active := lipgloss.NewStyle().Bold(true).Foreground(ColorTextPrimary).Background(ColorAccent).Padding(0, 1)
	idle := lipgloss.NewStyle().Foreground(ColorTextSecondary).Background(ColorBorder).Padding(0, 1)

	var tabs []string
	for i, q := range ix.Questions {
		label := q.Header
		if label == "" {
			label = fmt.Sprintf("Q%d", i+1)
		}
		if m.qAnswered(i) {
			label = "✓ " + label
		}
		st := idle
		if i == m.prompt.tab {
			st = active
		}
		tabs = append(tabs, st.Render(label))
	}
	submit := idle
	if m.onSubmitTab() {
		submit = active
	}
	tabs = append(tabs, submit.Render("Submit"))

	row := strings.Join(tabs, " ")
	if lipgloss.Width(row) > width {
		pos := min(m.prompt.tab+1, len(ix.Questions))
		return StyleDim.Render(fmt.Sprintf("Question %d/%d", pos, len(ix.Questions)))
	}
	return row
}

// submitTabBody renders the answer review list and the Submit/Cancel actions.
func (m model) submitTabBody(ix *session.Interaction, width int) (string, int) {
	var b strings.Builder
	b.WriteString(StyleAccentBold.Render("Review answers") + "\n\n")
	for tab := range ix.Questions {
		q := &ix.Questions[tab]
		head := q.Header
		if head == "" {
			head = fmt.Sprintf("Q%d", tab+1)
		}
		line := StyleSecondaryBold.Render(head) + ": " + m.answerSummary(q, tab)
		b.WriteString(hardWrap(line, width) + "\n")
	}
	b.WriteString("\n")
	for i, act := range []string{"Submit", "Cancel"} {
		marker, label := "  ", StyleSecondary.Render(act)
		if i == m.prompt.submitSel {
			marker, label = cursorStyle.Render("▸ "), StylePrimaryBold.Render(act)
		}
		b.WriteString(marker + label + "\n")
	}
	out := strings.TrimRight(b.String(), "\n")
	// Anchor on the last action so windowing keeps the Submit/Cancel pair visible.
	anchor := strings.Count(out, "\n")
	return out, anchor
}

// answerSummary describes a question's committed answer for the Submit review.
func (m model) answerSummary(q *session.QuestionSpec, tab int) string {
	v, ok := m.questionAnswer(q, tab)
	if !ok {
		return StyleDim.Render("(not answered)")
	}
	if labels, isList := v.([]string); isList {
		return strings.Join(labels, ", ")
	}
	if s, isStr := v.(string); isStr {
		return s
	}
	return StyleDim.Render("(not answered)")
}

// questionHeading is the single-question heading; multi-question prompts carry
// headers in the tab bar instead.
func (m model) questionHeading(q *session.QuestionSpec) string {
	h := StyleAccentBold.Render(Icon.Chat.Glyph + " Claude is asking")
	if q.Header != "" {
		h += "  " + headerChip(q.Header)
	}
	return h
}

// headerChip renders a question's header as a padded chip, shared by the live
// prompt heading and the transcript detail view.
func headerChip(label string) string {
	return lipgloss.NewStyle().Bold(true).
		Foreground(ColorTextPrimary).Background(ColorBorder).
		Padding(0, 1).Render(label)
}

func promptHeading(ix *session.Interaction) string {
	switch ix.Kind {
	case session.InteractionPermission:
		s := "Permission requested"
		if ix.ToolName != "" {
			s += " · " + ix.ToolName
		}
		return StyleAccentBold.Render(Icon.SystemErr.Glyph + " " + s)
	case session.InteractionPlan:
		return StyleAccentBold.Render(Icon.Output.Glyph + " Plan approval")
	default:
		return StyleAccentBold.Render(Icon.System.Glyph + " Waiting for input")
	}
}

// interactionBody renders the descriptive body for plan/permission/idle prompts.
func interactionBody(m model, ix *session.Interaction, width int) string {
	switch ix.Kind {
	case session.InteractionPlan:
		if ix.Plan != "" {
			return m.renderMD(ix.Plan, width-2)
		}
	case session.InteractionPermission:
		var parts []string
		if ix.Message != "" {
			parts = append(parts, hardWrap(StyleSecondary.Render(ix.Message), width-2))
		}
		if ix.ToolInput != "" {
			// Reuse the per-tool renderers (Bash → "$ cmd", Edit → diff, …) on a
			// synthetic item; hardWrap bounds the result here (unlike the detail view).
			it := claudecode.Item{Kind: claudecode.ItemTool, ToolName: ix.ToolName, ToolInput: ix.ToolInput}
			parts = append(parts, hardWrap(m.toolBody(it, width-2), width-2))
		}
		return strings.Join(parts, "\n")
	default:
		if ix.Message != "" {
			return hardWrap(StyleSecondary.Render(ix.Message), width-2)
		}
	}
	return ""
}

// previewBox renders an option's preview verbatim inside a rounded border, clipped
// to width×height: lines truncate on the right, excess rows collapse to "… more".
func previewBox(content string, width, height int) string {
	iw := max(width-2, 10) // border eats 2 columns
	ih := max(height-2, 1) // border eats 2 rows

	truncRunes := func(s string, n int) string {
		r := []rune(s)
		if len(r) <= n {
			return s
		}
		if n <= 1 {
			return string(r[:n])
		}
		return string(r[:n-1]) + "…"
	}

	lines := strings.Split(content, "\n")
	clipped := len(lines) > ih
	if clipped {
		lines = lines[:ih]
	}
	for i, l := range lines {
		lines[i] = truncRunes(l, iw)
	}
	if clipped {
		lines[ih-1] = StyleDim.Render("… more")
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Width(iw).
		Render(strings.Join(lines, "\n"))
}
