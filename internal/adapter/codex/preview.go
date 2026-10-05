package codex

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/MunifTanjim/argus/internal/codextool"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// toolPreview is the one-line transcript row summary for a Codex tool call.
// Clients only see this in rows: the wire form strips ToolInput.
func toolPreview(name, input string) string {
	switch name {
	case "exec_command":
		var in struct {
			Cmd string `json:"cmd"`
		}
		_ = json.Unmarshal([]byte(input), &in)
		return previewLine(in.Cmd)
	case "apply_patch":
		files, _ := codextool.ParsePatch(input)
		return codextool.PatchSummary(files)
	case "update_plan":
		var in struct {
			Plan []struct {
				Step   string `json:"step"`
				Status string `json:"status"`
			} `json:"plan"`
		}
		_ = json.Unmarshal([]byte(input), &in)
		for _, p := range in.Plan {
			if p.Status == "in_progress" {
				return previewLine(p.Step)
			}
		}
		if n := len(in.Plan); n > 0 {
			return fmt.Sprintf("%d steps", n)
		}
	case "web_search":
		var in struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal([]byte(input), &in)
		return previewLine(in.Query)
	case "view_image":
		var in struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal([]byte(input), &in)
		return in.Path
	case "exec":
		return codeModePreview(input)
	case "request_user_input":
		var in struct {
			Questions []struct {
				Header   string `json:"header"`
				Question string `json:"question"`
			} `json:"questions"`
		}
		_ = json.Unmarshal([]byte(input), &in)
		if len(in.Questions) > 0 {
			q := in.Questions[0]
			first := q.Question
			if first == "" {
				first = q.Header
			}
			return withMore(previewLine(first), len(in.Questions)-1)
		}
	case "request_user_input_async":
		var in struct {
			Questions []struct {
				Title string `json:"title"`
			} `json:"questions"`
		}
		_ = json.Unmarshal([]byte(input), &in)
		if len(in.Questions) > 0 {
			return withMore(previewLine(in.Questions[0].Title), len(in.Questions)-1)
		}
	default:
		if _, ok := transcript.MCPDisplayName(name); ok {
			return mcpArgsPreview(input)
		}
	}
	return ""
}

// mcpArgsPreview summarizes MCP tool arguments the way Claude's MCP rows do: a
// well-known argument, else the first string argument by key.
func mcpArgsPreview(input string) string {
	var args map[string]json.RawMessage
	if json.Unmarshal([]byte(input), &args) != nil {
		return ""
	}
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(args[k], &s)
		return previewLine(s)
	}
	for _, k := range []string{"name", "path", "file", "query", "command"} {
		if s := str(k); s != "" {
			return s
		}
	}
	keys := slices.Sorted(maps.Keys(args))
	for _, k := range keys {
		if s := str(k); s != "" {
			return s
		}
	}
	return ""
}

func previewLine(s string) string {
	return strings.TrimSpace(firstLine(s))
}

func withMore(s string, more int) string {
	if more > 0 {
		return fmt.Sprintf("%s +%d more", s, more)
	}
	return s
}

// resultIsError reports whether a tool result signals failure; known is false
// when the output carries no status marker.
func resultIsError(name, output string) (isErr, known bool) {
	// Codex rejected the call before running it (bad arguments).
	if strings.HasPrefix(output, "failed to parse function arguments") {
		return true, true
	}
	switch name {
	case "apply_patch":
		if code, ok := exitCodeAfter(output, "Exit code: "); ok {
			return code != 0, true
		}
		// Older Codex recorded JSON with metadata.exit_code.
		var j struct {
			Metadata struct {
				ExitCode *int `json:"exit_code"`
			} `json:"metadata"`
		}
		if json.Unmarshal([]byte(output), &j) == nil && j.Metadata.ExitCode != nil {
			return *j.Metadata.ExitCode != 0, true
		}
		// A successful run carries "Exit code: 0" or starts with "Success.";
		// verification failures come back as bare text with no header.
		t := strings.TrimSpace(output)
		if strings.HasPrefix(t, "Success.") {
			return false, true
		}
		if t != "" {
			return true, true
		}
		return false, false
	case "request_user_input", "request_user_input_async":
		// An answered question returns a JSON object; Codex rejects a malformed
		// call with plain text.
		var obj map[string]json.RawMessage
		return json.Unmarshal([]byte(output), &obj) != nil, true
	case "exec":
		return strings.HasPrefix(output, "Script failed") || strings.HasPrefix(output, "aborted"), true
	}
	if code, ok := execExitCode(output); ok {
		return code != 0, true
	}
	return false, false
}

// asyncQuestionText renders request_user_input_async questions as markdown, the
// way the Codex TUI shows an accepted call: an assistant message, not a tool.
// ok is false when the input has no questions.
func asyncQuestionText(input string) (text string, ok bool) {
	var in struct {
		Questions []struct {
			Title   string   `json:"title"`
			Options []string `json:"options"`
		} `json:"questions"`
	}
	_ = json.Unmarshal([]byte(input), &in)
	var parts []string
	for _, q := range in.Questions {
		part := "**" + strings.TrimSpace(q.Title) + "**"
		if len(q.Options) > 0 {
			part += "\n\n- " + strings.Join(q.Options, "\n- ")
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n\n"), true
}

// inlineAcceptedAsyncQuestion turns an accepted request_user_input_async call
// into assistant text, matching how the Codex TUI shows it. It reports whether
// it did, so the caller can drop the call from its turn's tool count.
func inlineAcceptedAsyncQuestion(e *transcript.Entry, output string) bool {
	if e == nil || e.ToolName != "request_user_input_async" || strings.TrimSpace(output) != `{"accepted":true}` {
		return false
	}
	text, ok := asyncQuestionText(e.ToolInput)
	if !ok {
		return false
	}
	*e = transcript.Entry{Kind: transcript.EntryText, Timestamp: e.Timestamp, Text: text, ID: e.ID}
	return true
}
