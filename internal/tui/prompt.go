package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// Compose-then-submit prompt dock: nothing is sent to Claude until Enter. On
// submit the node resolves the parked PermissionRequest hook structurally
// (decision / answers), so the prompt never appears in Claude's pane. Idle
// replies go through pane input; open live-screen drops to the raw screen view.
//
// AskUserQuestion with several questions renders as a tabbed panel + trailing
// "Submit" review tab; a single question hides the tabs and submits on Enter.

// otherLabel is the synthetic "type your own" option (matches Claude's UI).
const otherLabel = "✎ type your own…"

func newDenyReasonInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	return ti
}

func newQuestionAnswerInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Focus()
	return ti
}

func newIdleReplyArea() textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	// Grow with content and self-clamp the scroll offset. A fixed height scrolls
	// the first line out of view when a newline is added before a re-size.
	ta.DynamicHeight = true
	// Mark only the first line, so a multi-line reply is not prefixed on every row.
	ta.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "> "
		}
		return "  "
	})
	// enter submits, so newlines come from shift+enter (Kitty keyboard protocol).
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter"))
	return ta
}

// sizeIdleReply fits the reply composer to the dock width. Height is dynamic. The
// textarea shares its viewport by pointer, so width must be set in the update
// path, never during render, or a render-time resize would corrupt the offset.
func (d *dockComp) sizeIdleReply(c *ctx) {
	leftW, _, _ := d.dockWidths(c)
	d.reply.SetWidth(leftW)
}

// -- Interaction / question accessors -----------------------------------------

