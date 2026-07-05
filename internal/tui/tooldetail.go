package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func (m model) fetchToolBodyCmd(it transcript.Item, agentID string) tea.Cmd {
	if it.ToolID == "" {
		return nil
	}
	if e, ok := m.toolBodies[it.ToolID]; ok && (e.done || e.loading) {
		return nil
	}
	m.toolBodies[it.ToolID] = toolBodyEntry{loading: true}
	client := m.client
	toolID := it.ToolID
	if m.mode == modeHistoryTranscript {
		nodeID, path := m.history.openNodeID, m.history.openPath
		return func() tea.Msg {
			var td api.ToolDetail
			err := client.Call(api.MethodSessionHistoryToolDetail, api.HistoryToolDetailParams{
				NodeID: nodeID, TranscriptPath: path, AgentID: agentID, ToolID: toolID,
			}, &td)
			return toolDetailMsg{toolID: toolID, detail: td, err: err}
		}
	}
	sessionID := m.selectedID
	return func() tea.Msg {
		var td api.ToolDetail
		err := client.Call(api.MethodSessionToolDetail, api.ToolDetailParams{
			SessionID: sessionID, AgentID: agentID, ToolID: toolID,
		}, &td)
		return toolDetailMsg{toolID: toolID, detail: td, err: err}
	}
}

func (m model) toolDetailBody(it transcript.Item, width int) (string, bool) {
	meta, ok := toolRegistry[it.ToolName]
	if !ok || meta.detail == nil {
		return "", false // unregistered, or registered with the generic body
	}
	return meta.detail(m, it, width), true
}

// sectionLabel renders a bold section heading ("Input"/"Result"), red for errors.
func sectionLabel(text string, isErr bool) string {
	if isErr {
		return StyleErrorBold.Render(text)
	}
	return StyleSecondaryBold.Render(text)
}

// sectionRule renders a full-width muted horizontal separator.
func sectionRule(width int) string {
	if width < 1 {
		width = 1
	}
	return StyleMuted.Render(strings.Repeat(GlyphHRule, width))
}

func resultLabelText(it transcript.Item) string {
	if it.ResultIsError {
		return "Error"
	}
	return "Result"
}

// hardWrap wraps s to width preserving ANSI styling; lipgloss hard-wraps so even a
// long no-space token (path/URL) is bounded rather than overflowing.
func hardWrap(s string, width int) string {
	return lipgloss.NewStyle().Width(max(width, 10)).Render(s)
}

// renderToolText highlights JSON (else plain text) and wraps to width so long lines
// never run off-screen.
func (m model) renderToolText(s string, width int) string {
	s = strings.TrimRight(s, "\n")
	if m.transcript.jsonHL != nil {
		if out, ok := m.transcript.jsonHL.highlight(s); ok {
			return hardWrap(strings.TrimRight(out, "\n"), width)
		}
	}
	return hardWrap(s, width)
}

