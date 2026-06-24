package claudecode

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// appendLine appends one JSONL line (with newline) to path.
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

func TestStreamingTranscriptMatchesOneShot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	// Use array content blocks — the same shape as chunk_test.go fixtures —
	// so that user and assistant messages classify into real chunks.
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2025-01-15T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"Hello"}]}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2025-01-15T10:00:01Z","message":{"role":"assistant","model":"claude","content":[{"type":"text","text":"Hi"}]}}`,
		`{"type":"user","uuid":"u2","timestamp":"2025-01-15T10:00:02Z","message":{"role":"user","content":[{"type":"text","text":"More"}]}}`,
	}

	st := NewStreamingTranscript(path, false)
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	for i, line := range lines {
		appendLine(t, path, line)
		got, err := st.Refresh()
		if err != nil {
			t.Fatal(err)
		}
		want, err := ReadStreamingView(path)
		if err != nil {
			t.Fatal(err)
		}
		// Guard: first append must produce non-empty chunks so the equivalence
		// test is meaningful (not vacuously comparing empty to empty).
		if i == 0 && len(want) == 0 {
			t.Fatalf("ReadStreamingView returned empty chunks after first append — fixture shape is wrong")
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("after appending %q:\n incremental = %+v\n one-shot    = %+v", line, got, want)
		}
	}
}

func TestStreamingTranscriptTruncationResets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	appendLine(t, path, `{"type":"user","uuid":"u1","timestamp":"2025-01-15T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}`)
	appendLine(t, path, `{"type":"user","uuid":"u2","timestamp":"2025-01-15T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"second"}]}}`)

	st := NewStreamingTranscript(path, false)
	if _, err := st.Refresh(); err != nil {
		t.Fatal(err)
	}

	// Truncate to a single different line.
	newLine := `{"type":"user","uuid":"u3","timestamp":"2025-01-15T10:00:02Z","message":{"role":"user","content":[{"type":"text","text":"only"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(newLine), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := st.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	want, err := ReadStreamingView(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatalf("ReadStreamingView returned empty chunks after truncation — fixture shape is wrong")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after truncation:\n incremental = %+v\n one-shot = %+v", got, want)
	}
}

// TestStreamingTranscriptMatchesOneShotWithSubagent verifies that
// StreamingTranscript.Refresh() matches ReadStreamingView step-by-step for a
// session that links a subagent, exercising the AgentRefsFromLinks /
// ItemSubagent branch. The subagent file is written upfront; the parent lines
// are appended one at a time.
func TestStreamingTranscriptMatchesOneShotWithSubagent(t *testing.T) {
	// writeSession (defined in chunk_test.go) creates a parent transcript with
	// a linked subagent (agentID "abc123") whose file exists at the expected path.
	// Reuse it directly rather than duplicating the fixture shape.
	parentPath := writeSession(t)

	// The fixture writes the file in one shot; we need to drive Refresh()
	// line-by-line. Read the content back, reset to empty, then re-append.
	content, err := os.ReadFile(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	// Reset the parent file to empty (subagent file stays intact).
	if err := os.WriteFile(parentPath, nil, 0644); err != nil {
		t.Fatal(err)
	}

	lines := splitLines(content)
	st := NewStreamingTranscript(parentPath, false)

	for i, line := range lines {
		appendLine(t, parentPath, line)
		got, err := st.Refresh()
		if err != nil {
			t.Fatalf("step %d Refresh: %v", i, err)
		}
		want, err := ReadStreamingView(parentPath)
		if err != nil {
			t.Fatalf("step %d ReadStreamingView: %v", i, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d: mismatch after appending %q\n incremental = %+v\n one-shot    = %+v", i, line, got, want)
		}
	}

	// Non-vacuity: after all lines are appended the final view must contain a
	// linked ItemSubagent item (AgentID != "" && HasTrace == true).
	final, err := ReadStreamingView(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	var sub *Item
	for ci := range final {
		for ii := range final[ci].Items {
			if final[ci].Items[ii].Kind == ItemSubagent {
				sub = &final[ci].Items[ii]
			}
		}
	}
	if sub == nil {
		t.Fatal("no subagent item found — fixture does not exercise the link branch")
	}
	if sub.AgentID == "" || !sub.HasTrace {
		t.Errorf("subagent item must carry AgentID + HasTrace, got AgentID=%q HasTrace=%v", sub.AgentID, sub.HasTrace)
	}
}

// TestStreamingTranscriptSubagentClearsSidechain verifies that streaming a
// subagent file (every entry isSidechain=true) yields non-empty chunks. The
// live subagent-drilldown path folds the subagent file with isSubagent=true;
// Classify would otherwise drop every entry, leaving an empty trace.
func TestStreamingTranscriptSubagentClearsSidechain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-abc123.jsonl")
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2025-01-15T10:00:00Z","isSidechain":true,"message":{"role":"user","content":[{"type":"text","text":"do the thing"}]}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2025-01-15T10:00:01Z","isSidechain":true,"message":{"role":"assistant","model":"claude","content":[{"type":"text","text":"done"}]}}`,
	}
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}

	st := NewStreamingTranscript(path, true) // isSubagent = true
	for _, line := range lines {
		appendLine(t, path, line)
	}
	got, err := st.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatalf("subagent stream produced no chunks — isSidechain entries were filtered out")
	}
}

// splitLines splits a byte slice into non-empty lines (without trailing newlines).
func splitLines(data []byte) []string {
	var out []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			line := string(data[start:i])
			if line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if start < len(data) {
		if line := string(data[start:]); line != "" {
			out = append(out, line)
		}
	}
	return out
}
