package antigravity

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// parseTranscript reads transcript_full.jsonl.
func parseTranscript(path string, finished bool) ([]transcript.Entry, error) {
	lines, err := scanTranscript(path)
	if err != nil {
		return nil, err
	}
	return foldTranscript(lines, finished), nil
}

// foldTranscript is pure in its input, so streaming can re-fold an
// accumulating line slice each Refresh. A tool_call's result is the adjacent
// non-PLANNER/non-USER line. finished closes the final turn (history reads).
func foldTranscript(lines []line, finished bool) []transcript.Entry {
	var out []transcript.Entry
	var turn transcript.Turn
	pendingIdx := -1 // index into out of a tool entry awaiting its result line
	toolSeq := 0

	add := func(e transcript.Entry) {
		e.ID = "e" + strconv.Itoa(len(out))
		out = append(out, e)
	}
	addToTurn := func(e transcript.Entry) {
		turn.Count(e)
		add(e)
	}
	closeTurn := func() {
		if f, ok := turn.Close(); ok {
			add(f)
		}
	}

	for _, l := range lines {
		switch l.Type {
		case "USER_INPUT":
			closeTurn()
			pendingIdx = -1
			add(transcript.Entry{Kind: transcript.EntryUser, Timestamp: l.CreatedAt, Text: stripUserWrappers(l.Content)})
		case "PLANNER_RESPONSE":
			pendingIdx = -1 // a new step: the prior tool_call (if any) got no result
			if turn.Footer == nil {
				turn.Open(transcript.Entry{Kind: transcript.EntryTurnEnd})
			}
			if l.CreatedAt != "" {
				turn.Footer.Timestamp = l.CreatedAt
			}
			if strings.TrimSpace(l.Thinking) != "" {
				addToTurn(transcript.Entry{Kind: transcript.EntryThinking, Timestamp: l.CreatedAt, Text: l.Thinking})
			}
			if strings.TrimSpace(l.Content) != "" {
				addToTurn(transcript.Entry{Kind: transcript.EntryText, Timestamp: l.CreatedAt, Text: l.Content})
			}
			if len(l.ToolCalls) > 0 {
				tc := l.ToolCalls[0]
				addToTurn(transcript.Entry{
					Kind:         transcript.EntryTool,
					Timestamp:    l.CreatedAt,
					ToolName:     tc.Name,
					ToolID:       "t" + strconv.Itoa(toolSeq),
					ToolInput:    string(tc.Args),
					InputPreview: toolPreview(tc.Args),
				})
				toolSeq++
				pendingIdx = len(out) - 1
			}
		default:
			// Any non-role line right after a tool_call is that tool's result;
			// otherwise it is scaffolding and is skipped.
			if pendingIdx >= 0 {
				e := &out[pendingIdx]
				e.Result = l.Content
				e.ResultIsError = l.Type == "ERROR_MESSAGE"
				if e.ToolName == "invoke_subagent" {
					linkSubagent(e)
				}
				pendingIdx = -1
			}
		}
	}
	if finished {
		closeTurn()
	}
	return out
}

// stripUserWrappers extracts the text from agy's <USER_REQUEST>...</USER_REQUEST>
// wrapper, stripping <ADDITIONAL_METADATA>.
func stripUserWrappers(s string) string {
	if i := strings.Index(s, "<USER_REQUEST>"); i >= 0 {
		rest := s[i+len("<USER_REQUEST>"):]
		if j := strings.Index(rest, "</USER_REQUEST>"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	if i := strings.Index(s, "<ADDITIONAL_METADATA>"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// toolPreview returns a short label from a tool call's toolSummary or toolAction.
func toolPreview(args json.RawMessage) string {
	var a struct {
		ToolSummary string `json:"toolSummary"`
		ToolAction  string `json:"toolAction"`
	}
	_ = json.Unmarshal(args, &a)
	if a.ToolSummary != "" {
		return a.ToolSummary
	}
	return a.ToolAction
}
