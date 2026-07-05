package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// Strips the "Created At:"/"Completed At:" timestamp lines that prefix every
// antigravity tool result.
func agyResultBody(result string) string {
	lines := strings.Split(result, "\n")
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "Created At:") || strings.HasPrefix(t, "Completed At:") {
			i++
			continue
		}
		break
	}
	return strings.TrimLeft(strings.Join(lines[i:], "\n"), "\n")
}

var agyBoilerplate = []string{
	"If relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission.",
	"Do not output the path of this image to show to the user since the user can already see it. However, you can embed this image in artifacts for the USER's review.",
}

func stripAgyBoilerplate(s string) string {
	for _, b := range agyBoilerplate {
		s = strings.ReplaceAll(s, b, "")
	}
	return strings.TrimSpace(s)
}

func stripAgyReminder(s string) string {
	if i := strings.Index(s, "REMINDER:"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func appendResult(sb *strings.Builder, it transcript.Item, width int, body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	if sb.Len() > 0 {
		sb.WriteString(sectionRule(width) + "\n")
	}
	sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
	sb.WriteString(body)
}

func formatBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fk", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func (m model) runCommandDetail(it transcript.Item, width int) string {
	var in struct {
		CommandLine string `json:"CommandLine"`
		Cwd         string `json:"Cwd"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.CommandLine != "" {
		if in.Cwd != "" {
			sb.WriteString(StyleDim.Render("# "+in.Cwd) + "\n")
		}
		sb.WriteString(StyleSecondaryBold.Render("$ "+in.CommandLine) + "\n")
	} else if it.ToolInput != "" {
		sb.WriteString(dumpLines(prettyJSON(it.ToolInput), width) + "\n")
	}

	if it.Result != "" {
		if sb.Len() > 0 {
			sb.WriteString(sectionRule(width) + "\n")
		}
		sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
		sb.WriteString(m.runCommandResultBody(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) runCommandResultBody(result string, width int) string {
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	outIdx, indent := -1, ""
	for i, ln := range lines {
		trimmed := strings.TrimLeft(ln, "\t")
		if trimmed == "Output:" || trimmed == "Stdout:" || trimmed == "Stderr:" {
			outIdx, indent = i, ln[:len(ln)-len(trimmed)]
			break
		}
	}
	if outIdx < 0 {
		return dumpLines(stripLinePrefix(result, "\t"), width)
	}
	head := strings.Join(stripLines(lines[:outIdx+1], indent), "\n")
	out := dumpLines(head, width)
	if outIdx+1 < len(lines) {
		body := strings.TrimRight(strings.Join(stripLines(lines[outIdx+1:], indent), "\n"), "\n")
		if body != "" {
			out += "\n" + m.renderToolText(body, width)
		}
	}
	return out
}

func stripLines(lines []string, prefix string) []string {
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = strings.TrimPrefix(ln, prefix)
	}
	return out
}

func stripLinePrefix(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimLeft(ln, prefix)
	}
	return strings.Join(lines, "\n")
}

func (m model) grepSearchDetail(it transcript.Item, width int) string {
	var in struct {
		Query           string `json:"Query"`
		SearchPath      string `json:"SearchPath"`
		IsRegex         bool   `json:"IsRegex"`
		CaseInsensitive bool   `json:"CaseInsensitive"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Query != "" {
		sb.WriteString(StyleSecondaryBold.Render(`"` + in.Query + `"`))
		if in.SearchPath != "" {
			sb.WriteString(StyleDim.Render(" in " + in.SearchPath))
		}
		var flags []string
		if in.IsRegex {
			flags = append(flags, "regex")
		}
		if in.CaseInsensitive {
			flags = append(flags, "case-insensitive")
		}
		if len(flags) > 0 {
			sb.WriteString(StyleDim.Render("  (" + strings.Join(flags, " · ") + ")"))
		}
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}

	appendResult(&sb, it, width, m.grepSearchResult(it.Result, width))
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) grepSearchResult(result string, width int) string {
	body := agyResultBody(result)
	if body == "" {
		return ""
	}
	var rows []string
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var e struct {
			File        string `json:"File"`
			LineNumber  int    `json:"LineNumber"`
			LineContent string `json:"LineContent"`
		}
		if json.Unmarshal([]byte(ln), &e) == nil && e.File != "" {
			loc := fmt.Sprintf("%s:%d", e.File, e.LineNumber)
			rows = append(rows, StyleDim.Render(loc)+" "+strings.TrimSpace(e.LineContent))
		} else {
			rows = append(rows, ln)
		}
	}
	return hardWrap(strings.Join(rows, "\n"), width)
}

func (m model) listDirDetail(it transcript.Item, width int) string {
	var in struct {
		DirectoryPath string `json:"DirectoryPath"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.DirectoryPath != "" {
		sb.WriteString(StyleSecondary.Render(in.DirectoryPath))
	}
	appendResult(&sb, it, width, m.listDirResult(it.Result, width))
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) listDirResult(result string, width int) string {
	body := agyResultBody(result)
	if body == "" {
		return ""
	}
	var rows []string
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var e struct {
			Name      string `json:"name"`
			IsDir     bool   `json:"isDir"`
			SizeBytes string `json:"sizeBytes"`
		}
		if json.Unmarshal([]byte(ln), &e) == nil && e.Name != "" {
			if e.IsDir {
				rows = append(rows, StyleSecondary.Render(e.Name+"/"))
				continue
			}
			row := e.Name
			if n, err := strconv.Atoi(e.SizeBytes); err == nil {
				row += "  " + StyleDim.Render(formatBytes(n))
			}
			rows = append(rows, row)
		} else {
			rows = append(rows, ln)
		}
	}
	return hardWrap(strings.Join(rows, "\n"), width)
}

func (m model) viewFileDetail(it transcript.Item, width int) string {
	var in struct {
		AbsolutePath string `json:"AbsolutePath"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.AbsolutePath != "" {
		sb.WriteString(StyleSecondary.Render(in.AbsolutePath) + "\n")
	}
	meta, content := splitViewFileResult(it.Result)
	if meta != "" {
		sb.WriteString(StyleDim.Render(meta) + "\n")
	}
	switch {
	case content != "":
		if sb.Len() > 0 {
			sb.WriteString(sectionRule(width) + "\n")
		}
		sb.WriteString(m.renderToolText(content, width))
	case it.Result != "":
		if sb.Len() > 0 {
			sb.WriteString(sectionRule(width) + "\n")
		}
		sb.WriteString(m.renderToolText(agyResultBody(it.Result), width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func splitViewFileResult(result string) (meta, content string) {
	body := agyResultBody(result)
	if body == "" {
		return "", ""
	}
	lines := strings.Split(body, "\n")
	var metaParts []string
	contentStart := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "Total Lines:"), strings.HasPrefix(t, "Total Bytes:"), strings.HasPrefix(t, "Showing lines"):
			metaParts = append(metaParts, t)
		case strings.HasPrefix(t, "The following code"):
			contentStart = i + 1
		}
		if contentStart >= 0 {
			break
		}
	}
	if contentStart >= 0 && contentStart < len(lines) {
		content = strings.TrimRight(strings.Join(lines[contentStart:], "\n"), "\n")
	}
	return strings.Join(metaParts, " · "), content
}

func (m model) writeToFileDetail(it transcript.Item, width int) string {
	var in struct {
		TargetFile  string `json:"TargetFile"`
		Description string `json:"Description"`
		CodeContent string `json:"CodeContent"`
		Overwrite   bool   `json:"Overwrite"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Description != "" {
		sb.WriteString(StyleDim.Render("# "+in.Description) + "\n")
	}
	if in.TargetFile != "" {
		head := StyleSecondary.Render(in.TargetFile)
		if in.Overwrite {
			head += StyleDim.Render("  (overwrite)")
		}
		sb.WriteString(head + "\n")
	}
	if in.CodeContent != "" {
		sb.WriteString(sectionRule(width) + "\n")
		sb.WriteString(strings.Join(addedLines(in.CodeContent), "\n"))
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}
	appendResult(&sb, it, width, m.renderToolText(stripAgyBoilerplate(agyResultBody(it.Result)), width))
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) replaceFileContentDetail(it transcript.Item, width int) string {
	var in struct {
		TargetFile         string `json:"TargetFile"`
		Description        string `json:"Description"`
		StartLine          int    `json:"StartLine"`
		EndLine            int    `json:"EndLine"`
		TargetContent      string `json:"TargetContent"`
		ReplacementContent string `json:"ReplacementContent"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Description != "" {
		sb.WriteString(StyleDim.Render("# "+in.Description) + "\n")
	}
	if in.TargetFile != "" {
		head := StyleSecondary.Render(in.TargetFile)
		if in.StartLine > 0 || in.EndLine > 0 {
			head += StyleDim.Render(fmt.Sprintf("  (lines %d–%d)", in.StartLine, in.EndLine))
		}
		sb.WriteString(head + "\n")
	}
	if in.TargetContent != "" || in.ReplacementContent != "" {
		sb.WriteString(sectionRule(width) + "\n")
		sb.WriteString(strings.Join(lineDiff(in.TargetContent, in.ReplacementContent), "\n"))
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}
	if it.ResultIsError {
		appendResult(&sb, it, width, m.renderToolText(agyResultBody(it.Result), width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) multiReplaceFileContentDetail(it transcript.Item, width int) string {
	var in struct {
		TargetFile        string `json:"TargetFile"`
		Description       string `json:"Description"`
		ReplacementChunks []struct {
			StartLine          int    `json:"StartLine"`
			EndLine            int    `json:"EndLine"`
			TargetContent      string `json:"TargetContent"`
			ReplacementContent string `json:"ReplacementContent"`
		} `json:"ReplacementChunks"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Description != "" {
		sb.WriteString(StyleDim.Render("# "+in.Description) + "\n")
	}
	if in.TargetFile != "" {
		head := StyleSecondary.Render(in.TargetFile)
		if n := len(in.ReplacementChunks); n > 0 {
			head += StyleDim.Render(fmt.Sprintf("  (%d edits)", n))
		}
		sb.WriteString(head + "\n")
	}
	if len(in.ReplacementChunks) > 0 {
		sb.WriteString(sectionRule(width))
		for i, c := range in.ReplacementChunks {
			sb.WriteString("\n" + StyleDim.Render(fmt.Sprintf("─── edit %d (lines %d–%d) ───", i+1, c.StartLine, c.EndLine)) + "\n")
			sb.WriteString(strings.Join(lineDiff(c.TargetContent, c.ReplacementContent), "\n"))
		}
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}
	if it.ResultIsError {
		appendResult(&sb, it, width, m.renderToolText(agyResultBody(it.Result), width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) searchWebDetail(it transcript.Item, width int) string {
	var in struct {
		Query  string `json:"query"`
		Domain string `json:"domain"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Query != "" {
		sb.WriteString(StyleSecondaryBold.Render(`"` + in.Query + `"`))
		if in.Domain != "" {
			sb.WriteString(StyleDim.Render("  " + in.Domain))
		}
	}
	body := agyResultBody(it.Result)
	if idx := strings.Index(body, "returned the following summary:"); idx >= 0 {
		body = strings.TrimSpace(body[idx+len("returned the following summary:"):])
	}
	if body != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n" + sectionRule(width) + "\n")
		}
		sb.WriteString(m.renderMD(body, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) generateImageDetail(it transcript.Item, width int) string {
	var in struct {
		Prompt      string `json:"Prompt"`
		ImageName   string `json:"ImageName"`
		AspectRatio string `json:"AspectRatio"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.ImageName != "" {
		head := StyleSecondaryBold.Render(in.ImageName)
		if in.AspectRatio != "" {
			head += StyleDim.Render("  (" + in.AspectRatio + ")")
		}
		sb.WriteString(head + "\n")
	}
	if in.Prompt != "" {
		sb.WriteString(StyleDim.Render("# "+in.Prompt) + "\n")
	}
	appendResult(&sb, it, width, m.renderToolText(agyImageResult(it.Result), width))
	return strings.TrimRight(sb.String(), "\n")
}

func agyImageResult(result string) string {
	body := stripAgyBoilerplate(agyResultBody(result))
	var keep []string
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "Using prompt:") {
			continue
		}
		keep = append(keep, t)
	}
	return strings.Join(keep, "\n")
}

func (m model) defineSubagentDetail(it transcript.Item, width int) string {
	var in struct {
		Name                string `json:"name"`
		Description         string `json:"description"`
		SystemPrompt        string `json:"system_prompt"`
		EnableWriteTools    bool   `json:"enable_write_tools"`
		EnableMCPTools      bool   `json:"enable_mcp_tools"`
		EnableSubagentTools bool   `json:"enable_subagent_tools"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Name != "" {
		sb.WriteString(StyleSecondaryBold.Render(in.Name) + "\n")
	}
	if in.Description != "" {
		sb.WriteString(wrapDim(in.Description, width) + "\n")
	}
	var tools []string
	if in.EnableWriteTools {
		tools = append(tools, "write")
	}
	if in.EnableMCPTools {
		tools = append(tools, "mcp")
	}
	if in.EnableSubagentTools {
		tools = append(tools, "subagent")
	}
	label := "none"
	if len(tools) > 0 {
		label = strings.Join(tools, " · ")
	}
	sb.WriteString(StyleDim.Render("tools: " + label))
	if in.SystemPrompt != "" {
		sb.WriteString("\n" + sectionRule(width) + "\n")
		sb.WriteString(StyleSecondaryBold.Render("System Prompt") + "\n")
		sb.WriteString(m.renderMD(in.SystemPrompt, width))
	}
	if it.ResultIsError {
		appendResult(&sb, it, width, m.renderToolText(agyResultBody(it.Result), width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) manageSubagentsDetail(it transcript.Item, width int) string {
	var in struct {
		Action string `json:"Action"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Action != "" {
		sb.WriteString(StyleSecondaryBold.Render(in.Action))
	}
	if body := agyResultBody(it.Result); body != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n" + sectionRule(width) + "\n")
		}
		sb.WriteString(m.renderToolText(body, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) manageTaskDetail(it transcript.Item, width int) string {
	var in struct {
		Action string `json:"Action"`
		TaskID string `json:"TaskId"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Action != "" {
		head := StyleSecondaryBold.Render(in.Action)
		if in.TaskID != "" {
			head += StyleDim.Render("  " + in.TaskID)
		}
		sb.WriteString(head)
	}
	if body := stripAgyReminder(agyResultBody(it.Result)); body != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n" + sectionRule(width) + "\n")
		}
		sb.WriteString(dumpLines(body, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) askQuestionDetail(it transcript.Item, width int) string {
	var in struct {
		Questions []struct {
			Question    string   `json:"question"`
			MultiSelect bool     `json:"is_multi_select"`
			Options     []string `json:"options"`
		} `json:"questions"`
	}
	unmarshalInput(it.ToolInput, &in)
	if len(in.Questions) == 0 {
		return m.genericToolBody(it, width)
	}
	answers := parseAgyAnswers(agyResultBody(it.Result))

	blocks := make([]string, 0, len(in.Questions))
	for qi, q := range in.Questions {
		var b strings.Builder
		b.WriteString(StyleAccentBold.Render(Icon.Chat.Glyph+" Agent is asking") + "\n")
		if q.Question != "" {
			b.WriteString(m.renderMD(q.Question, width-2))
		}
		ans := answers[qi]
		for _, opt := range q.Options {
			isChosen := ans != "" && strings.Contains(ans, opt)
			var mark string
			if q.MultiSelect {
				if isChosen {
					mark = "[x] "
				} else {
					mark = "[ ] "
				}
			} else if isChosen {
				mark = lipgloss.NewStyle().Foreground(ColorAccent).Render("◉") + " "
			} else {
				mark = StyleDim.Render("○") + " "
			}
			label := StyleSecondary.Render(opt)
			if isChosen {
				label = StylePrimaryBold.Render(opt)
			}
			b.WriteString("\n" + mark + label)
		}
		blocks = append(blocks, b.String())
	}
	return strings.Join(blocks, "\n"+sectionRule(width)+"\n")
}

func parseAgyAnswers(result string) map[int]string {
	out := map[int]string{}
	for _, ln := range strings.Split(result, "\n") {
		ln = strings.TrimSpace(ln)
		if len(ln) < 3 || ln[0] != 'A' {
			continue
		}
		colon := strings.IndexByte(ln, ':')
		if colon < 0 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(ln[1:colon]))
		if err != nil {
			continue
		}
		out[n-1] = strings.TrimSpace(ln[colon+1:])
	}
	return out
}

func (m model) askPermissionDetail(it transcript.Item, width int) string {
	var in struct {
		Action string `json:"Action"`
		Target string `json:"Target"`
		Reason string `json:"Reason"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Action != "" {
		title := in.Action
		if in.Target != "" {
			title += "(" + in.Target + ")"
		}
		sb.WriteString(StyleSecondaryBold.Render(title) + "\n")
	}
	if in.Reason != "" {
		sb.WriteString(wrapDim(in.Reason, width))
	}
	appendResult(&sb, it, width, m.renderToolText(agyResultBody(it.Result), width))
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) listPermissionsDetail(it transcript.Item, width int) string {
	body := agyResultBody(it.Result)
	if body == "" {
		return m.genericToolBody(it, width)
	}
	return m.renderToolText(body, width)
}

func (m model) sendMessageDetail(it transcript.Item, width int) string {
	var in struct {
		Message   string `json:"Message"`
		Recipient string `json:"Recipient"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Recipient != "" {
		sb.WriteString(StyleSecondaryBold.Render("→ "+in.Recipient) + "\n")
	}
	if in.Message != "" {
		sb.WriteString(m.renderMD(in.Message, width))
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) scheduleDetail(it transcript.Item, width int) string {
	var in struct {
		DurationSeconds string `json:"DurationSeconds"`
		Prompt          string `json:"Prompt"`
		TimerCondition  string `json:"TimerCondition"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.DurationSeconds != "" {
		head := StyleSecondaryBold.Render("Timer " + in.DurationSeconds + "s")
		if in.TimerCondition != "" {
			head += StyleDim.Render("  (condition: " + in.TimerCondition + ")")
		}
		sb.WriteString(head + "\n")
	}
	if in.Prompt != "" {
		sb.WriteString(StyleDim.Render("# " + in.Prompt))
	}
	appendResult(&sb, it, width, m.renderToolText(agyResultBody(it.Result), width))
	return strings.TrimRight(sb.String(), "\n")
}