func (m model) interaction() *session.Interaction {
	return m.sessions[m.liveSessionID()].Interaction
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
func (d dockComp) onSubmitTab(c *ctx) bool {
	return c.m.isMultiQuestion() && d.tab >= c.m.numQuestions()
}

// activeQuestion returns the question for the active tab, or nil (Submit tab / non-question).
func (d dockComp) activeQuestion(c *ctx) *session.QuestionSpec {
	ix := c.m.interaction()
	if ix == nil || ix.Kind != session.InteractionQuestion {
		return nil
	}
	if d.tab < 0 || d.tab >= len(ix.Questions) {
		return nil
	}
	return &ix.Questions[d.tab]
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
func (d dockComp) decisionRejecting(ix *session.Interaction) bool {
	sel := d.decisionSel
	return sel >= 0 && sel < len(ix.Options) && ix.Options[sel].Reject
}

// questionOptions returns a question's option labels plus the "type your own" entry.
func questionOptions(q *session.QuestionSpec) []string {
	return append(append([]string{}, q.Options...), otherLabel)
}

// otherIndex is the index of a question's "type your own" entry.
func otherIndex(q *session.QuestionSpec) int { return len(q.Options) }

// -- Per-question draft state -------------------------------------------------

// resetPromptState clears the per-interaction drafts. It leaves the idle reply
// composer untouched: that draft belongs to the session and persists across
// interaction changes (see saveReplyDraft).
func (d *dockComp) resetPromptState() {
	d.tab, d.submitSel, d.decisionSel = 0, 0, 0
	d.reason = newDenyReasonInput()
	d.reason.Focus()
	d.scroll = 0
	d.sel, d.chosen, d.toggles, d.text = nil, nil, nil, nil
}

// saveReplyDraft keeps the composer text per session, so it survives a session
// switch.
func (d *dockComp) saveReplyDraft(id string) {
	if id == "" {
		return
	}
	if d.drafts == nil {
		d.drafts = map[string]string{}
	}
	if strings.TrimSpace(d.reply.Value()) == "" {
		delete(d.drafts, id)
		return
	}
	d.drafts[id] = d.reply.Value()
}

// pruneReplyDrafts drops drafts whose session left the registry, so the map does
// not retain unsent text for sessions that no longer exist.
func (d *dockComp) pruneReplyDrafts(sessions map[string]session.Session) {
	for id := range d.drafts {
		if _, ok := sessions[id]; !ok {
			delete(d.drafts, id)
		}
	}
}

func (d *dockComp) loadReplyDraft(c *ctx, id string) {
	d.reply = newIdleReplyArea()
	if v := d.drafts[id]; v != "" {
		d.reply.SetValue(v)
	}
	d.reply.Focus()
	d.sizeIdleReply(c) // fit the composer so its first render is not default-sized
}

// ensurePromptState sizes the per-question slices to n (preserving entries) and
// clamps the active tab. chosen defaults to -1 (unanswered).
func (d *dockComp) ensurePromptState(n int) {
	if n < 0 {
		n = 0
	}
	if len(d.sel) != n {
		sel := make([]int, n)
		chosen := make([]int, n)
		tog := make([]map[int]bool, n)
		txt := make([]textinput.Model, n)
		for i := 0; i < n; i++ {
			chosen[i] = -1
			if i < len(d.sel) {
				sel[i] = d.sel[i]
			}
			if i < len(d.chosen) {
				chosen[i] = d.chosen[i]
			}
			if i < len(d.toggles) && d.toggles[i] != nil {
				tog[i] = d.toggles[i]
			} else {
				tog[i] = map[int]bool{}
			}
			if i < len(d.text) {
				txt[i] = d.text[i]
			} else {
				txt[i] = newQuestionAnswerInput()
			}
		}
		d.sel, d.chosen, d.toggles, d.text = sel, chosen, tog, txt
	}
	maxTab := n - 1
	if n > 1 {
		maxTab = n // Submit tab
	}
	if d.tab > maxTab {
		d.tab = maxTab
	}
	if d.tab < 0 {
		d.tab = 0
	}
}

// Bounds-checked getters so rendering never panics if state isn't sized yet.
func (d dockComp) qSel(tab int) int {
	if tab >= 0 && tab < len(d.sel) {
		return d.sel[tab]
	}
	return 0
}

func (d dockComp) qToggles(tab int) map[int]bool {
	if tab >= 0 && tab < len(d.toggles) && d.toggles[tab] != nil {
		return d.toggles[tab]
	}
	return map[int]bool{}
}

func (d dockComp) qText(tab int) string {
	if tab >= 0 && tab < len(d.text) {
		return d.text[tab].Value()
	}
	return ""
}

// qChosen returns the committed single-select option index, or -1 (unanswered).
func (d dockComp) qChosen(tab int) int {
	if tab >= 0 && tab < len(d.chosen) {
		return d.chosen[tab]
	}
	return -1
}

// qAnswered reports whether the question at tab has an explicit answer (committed
// selection / toggles, never the navigation highlight).
func (d dockComp) qAnswered(c *ctx, tab int) bool {
	ix := c.m.interaction()
	if ix == nil || tab < 0 || tab >= len(ix.Questions) {
		return false
	}
	_, ok := d.questionAnswer(&ix.Questions[tab], tab)
	return ok
}

// questionAnswer returns the committed answer (string for single-select, []string
// for multi) and whether it is answered. The navigation highlight never affects this.
func (d dockComp) questionAnswer(q *session.QuestionSpec, tab int) (any, bool) {
	oIdx := otherIndex(q)
	custom := strings.TrimSpace(d.qText(tab))
	if q.MultiSelect {
		var labels []string
		for i, o := range q.Options {
			if d.qToggles(tab)[i] {
				labels = append(labels, o)
			}
		}
		if d.qToggles(tab)[oIdx] && custom != "" {
			labels = append(labels, custom)
		}
		if len(labels) == 0 {
			return nil, false
		}
		return labels, true
	}
	sel := d.qChosen(tab)
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

// otherActive reports whether the "type your own" row is highlighted, so the
// free-text field takes editing keys. Multi-select inclusion is a separate
// concern (its toggle), so highlighting elsewhere leaves left/right for tabs.
func (d dockComp) otherActive(q *session.QuestionSpec, tab int) bool {
	return d.qSel(tab) == otherIndex(q)
}

// questionCustomActive reports whether the dock is editing a question's "type
// your own" answer, so a paste is routed to that field.
func (d dockComp) questionCustomActive(c *ctx) bool {
	ix := c.m.interaction()
	if ix == nil || ix.Kind != session.InteractionQuestion || d.onSubmitTab(c) {
		return false
	}
	tab := d.tab
	if tab < 0 || tab >= len(ix.Questions) || tab >= len(d.text) {
		return false
	}
	return d.otherActive(&ix.Questions[tab], tab)
}

// denyReasonActive reports whether the dock is editing a permission/plan deny
// reason, so a paste is routed to that field.
func (d dockComp) denyReasonActive(c *ctx) bool {
	ix := c.m.interaction()
	if ix == nil || (ix.Kind != session.InteractionPermission && ix.Kind != session.InteractionPlan) {
		return false
	}
	return d.decisionRejecting(ix)
}

// focusedOptionPreview returns the preview markdown for the active question's
// highlighted option, or "" when there is none.
func (d dockComp) focusedOptionPreview(c *ctx) string {
	q := d.activeQuestion(c)
	if q == nil || q.MultiSelect {
		return ""
	}
	sel := d.qSel(d.tab)
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
func (d dockComp) promptBody(c *ctx) string {
	lines, _, _ := d.promptLines(c)
	return strings.Join(lines, "\n")
}

// promptLines renders the dock body at the container width.
func (d dockComp) promptLines(c *ctx) ([]string, int, int) {
	return d.promptLinesWidth(c, c.m.containerWidth())
}

// promptLinesWidth renders the dock body wrapped to width and returns the anchor
// line index (the active control the dock windows around to keep visible) and
// ctrlStart, the first line of the pinned control block: lines above ctrlStart
// scroll, lines from ctrlStart on stay pinned at the dock bottom.
func (d dockComp) promptLinesWidth(c *ctx, width int) ([]string, int, int) {
	ix := c.m.interaction()
	if ix == nil {
		return []string{dimStyle.Render("(no pending interaction)")}, 0, 0
	}

	// Paneless idle session with no API prompt path: argus has no way to deliver
	// input, so show a static "respond elsewhere" indicator instead of a composer.
	if s := c.m.sessions[c.m.liveSessionID()]; ix.Kind == session.InteractionIdle && !s.AcceptsInput() {
		label := StyleAccentBold.Render(Icon.System.Glyph + " " + respondElsewhereLabel(s.Frontend))
		sub := dimStyle.Render("argus can't send input to this session")
		return strings.Split(label+"\n"+sub, "\n"), 0, 0
	}

	switch ix.Kind {
	case session.InteractionQuestion:
		return d.questionLines(c, ix, width)
	case session.InteractionIdle:
		return d.idleLines(c, ix, width)
	default: // permission / plan
		return d.decisionLines(c, ix, width)
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

// splitAnchorCtrl is splitAnchor plus a clamped ctrlStart (first pinned-control
// line): lines above it scroll, lines from it on stay pinned to the dock bottom.
func splitAnchorCtrl(b *strings.Builder, anchor, ctrlStart int) ([]string, int, int) {
	lines, a := splitAnchor(b, anchor)
	if ctrlStart < 0 {
		ctrlStart = 0
	}
	if ctrlStart > len(lines) {
		ctrlStart = len(lines)
	}
	return lines, a, ctrlStart
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
func (d dockComp) questionLines(c *ctx, ix *session.Interaction, width int) ([]string, int, int) {
	var b strings.Builder

	if c.m.isMultiQuestion() {
		b.WriteString(d.promptTabs(c, width) + "\n\n")
	}

	if d.onSubmitTab(c) {
		base := strings.Count(b.String(), "\n")
		body, a, ctrl := d.submitTabBody(ix, width)
		b.WriteString(body)
		b.WriteString("\n\n" + chatHint())
		return splitAnchorCtrl(&b, base+a, base+ctrl)
	}

	tab := d.tab
	if tab >= len(ix.Questions) {
		tab = len(ix.Questions) - 1
	}
	q := &ix.Questions[tab]

	if !c.m.isMultiQuestion() {
		b.WriteString(c.m.questionHeading(q) + "\n\n")
	}
	if q.Question != "" {
		b.WriteString(c.m.renderMD(q.Question, width-2) + "\n\n")
	}

	opts := questionOptions(q)
	marks := optionMarks{multi: q.MultiSelect, toggles: d.qToggles(tab),
		radio: !q.MultiSelect, chosen: d.qChosen(tab)}
	base := strings.Count(b.String(), "\n")
	otherText := d.qText(tab)
	if d.otherActive(q, tab) && tab < len(d.text) {
		ti := d.text[tab]
		ti.SetWidth(width)
		otherText = ti.View()
	}
	block, a := c.m.renderOptions(opts, d.qSel(tab), marks,
		otherIndex(q), otherText, d.otherActive(q, tab), q.OptionDescriptions, width)
	b.WriteString(block)
	b.WriteString("\n\n" + chatHint())
	// Question text (and tab bar) above the options scroll; the option list pins.
	return splitAnchorCtrl(&b, base+a, base)
}

// decisionLines renders a permission/plan allow-deny prompt with a deny reason.
func (d dockComp) decisionLines(c *ctx, ix *session.Interaction, width int) ([]string, int, int) {
	var b strings.Builder
	b.WriteString(promptHeading(ix) + "\n\n")
	if body := interactionBody(*c.m, ix, width); body != "" {
		b.WriteString(body + "\n\n")
	}
	opts := decisionOptions(ix)
	base := strings.Count(b.String(), "\n")
	block, a := c.m.renderOptions(opts, d.decisionSel, optionMarks{chosen: -1}, -1, "", false, nil, width)
	b.WriteString(block)
	anchor := base + a
	// The reason field appears only on the reject choice.
	if d.decisionRejecting(ix) {
		anchor = strings.Count(b.String(), "\n") + 1
		b.WriteString("\n" + d.rejectInput(ix, width))
	}
	// The plan/permission body above the options scrolls; options + reason pin.
	return splitAnchorCtrl(&b, anchor, base)
}

// rejectInput renders the reject feedback field, or the option's placeholder when empty.
func (d dockComp) rejectInput(ix *session.Interaction, width int) string {
	ph := "reason (for deny)"
	sel := d.decisionSel
	if sel >= 0 && sel < len(ix.Options) && ix.Options[sel].Placeholder != "" {
		ph = ix.Options[sel].Placeholder
	}
	ti := d.reason
	ti.Placeholder = ph
	ti.SetWidth(width)
	return userStyle.Render("> ") + ti.View()
}

// idleLines renders the free-text composer for an idle interaction.
func (d dockComp) idleLines(c *ctx, ix *session.Interaction, width int) ([]string, int, int) {
	var b strings.Builder
	b.WriteString(promptHeading(ix) + "\n\n")
	if body := interactionBody(*c.m, ix, width); body != "" {
		b.WriteString(body + "\n\n")
	}
	anchor := strings.Count(b.String(), "\n")
	// The composer is sized in the update path (sizeIdleReply): resizing here would
	// mutate the shared viewport pointer and scroll the next keypress.
	b.WriteString(d.reply.View())
	// The message body above scrolls; the reply composer pins to the bottom.
	return splitAnchorCtrl(&b, anchor, anchor)
}

// promptTabs renders the header tab row (+ trailing Submit tab) for a
// multi-question prompt, falling back to a compact "Question i/N" when too wide.
func (d dockComp) promptTabs(c *ctx, width int) string {
	ix := c.m.interaction()
	active := lipgloss.NewStyle().Bold(true).Foreground(ColorTextPrimary).Background(ColorAccent).Padding(0, 1)
	idle := lipgloss.NewStyle().Foreground(ColorTextSecondary).Background(ColorBorder).Padding(0, 1)

	var tabs []string
	for i, q := range ix.Questions {
		label := q.Header
		if label == "" {
			label = fmt.Sprintf("Q%d", i+1)
		}
		if d.qAnswered(c, i) {
			label = "✓ " + label
		}
		st := idle
		if i == d.tab {
			st = active
		}
		tabs = append(tabs, st.Render(label))
	}
	submit := idle
	if d.onSubmitTab(c) {
		submit = active
	}
	tabs = append(tabs, submit.Render("Submit"))

	row := strings.Join(tabs, " ")
	if lipgloss.Width(row) > width {
		pos := min(d.tab+1, len(ix.Questions))
		return StyleDim.Render(fmt.Sprintf("Question %d/%d", pos, len(ix.Questions)))
	}
	return row
}

// submitTabBody renders the answer review list and the Submit/Cancel actions.
// It returns the anchor (last action line) and ctrlStart (first action line):
// the answer list above ctrlStart scrolls, the Submit/Cancel pair pins.
func (d dockComp) submitTabBody(ix *session.Interaction, width int) (string, int, int) {
	var b strings.Builder
	b.WriteString(StyleAccentBold.Render("Review answers") + "\n\n")
	for tab := range ix.Questions {
		q := &ix.Questions[tab]
		head := q.Header
		if head == "" {
			head = fmt.Sprintf("Q%d", tab+1)
		}
		line := StyleSecondaryBold.Render(head) + ": " + d.answerSummary(q, tab)
		b.WriteString(hardWrap(line, width) + "\n")
	}
	b.WriteString("\n")
	ctrlStart := strings.Count(b.String(), "\n")
	for i, act := range []string{"Submit", "Cancel"} {
		marker, label := "  ", StyleSecondary.Render(act)
		if i == d.submitSel {
			marker, label = cursorStyle.Render("▸ "), StylePrimaryBold.Render(act)
		}
		b.WriteString(marker + label + "\n")
	}
	out := strings.TrimRight(b.String(), "\n")
	// Anchor on the last action so windowing keeps the Submit/Cancel pair visible.
	anchor := strings.Count(out, "\n")
	return out, anchor, ctrlStart
}

// answerSummary describes a question's committed answer for the Submit review.
func (d dockComp) answerSummary(q *session.QuestionSpec, tab int) string {
	v, ok := d.questionAnswer(q, tab)
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
			s += " · " + toolDisplayName(ix.ToolName)
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
			it := transcript.Item{Kind: transcript.ItemTool, ToolName: ix.ToolName, ToolInput: ix.ToolInput}
			parts = append(parts, hardWrap(m.renderToolBody(it, width-2), width-2))
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
