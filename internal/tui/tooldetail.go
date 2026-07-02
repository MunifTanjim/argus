package tui

import (
	"encoding/json"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// fetchToolBodyCmd lazily fetches one tool item's heavy body (input/result), keyed
// by tool_use id; agentID selects the subagent trace ("" = main transcript). The
// cache entry is marked loading to coalesce repeats; nil when not addressable or
// already loaded/loading. Live vs history RPC is chosen by the view mode.
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

// toolDetailBody renders a tool-specific body at the given inner width, returning
// ok=false for tools with no custom renderer (caller then uses genericToolBody).
func (m model) toolDetailBody(it transcript.Item, width int) (string, bool) {
	switch it.ToolName {
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return m.editToolDetail(it, width), true
	case "Bash":
		return m.bashDetail(it, width), true
	// BashOutput/KillShell carry a shell id, not a command, so they use the
	// generic Input/Result layout (default branch).
	case "Read", "NotebookRead":
		return m.readDetail(it, width), true
	case "TodoWrite":
		return m.todoDetail(it, width), true
	case "Grep":
		return m.grepDetail(it, width), true
	case "Glob", "LS":
		return m.globDetail(it, width), true
	case "WebFetch", "WebSearch":
		return m.webDetail(it, width), true
	case "AskUserQuestion":
		return m.askUserQuestionDetail(it, width), true
	default:
		return "", false
	}
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

// resultLabelText is "Result" or "Error" for a tool result.
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

// genericToolBody is the default Input/Result(/Error) layout.
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

// editToolDetail renders Edit/Write/MultiEdit as a colored diff plus the result.
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

// bashDetail renders a Bash command as "$ cmd" (with optional "# description")
// followed by the result.
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

// readDetail renders a file read: path header, then content (which already carries
// line numbers from the Read tool).
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

// todoDetail renders a TodoWrite list with status glyphs. In-progress items show
// their activeForm; others show content.
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

// grepDetail renders a search query header ("pattern" in path) then the matches.
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

// globDetail renders a glob/LS pattern header then the matched paths.
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

// askUserQuestionPair matches the `"question"="answer"` pairs in an answered
// AskUserQuestion result string.
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

// askUserQuestionDetail renders an answered AskUserQuestion: header chip + prompt,
// options with the chosen one(s) marked (◉/[x]), and an "Answer" line for any
// custom answer matching no option. Mirrors the live prompt dock (prompt.go).
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

// webDetail renders a WebFetch URL or WebSearch query header then the result.
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
