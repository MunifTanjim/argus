package tui

import (
	"encoding/json"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/codextool"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// applyPatchDetail renders a Codex patch per file: an A/M/D header, then its
// lines colored as a diff. The result is shown only on failure.
func (m model) applyPatchDetail(it transcript.Entry, width int) string {
	var sb strings.Builder
	files, ok := codextool.ParsePatch(it.ToolInput)
	if !ok {
		if it.ToolInput != "" {
			sb.WriteString(m.renderToolText(it.ToolInput, width) + "\n")
		}
	} else {
		add := lipgloss.NewStyle().Foreground(ColorDiffAdd)
		del := lipgloss.NewStyle().Foreground(ColorDiffDel)
		for i, f := range files {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(hardWrap(StyleSecondaryBold.Render(patchHeader(f)), width) + "\n")
			for _, l := range f.Lines {
				var line string
				switch l.Kind {
				case '+':
					line = add.Render("+" + l.Text)
				case '-':
					line = del.Render("-" + l.Text)
				case '@':
					line = StyleDim.Render(strings.TrimSpace("@@ " + l.Text))
				default:
					line = StyleDim.Render(" " + l.Text)
				}
				sb.WriteString(hardWrap(line, width) + "\n")
			}
		}
	}
	if it.ResultIsError && it.Result != "" {
		body := it.Result
		if _, after, found := strings.Cut(body, "Output:\n"); found {
			body = after
		}
		resultSection(&sb, it, width, m.renderToolText(body, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func patchHeader(f codextool.PatchFile) string {
	switch f.Op {
	case codextool.PatchAdd:
		return "A " + f.Path
	case codextool.PatchDelete:
		return "D " + f.Path
	}
	if f.MoveTo != "" {
		return "M " + f.Path + " → " + f.MoveTo
	}
	return "M " + f.Path
}

// codexExecDetail renders a code-mode script (JavaScript) and its result, split
// like exec_command at the "Output:" marker.
func (m model) codexExecDetail(it transcript.Entry, width int) string {
	var sb strings.Builder
	if it.ToolInput != "" {
		sb.WriteString(m.renderJS(it.ToolInput, width) + "\n")
	}
	if it.Result != "" {
		if strings.HasPrefix(it.Result, "aborted") {
			resultSection(&sb, it, width, m.renderToolText(it.Result, width))
		} else {
			resultSection(&sb, it, width, m.execCommandResultBody(it.Result, width))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// codexQuestionDetail renders request_user_input questions, reading the chosen
// option and note from the id-keyed result.
func (m model) codexQuestionDetail(it transcript.Entry, width int) string {
	var res struct {
		Answers map[string]struct {
			Answers []string `json:"answers"`
		} `json:"answers"`
	}
	_ = json.Unmarshal([]byte(it.Result), &res)
	return m.renderQuestions(it, width, "Codex", func(q askedQuestion) ([]string, string) {
		label, note := codextool.SplitAnswer(res.Answers[q.ID].Answers)
		return []string{label}, note
	})
}

// codexAsyncQuestionDetail renders request_user_input_async questions; the user
// answers them in a later message, so there is no answer to mark.
func (m model) codexAsyncQuestionDetail(it transcript.Entry, width int) string {
	var in struct {
		Questions []struct {
			Title   string   `json:"title"`
			Options []string `json:"options"`
		} `json:"questions"`
	}
	unmarshalInput(it.ToolInput, &in)
	if len(in.Questions) == 0 {
		return m.genericToolBody(it, width)
	}
	var sb strings.Builder
	for i, q := range in.Questions {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(hardWrap(StylePrimaryBold.Render(q.Title), width) + "\n")
		for _, o := range q.Options {
			sb.WriteString(hardWrap(StyleSecondary.Render("• "+o), width) + "\n")
		}
	}
	if it.ResultIsError && it.Result != "" {
		resultSection(&sb, it, width, m.renderToolText(it.Result, width))
		return sb.String()
	}
	sb.WriteString("\n" + StyleDim.Render("Asked without waiting; answered in a later message."))
	return sb.String()
}

func (m model) viewImageDetail(it transcript.Entry, width int) string {
	var in struct {
		Path   string `json:"path"`
		Detail string `json:"detail"`
	}
	unmarshalInput(it.ToolInput, &in)
	var sb strings.Builder
	if in.Path != "" {
		head := "path: " + in.Path
		if in.Detail != "" {
			head += "\ndetail: " + in.Detail
		}
		sb.WriteString(dumpLines(head, width) + "\n")
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width) + "\n")
	}
	switch {
	case strings.HasPrefix(it.Result, "[image"):
		sb.WriteString(StyleDim.Render("image attached"))
	case it.Result != "":
		resultSection(&sb, it, width, m.renderToolText(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func resultSection(sb *strings.Builder, it transcript.Entry, width int, body string) {
	if sb.Len() > 0 {
		sb.WriteString(sectionRule(width) + "\n")
	}
	sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
	sb.WriteString(body)
}

// codexWaitCellDetail renders code mode's wait on a running exec cell; the
// result reports the cell like exec does.
func (m model) codexWaitCellDetail(it transcript.Entry, width int) string {
	var in struct {
		CellID      string `json:"cell_id"`
		YieldTimeMs int64  `json:"yield_time_ms"`
	}
	unmarshalInput(it.ToolInput, &in)
	var sb strings.Builder
	if in.CellID != "" {
		sb.WriteString(StyleSecondaryBold.Render("Waiting on cell ") + in.CellID)
		if in.YieldTimeMs > 0 {
			sb.WriteString(StyleDim.Render("  (yield " + codextool.MsDuration(in.YieldTimeMs) + ")"))
		}
		sb.WriteString("\n")
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width) + "\n")
	}
	if it.Result != "" {
		resultSection(&sb, it, width, m.execCommandResultBody(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) codexSleepDetail(it transcript.Entry, width int) string {
	var in struct {
		DurationMs int64 `json:"duration_ms"`
	}
	unmarshalInput(it.ToolInput, &in)
	var sb strings.Builder
	if in.DurationMs > 0 {
		sb.WriteString(StyleSecondaryBold.Render("Sleep ") + codextool.MsDuration(in.DurationMs) + "\n")
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width) + "\n")
	}
	if it.Result != "" {
		resultSection(&sb, it, width, m.renderToolText(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// codexV2WaitAgentDetail renders multi-agent v2's wait_agent, which waits on any
// agent: the input is only a timeout.
func (m model) codexV2WaitAgentDetail(it transcript.Entry, width int) string {
	var in struct {
		TimeoutMs int64 `json:"timeout_ms"`
	}
	unmarshalInput(it.ToolInput, &in)
	var sb strings.Builder
	sb.WriteString(StyleSecondaryBold.Render("Waiting on agents"))
	if in.TimeoutMs > 0 {
		sb.WriteString(StyleDim.Render("  (timeout " + codextool.MsDuration(in.TimeoutMs) + ")"))
	}
	sb.WriteString("\n")
	if it.Result != "" {
		var res struct {
			Message  string `json:"message"`
			TimedOut bool   `json:"timed_out"`
		}
		var body string
		if json.Unmarshal([]byte(it.Result), &res) == nil && (res.Message != "" || res.TimedOut) {
			body = m.renderMD(res.Message, width)
			if res.TimedOut {
				body = strings.TrimRight(StyleDim.Render("timed out")+"\n"+body, "\n")
			}
		} else {
			body = m.renderToolText(it.Result, width)
		}
		resultSection(&sb, it, width, body)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// codexAgentMessageDetail renders multi-agent v2's send_message and
// followup_task: a message to the agent at target. Codex may send the message
// encrypted, in which case only the target is readable.
func (m model) codexAgentMessageDetail(it transcript.Entry, width int) string {
	var in struct {
		Target  string `json:"target"`
		Message string `json:"message"`
	}
	unmarshalInput(it.ToolInput, &in)
	if in.Target == "" {
		return m.genericToolBody(it, width)
	}
	var sb strings.Builder
	sb.WriteString(StyleSecondaryBold.Render("To ") + in.Target + "\n")
	switch {
	case strings.HasPrefix(in.Message, codextool.EncryptedPrefix):
		sb.WriteString(StyleDim.Render("message encrypted") + "\n")
	case in.Message != "":
		sb.WriteString(m.renderMD(in.Message, width) + "\n")
	}
	if it.Result != "" {
		resultSection(&sb, it, width, m.renderToolText(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) codexInterruptAgentDetail(it transcript.Entry, width int) string {
	var in struct {
		Target string `json:"target"`
	}
	unmarshalInput(it.ToolInput, &in)
	if in.Target == "" {
		return m.genericToolBody(it, width)
	}
	var sb strings.Builder
	sb.WriteString(StyleSecondaryBold.Render("Interrupted ") + in.Target + "\n")
	if it.Result != "" {
		var res struct {
			PreviousStatus json.RawMessage `json:"previous_status"`
		}
		if json.Unmarshal([]byte(it.Result), &res) == nil && len(res.PreviousStatus) > 0 {
			resultSection(&sb, it, width, m.agentStatusBlock("Previous status", res.PreviousStatus, width))
		} else {
			resultSection(&sb, it, width, m.renderToolText(it.Result, width))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) codexListAgentsDetail(it transcript.Entry, width int) string {
	var in struct {
		PathPrefix string `json:"path_prefix"`
	}
	unmarshalInput(it.ToolInput, &in)
	var sb strings.Builder
	if in.PathPrefix != "" {
		sb.WriteString(StyleSecondaryBold.Render("Agents under ") + in.PathPrefix + "\n")
	}
	if it.Result != "" {
		var res struct {
			Agents []struct {
				Name   string          `json:"agent_name"`
				Status json.RawMessage `json:"agent_status"`
			} `json:"agents"`
		}
		var body string
		switch {
		case json.Unmarshal([]byte(it.Result), &res) != nil || res.Agents == nil:
			body = m.renderToolText(it.Result, width)
		case len(res.Agents) == 0:
			body = StyleDim.Render("no agents")
		default:
			blocks := make([]string, len(res.Agents))
			for i, a := range res.Agents {
				blocks[i] = m.agentStatusBlock(a.Name, a.Status, width)
			}
			body = strings.Join(blocks, "\n")
		}
		resultSection(&sb, it, width, body)
	}
	return strings.TrimRight(sb.String(), "\n")
}
