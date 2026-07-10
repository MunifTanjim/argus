package antigravity

import (
	"bytes"
	"encoding/json"
	"io"
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
	lines, _, err := scanTranscriptFrom(path, 0)
	return lines, err
}

// scanTranscriptFrom reads complete lines appended after offset, deferring a
// trailing partial line (no \n yet) for the next call. A missing file yields
// (nil, offset, nil). Assumes every entry ends with \n (agy always terminates
// records); a file's final line lacking one is treated as still-being-written.
func scanTranscriptFrom(path string, offset int64) ([]line, int64, error) {
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

	var out []line
	for _, raw := range bytes.Split(data[:lastNL+1], []byte{'\n'}) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		var l line
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		out = append(out, l)
	}
	return out, newOffset, nil
}
