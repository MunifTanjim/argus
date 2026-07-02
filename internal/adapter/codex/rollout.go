package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
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

	// event_msg token_count
	Info *tokenInfo `json:"info"`
	// event_msg task_complete
	DurationMs int64 `json:"duration_ms"`
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
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // base_instructions lines are long
	var out []rolloutLine
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rl rolloutLine
		if json.Unmarshal(line, &rl) != nil {
			continue
		}
		out = append(out, rl)
	}
	return out, sc.Err()
}
