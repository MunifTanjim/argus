package antigravity

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// parseTranscript folds transcript_full.jsonl into display chunks. USER_INPUT starts
// a turn; MODEL lines until the next USER_INPUT form one AI chunk. A tool_call's
// result is the adjacent non-PLANNER/non-USER line.
func parseTranscript(path string) ([]transcript.Chunk, error) {
	lines, err := scanTranscript(path)
	if err != nil {
		return nil, err
	}
	return foldTranscript(lines), nil
}

// foldTranscript folds a flat transcript line list into display chunks. Pure in its
// input, so streaming can re-fold an accumulating line slice each Refresh.
func foldTranscript(lines []line) []transcript.Chunk {
	var chunks []transcript.Chunk
	var ai *transcript.Chunk // current assistant turn
	pendingIdx := -1         // index into ai.Items of a tool item awaiting its result line
	toolSeq := 0
	itemSeq := 0

	flush := func() {
		if ai != nil && len(ai.Items) > 0 {
			chunks = append(chunks, *ai)
		}
		ai = nil
		pendingIdx = -1
	}
	ensureAI := func(ts string) {
		if ai == nil {
			ai = &transcript.Chunk{ID: "c" + strconv.Itoa(len(chunks)), Kind: transcript.ChunkAI, Timestamp: ts}
			itemSeq = 0
		}
	}
	nextItemID := func() string { itemSeq++; return strconv.Itoa(itemSeq) }

	for _, l := range lines {
		switch l.Type {
		case "USER_INPUT":
			flush()
			chunks = append(chunks, transcript.Chunk{
				ID:        "c" + strconv.Itoa(len(chunks)),
				Kind:      transcript.ChunkUser,
				Timestamp: l.CreatedAt,
				Text:      stripUserWrappers(l.Content),
			})
		case "PLANNER_RESPONSE":
			pendingIdx = -1 // a new step: the prior tool_call (if any) got no result
			ensureAI(l.CreatedAt)
			if strings.TrimSpace(l.Thinking) != "" {
				ai.Items = append(ai.Items, transcript.Item{ID: nextItemID(), Kind: transcript.ItemThinking, Text: l.Thinking})
				ai.Thinking++
			}
			if strings.TrimSpace(l.Content) != "" {
				ai.Items = append(ai.Items, transcript.Item{ID: nextItemID(), Kind: transcript.ItemText, Text: l.Content})
			}
			if len(l.ToolCalls) > 0 {
				tc := l.ToolCalls[0]
				it := transcript.Item{
					ID:           nextItemID(),
					Kind:         transcript.ItemTool,
					ToolName:     tc.Name,
					ToolID:       "t" + strconv.Itoa(toolSeq),
					ToolInput:    string(tc.Args),
					InputPreview: toolPreview(tc.Args),
				}
				toolSeq++
				ai.Items = append(ai.Items, it)
				pendingIdx = len(ai.Items) - 1
				ai.ToolCount++
			}
		default:
			// Any non-role line right after a tool_call is that tool's result;
			// otherwise it is scaffolding and is skipped.
			if pendingIdx >= 0 {
				it := &ai.Items[pendingIdx]
				it.Result = l.Content
				it.ResultIsError = l.Type == "ERROR_MESSAGE"
				if it.ToolName == "invoke_subagent" {
					linkSubagent(it)
				}
				pendingIdx = -1
			}
		}
	}
	flush()
	return chunks
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
