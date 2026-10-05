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
		if sb.Len() > 0 {
			sb.WriteString(sectionRule(width) + "\n")
		}
		sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
		sb.WriteString(m.renderToolText(it.Result, width))
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
