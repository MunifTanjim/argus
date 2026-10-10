package codex

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type rolloutLine struct {
	Timestamp string         `json:"timestamp"`
	Type      string         `json:"type"` // session_meta | turn_context | response_item | event_msg
	Payload   rolloutPayload `json:"payload"`
}

// rolloutPayload is the union of all payload shapes; unused fields stay zero.
type rolloutPayload struct {
	// response_item + event_msg discriminator ("" for session_meta/turn_context).
	Type string `json:"type"`

	Role    string           `json:"role"`
	Content []rolloutContent `json:"content"`
	// agent_message (multi-agent v2): the agent path that sent it.
	Author string `json:"author"`

	// Polymorphic: array for reasoning, "auto" for turn_context; decoded per use.
	Summary json.RawMessage `json:"summary"`

	// function_call / function_call_output; polymorphic, decoded per use.
	Name      string          `json:"name"`
	Namespace string          `json:"namespace"`
	Arguments json.RawMessage `json:"arguments"`
	CallID    string          `json:"call_id"`
	Output    json.RawMessage `json:"output"`

	// custom_tool_call: input is a plain string, unlike function_call's JSON-encoded arguments.
	Input string `json:"input"`

	// web_search_call: action is a plain JSON object, stored as ToolInput directly.
	Action json.RawMessage `json:"action"`

	// turn_context / session_meta
	Model    string `json:"model"`
	ThreadID string `json:"turn_id"` // turn_context turn id
	ID       string `json:"id"`
	Cwd      string `json:"cwd"`
	// session_meta of a forked thread; a subagent's thread_source is "subagent".
	ForkedFromID string `json:"forked_from_id"`
	ThreadSource string `json:"thread_source"`
	// event_msg thread_settings_applied
	EventThreadID string `json:"thread_id"`
	// response_item metadata; the daemon lists items by turn.
	Meta struct {
		TurnID string `json:"turn_id"`
	} `json:"internal_chat_message_metadata_passthrough"`

	// event_msg token_count
	Info *tokenInfo `json:"info"`
	// event_msg task_complete
	DurationMs int64 `json:"duration_ms"`
	// event_msg item_completed
	Item *rolloutItem `json:"item"`
	// event_msg agent/user text (unused; response_item is canonical)
	Message string `json:"message"`
}

type rolloutContent struct {
	Type string `json:"type"` // input_text | output_text
	Text string `json:"text"`
}

type rolloutSummary struct {
	Type string `json:"type"` // summary_text
	Text string `json:"text"`
}

type tokenInfo struct {
	Total              tokenUsage `json:"total_token_usage"`
	Last               tokenUsage `json:"last_token_usage"`
	ModelContextWindow int        `json:"model_context_window"`
}

type tokenUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	TotalTokens       int `json:"total_tokens"`
}

// scanRollout decodes a rollout JSONL file, skipping malformed lines. A missing
// file yields (nil, nil).
func scanRollout(path string) ([]rolloutLine, error) {
	lines, _, err := scanRolloutFrom(path, 0)
	return lines, err
}

// scanRolloutFrom reads complete lines appended after offset, deferring a trailing
// partial line (no \n yet) for the next call. A missing file yields (nil, offset, nil).
// Assumes every entry ends with \n (codex always terminates records); a file's final
// line lacking one is treated as still-being-written.
func scanRolloutFrom(path string, offset int64) ([]rolloutLine, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, offset, nil
		}
		return nil, offset, err
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, offset, err
	}

	lastNL := bytes.LastIndexByte(data, '\n')
	if lastNL < 0 {
		return nil, offset, nil // no complete line appended
	}
	newOffset := offset + int64(lastNL) + 1

	var out []rolloutLine
	for _, line := range bytes.Split(data[:lastNL+1], []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rl rolloutLine
		if json.Unmarshal(line, &rl) != nil {
			continue
		}
		out = append(out, rl)
	}
	return out, newOffset, nil
}

// ownLines drops the parent history a forked subagent's rollout starts with: the
// lines from the copied parent session_meta up to the child's
// thread_settings_applied event. Other rollouts, and a subagent rollout without
// that marker, are returned whole.
func ownLines(lines []rolloutLine) []rolloutLine {
	if len(lines) == 0 || lines[0].Type != "session_meta" {
		return lines
	}
	meta := lines[0].Payload
	if meta.ForkedFromID == "" || meta.ThreadSource != "subagent" {
		return lines
	}
	for i := 1; i < len(lines); i++ {
		p := lines[i].Payload
		if lines[i].Type == "event_msg" && p.Type == "thread_settings_applied" && p.EventThreadID == meta.ID {
			return append(lines[:1:1], lines[i+1:]...)
		}
	}
	return lines
}

// rolloutThreadID is the thread id a rollout file is named after:
// rollout-<timestamp>-<uuid>.jsonl.
func rolloutThreadID(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if n := len(stem); n > 36 && stem[n-37] == '-' {
		return stem[n-36:]
	}
	return ""
}
