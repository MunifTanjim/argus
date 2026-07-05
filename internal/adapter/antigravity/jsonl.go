package antigravity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
)

// toolCallLine is one tool proposal on a PLANNER_RESPONSE line (agy emits one per line).
type toolCallLine struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// line is one JSON object of transcript_full.jsonl.
// step_index is non-dense/non-monotonic; document order is authoritative.
type line struct {
	Type      string         `json:"type"`
	Content   string         `json:"content"`
	Thinking  string         `json:"thinking"`
	ToolCalls []toolCallLine `json:"tool_calls"`
	Source    string         `json:"source"`
	Status    string         `json:"status"`
	StepIndex int            `json:"step_index"`
	CreatedAt string         `json:"created_at"`
}

// scanTranscript decodes a transcript_full.jsonl, skipping malformed lines. A
// missing file yields (nil, nil).
func scanTranscript(path string) ([]line, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // content/thinking blocks are long
	var out []line
	for sc.Scan() {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var l line
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		out = append(out, l)
	}
	return out, sc.Err()
}