func (m model) genericToolBody(it transcript.Item, width int) string {
	var sb strings.Builder
	if it.ToolInput != "" {
		sb.WriteString(sectionLabel("Input", false) + "\n")
		sb.WriteString(m.renderToolText(it.ToolInput, width) + "\n")
	}
	if it.Result != "" {
		if sb.Len() > 0 {
			sb.WriteString(sectionRule(width) + "\n")
		}
		sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
		sb.WriteString(m.renderToolText(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) editToolDetail(it transcript.Item, width int) string {
	var sb strings.Builder
	if diff, ok := editDiff(it.ToolName, it.ToolInput); ok {
		sb.WriteString(diff)
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}
	if it.Result != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n" + sectionRule(width) + "\n")
		}
		sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
		sb.WriteString(m.renderToolText(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// unmarshalInput is a small helper for per-tool renderers to decode ToolInput.
func unmarshalInput(raw string, v any) {
	_ = json.Unmarshal([]byte(raw), v)
}

func (m model) bashDetail(it transcript.Item, width int) string {
	var in struct {
		Command     string `json:"command"`
		Description string `json:"description"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Description != "" {
		sb.WriteString(StyleDim.Render("# "+in.Description) + "\n")
	}
	if in.Command != "" {
		// One Render call: a split would insert an ANSI reset between "$" and cmd.
		sb.WriteString(StyleSecondaryBold.Render("$ "+in.Command) + "\n")
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width) + "\n")
	}
	if it.Result != "" {
		sb.WriteString(sectionRule(width) + "\n")
		sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
		sb.WriteString(m.renderToolText(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func dumpLines(text string, width int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, ln := range lines {
		idx := strings.IndexByte(ln, ':')
		if idx < 0 {
			lines[i] = hardWrap(ln, width)
			continue
		}
		key := strings.TrimSpace(ln[:idx])
		val := strings.TrimSpace(ln[idx+1:])
		lines[i] = hardWrap(StyleSecondaryBold.Render(key+":")+" "+val, width)
	}
	return strings.Join(lines, "\n")
}

func prettyJSON(s string) string {
	var buf bytes.Buffer
	if json.Indent(&buf, []byte(s), "", "  ") != nil {
		return s
	}
	return buf.String()
}

func (m model) execCommandDetail(it transcript.Item, width int) string {
	var in struct {
		Command         string `json:"cmd"`
		Workdir         string `json:"workdir"`
		YieldTimeMs     int    `json:"yield_time_ms"`
		MaxOutputTokens int    `json:"max_output_tokens"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Command != "" {
		if in.Workdir != "" {
			sb.WriteString(StyleDim.Render("# "+in.Workdir) + "\n")
		}
		sb.WriteString(StyleSecondaryBold.Render("$ "+in.Command) + "\n")
		var meta []string
		if in.YieldTimeMs > 0 {
			meta = append(meta, fmt.Sprintf("yield %dms", in.YieldTimeMs))
		}
		if in.MaxOutputTokens > 0 {
			meta = append(meta, fmt.Sprintf("max %d tokens", in.MaxOutputTokens))
		}
		if len(meta) > 0 {
			sb.WriteString(StyleDim.Render(strings.Join(meta, " · ")) + "\n")
		}
	} else if it.ToolInput != "" {
		sb.WriteString(dumpLines(prettyJSON(it.ToolInput), width) + "\n")
	}

	if it.Result != "" {
		if sb.Len() > 0 {
			sb.WriteString(sectionRule(width) + "\n")
		}
		sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
		sb.WriteString(m.execCommandResultBody(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) execCommandResultBody(result string, width int) string {
	const marker = "Output:\n"
	idx := strings.Index(result, marker)
	if idx < 0 {
		return dumpLines(result, width)
	}
	head := dumpLines(result[:idx]+"Output:", width)
	body := result[idx+len(marker):]
	if body == "" {
		return head
	}
	return head + "\n" + m.renderToolText(body, width)
}

func isAgentRefTool(name string) bool {
	return name == "wait_agent" || name == "close_agent"
}

func agentDisplayName(it transcript.Item, agentID string) string {
	for _, s := range it.Subagents {
		if s.ID == agentID && s.Name != "" {
			return s.Name
		}
	}
	return agentID
}

func agentTargetNames(it transcript.Item) []string {
	if len(it.Subagents) == 0 {
		return nil
	}
	names := make([]string, len(it.Subagents))
	for i, s := range it.Subagents {
		if s.Name != "" {
			names[i] = s.Name
		} else {
			names[i] = s.ID
		}
	}
	return names
}

func agentToolLabel(it transcript.Item) string {
	prefix := "Wait Agent"
	if it.ToolName == "close_agent" {
		prefix = "Close Agent"
	}
	names := agentTargetNames(it)
	if len(names) == 0 {
		return prefix
	}
	return prefix + ": " + strings.Join(names, ", ")
}

// agentStatusText extracts the (state, message) pair from a status object of
// shape {"<state>":"<message>"}.
func agentStatusText(raw json.RawMessage) (state, message string, ok bool) {
	var m map[string]string
	if json.Unmarshal(raw, &m) != nil || len(m) == 0 {
		return "", "", false
	}
	for k, v := range m {
		return k, v, true // exactly one key in practice
	}
	return "", "", false
}

func (m model) agentStatusBlock(name string, raw json.RawMessage, width int) string {
	state, message, ok := agentStatusText(raw)
	if !ok {
		return StyleSecondaryBold.Render(name)
	}
	head := StyleSecondaryBold.Render(name) + ": " + StyleSecondary.Render(state)
	if message == "" {
		return head
	}
	return head + "\n" + m.renderMD(message, width)
}

func (m model) waitAgentDetail(it transcript.Item, width int) string {
	var in struct {
		Targets   []string `json:"targets"`
		TimeoutMs int      `json:"timeout_ms"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if len(in.Targets) > 0 {
		names := make([]string, len(in.Targets))
		for i, id := range in.Targets {
			names[i] = agentDisplayName(it, id)
		}
		sb.WriteString(StyleSecondaryBold.Render("Waiting on ") + strings.Join(names, ", "))
		if in.TimeoutMs > 0 {
			sb.WriteString(StyleDim.Render(fmt.Sprintf("  (timeout %dms)", in.TimeoutMs)))
		}
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}

	if it.Result != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n" + sectionRule(width) + "\n")
		}
		sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
		var out struct {
			Status map[string]json.RawMessage `json:"status"`
		}
		if json.Unmarshal([]byte(it.Result), &out) == nil && len(out.Status) > 0 {
			ids := in.Targets
			if len(ids) == 0 {
				for id := range out.Status {
					ids = append(ids, id) // no input order to follow; best effort
				}
			}
			var blocks []string
			for _, id := range ids {
				if raw, ok := out.Status[id]; ok {
					blocks = append(blocks, m.agentStatusBlock(agentDisplayName(it, id), raw, width))
				}
			}
			sb.WriteString(strings.Join(blocks, "\n"))
		} else {
			sb.WriteString(m.renderToolText(it.Result, width))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) closeAgentDetail(it transcript.Item, width int) string {
	var in struct {
		Target string `json:"target"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Target != "" {
		sb.WriteString(StyleSecondaryBold.Render("Closed ") + agentDisplayName(it, in.Target))
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}

	if it.Result != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n" + sectionRule(width) + "\n")
		}
		sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
		var out struct {
			PreviousStatus json.RawMessage `json:"previous_status"`
		}
		if json.Unmarshal([]byte(it.Result), &out) == nil && len(out.PreviousStatus) > 0 {
			sb.WriteString(m.agentStatusBlock(agentDisplayName(it, in.Target), out.PreviousStatus, width))
		} else {
			sb.WriteString(m.renderToolText(it.Result, width))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) readDetail(it transcript.Item, width int) string {
	var in struct {
		FilePath string `json:"file_path"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.FilePath != "" {
		sb.WriteString(StyleSecondary.Render(in.FilePath) + "\n")
	}
	if it.Result != "" {
		sb.WriteString(sectionRule(width) + "\n")
		sb.WriteString(m.renderToolText(it.Result, width))
	} else if it.ToolInput != "" && in.FilePath == "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) todoDetail(it transcript.Item, width int) string {
	var in struct {
		Todos []struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm"`
		} `json:"todos"`
	}
	unmarshalInput(it.ToolInput, &in)
	if len(in.Todos) == 0 {
		return m.genericToolBody(it, width)
	}
	var rows []string
	for _, t := range in.Todos {
		icon := Icon.Task.Pending
		text := t.Content
		switch t.Status {
		case "completed":
			icon = Icon.Task.Done
		case "in_progress":
			icon = Icon.Task.Active
			if t.ActiveForm != "" {
				text = t.ActiveForm
			}
		}
		rows = append(rows, icon.Render()+" "+text)
	}
	return strings.Join(rows, "\n")
}

func (m model) planDetail(it transcript.Item, width int) string {
	var in struct {
		Plan []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	unmarshalInput(it.ToolInput, &in)
	if len(in.Plan) == 0 {
		return m.genericToolBody(it, width)
	}
	var rows []string
	for _, p := range in.Plan {
		icon := Icon.Task.Pending
		switch p.Status {
		case "completed":
			icon = Icon.Task.Done
		case "in_progress":
			icon = Icon.Task.Active
		}
		rows = append(rows, icon.Render()+" "+p.Step)
	}
	return strings.Join(rows, "\n")
}

func (m model) grepDetail(it transcript.Item, width int) string {
	var in struct {
		Pattern string `json:"pattern"`
		Glob    string `json:"glob"`
		Path    string `json:"path"`
	}
	unmarshalInput(it.ToolInput, &in)

	header := StyleSecondaryBold.Render(`"` + in.Pattern + `"`)
	scope := in.Glob
	if in.Path != "" {
		if scope != "" {
			scope += " "
		}
		scope += in.Path
	}
	if scope != "" {
		header += StyleDim.Render(" in " + scope)
	}

	var sb strings.Builder
	sb.WriteString(header)
	if it.Result != "" {
		sb.WriteString("\n" + sectionRule(width) + "\n")
		sb.WriteString(m.renderToolText(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) globDetail(it transcript.Item, width int) string {
	var in struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	unmarshalInput(it.ToolInput, &in)
	head := in.Pattern
	if head == "" {
		head = in.Path
	}
	var sb strings.Builder
	if head != "" {
		sb.WriteString(StyleSecondaryBold.Render(head))
	}
	if it.Result != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n" + sectionRule(width) + "\n")
		}
		sb.WriteString(m.renderToolText(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) taskCreateDetail(it transcript.Item, width int) string {
	var in struct {
		Subject     string `json:"subject"`
		Description string `json:"description"`
		ActiveForm  string `json:"activeForm"`
	}
	unmarshalInput(it.ToolInput, &in)
	if in.Subject == "" && it.ToolInput != "" {
		return m.genericToolBody(it, width)
	}

	var sb strings.Builder
	sb.WriteString(StyleSecondaryBold.Render(in.Subject))
	if in.ActiveForm != "" {
		sb.WriteString("\n" + StyleDim.Render(in.ActiveForm))
	}
	if in.Description != "" {
		sb.WriteString("\n" + m.renderToolText(in.Description, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) taskUpdateDetail(it transcript.Item, width int) string {
	var in struct {
		TaskID string `json:"taskId"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.TaskID != "" {
		sb.WriteString(StyleSecondaryBold.Render("Task "+in.TaskID) + "\n")
	}
	if it.ToolInput != "" {
		sb.WriteString(dumpLines(prettyJSON(it.ToolInput), width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

var askUserQuestionPair = regexp.MustCompile(`"([^"]+)"="([^"]*)"`)

// parseAnsweredAnswers best-effort parses an AskUserQuestion result into a
// question→answer map. The result is free text, so an unparseable string yields an
// empty map and callers degrade gracefully (no marks).
func parseAnsweredAnswers(result string) map[string]string {
	out := map[string]string{}
	for _, mt := range askUserQuestionPair.FindAllStringSubmatch(result, -1) {
		out[mt[1]] = mt[2]
	}
	return out
}

// askUserQuestionPreviewCap caps an option preview's height so a large ASCII mockup
// can't dominate the view (previewBox clips the rest).
const askUserQuestionPreviewCap = 12

func (m model) askUserQuestionDetail(it transcript.Item, width int) string {
	var in struct {
		Questions []struct {
			Header      string `json:"header"`
			Question    string `json:"question"`
			MultiSelect bool   `json:"multiSelect"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
				Preview     string `json:"preview"`
			} `json:"options"`
		} `json:"questions"`
	}
	unmarshalInput(it.ToolInput, &in)
	if len(in.Questions) == 0 {
		return m.genericToolBody(it, width)
	}
	answers := parseAnsweredAnswers(it.Result)

	blocks := make([]string, 0, len(in.Questions))
	for _, q := range in.Questions {
		var b strings.Builder

		head := StyleAccentBold.Render(Icon.Chat.Glyph + " Claude is asking")
		if q.Header != "" {
			head += "  " + headerChip(q.Header)
		}
		b.WriteString(head + "\n")
		if q.Question != "" {
			b.WriteString(m.renderMD(q.Question, width-2))
		}

		// Split so a multi-select answer ("A, B") matches several option labels;
		// unmatched pieces are custom answers, surfaced on a trailing line.
		chosen := map[string]bool{}
		for _, p := range strings.Split(answers[q.Question], ", ") {
			if p = strings.TrimSpace(p); p != "" {
				chosen[p] = true
			}
		}

		for _, opt := range q.Options {
			isChosen := chosen[opt.Label]
			delete(chosen, opt.Label)

			var mark string
			indent := "  "
			if q.MultiSelect {
				if isChosen {
					mark = "[x] "
				} else {
					mark = "[ ] "
				}
				indent = "    "
			} else if isChosen {
				mark = lipgloss.NewStyle().Foreground(ColorAccent).Render("◉") + " "
			} else {
				mark = StyleDim.Render("○") + " "
			}

			label := StyleSecondary.Render(opt.Label)
			if isChosen {
				label = StylePrimaryBold.Render(opt.Label)
			}
			b.WriteString("\n" + mark + label)
			if d := strings.TrimSpace(opt.Description); d != "" {
				b.WriteString("\n" + indentBlock(wrapDim(d, width-len(indent)), indent))
			}
			if p := strings.TrimSpace(opt.Preview); p != "" {
				box := previewBox(p, width-2, askUserQuestionPreviewCap)
				b.WriteString("\n" + indentBlock(box, indent))
			}
		}

		// Custom answers (no matching option), in stable order.
		for _, p := range strings.Split(answers[q.Question], ", ") {
			p = strings.TrimSpace(p)
			if p != "" && chosen[p] {
				b.WriteString("\n" + StyleSecondaryBold.Render("Answer: ") + StyleSecondary.Render(p))
				delete(chosen, p)
			}
		}

		blocks = append(blocks, b.String())
	}

	return strings.Join(blocks, "\n"+sectionRule(width)+"\n")
}

func (m model) webDetail(it transcript.Item, width int) string {
	var in struct {
		URL    string `json:"url"`
		Prompt string `json:"prompt"`
		Query  string `json:"query"`
	}
	unmarshalInput(it.ToolInput, &in)
	head := in.URL
	if head == "" {
		head = in.Query
	}
	var sb strings.Builder
	if head != "" {
		sb.WriteString(StyleSecondaryBold.Render(head))
	}
	if in.Prompt != "" {
		sb.WriteString("\n" + StyleDim.Render("# "+in.Prompt))
	}
	if it.Result != "" {
		sb.WriteString("\n" + sectionRule(width) + "\n")
		sb.WriteString(m.renderToolText(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}
