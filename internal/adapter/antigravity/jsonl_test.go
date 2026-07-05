package antigravity

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLines(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "transcript_full.jsonl")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestScanTranscriptToleratesBlankAndMalformed(t *testing.T) {
	content := `{"type":"USER_INPUT","content":"hi","step_index":0}

not json
{"type":"PLANNER_RESPONSE","content":"hello","step_index":2}
`
	got, err := scanTranscript(writeLines(t, content))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 lines (blank + malformed skipped), got %d", len(got))
	}
	if got[0].Type != "USER_INPUT" || got[1].Type != "PLANNER_RESPONSE" {
		t.Fatalf("wrong lines: %+v", got)
	}
}

func TestScanTranscriptMissingFile(t *testing.T) {
	got, err := scanTranscript(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("missing file should be (nil,nil), got err %v", err)
	}
	if got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestScanTranscriptToolCall(t *testing.T) {
	content := `{"type":"PLANNER_RESPONSE","tool_calls":[{"name":"run_command","args":{"CommandLine":"echo hi","toolSummary":"Run echo"}}],"step_index":1}
`
	got, _ := scanTranscript(writeLines(t, content))
	if len(got) != 1 || len(got[0].ToolCalls) != 1 || got[0].ToolCalls[0].Name != "run_command" {
		t.Fatalf("tool_call not parsed: %+v", got)
	}
}
