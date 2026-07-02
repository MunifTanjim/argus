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

	st := NewStreamingTranscript(path, path, false)
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

	st := NewStreamingTranscript(path, path, false)
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
	st := NewStreamingTranscript(parentPath, parentPath, false)

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
	sa := sub.Subagents[0]
	if sa.ID == "" || !sa.HasTrace {
		t.Errorf("subagent item must carry ID + HasTrace, got ID=%q HasTrace=%v", sa.ID, sa.HasTrace)
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

	st := NewStreamingTranscript(path, path, true) // isSubagent = true
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

func writeNestedStreamFixture(t *testing.T) (root, subA string) {
	t.Helper()
	dir := t.TempDir()
	root = filepath.Join(dir, "sess.jsonl")
	if err := os.WriteFile(root, []byte(
		`{"uuid":"u1","type":"user","timestamp":"2025-06-15T10:00:00Z","message":{"role":"user","content":"go"}}`+"\n"+
			`{"uuid":"a1","type":"assistant","timestamp":"2025-06-15T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-gp","name":"Task","input":{"subagent_type":"general-purpose","description":"d"}}]}}`+"\n"+
			`{"uuid":"r1","type":"user","timestamp":"2025-06-15T10:00:30Z","isMeta":true,"sourceToolUseID":"tool-gp","toolUseResult":{"agentId":"A"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-gp","content":"done"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(dir, "sess", "subagents")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	subA = filepath.Join(sd, "agent-A.jsonl")
	if err := os.WriteFile(subA, []byte(
		`{"uuid":"au1","type":"user","timestamp":"2025-06-15T10:00:02Z","isSidechain":true,"message":{"role":"user","content":"d"}}`+"\n"+
			`{"uuid":"aa1","type":"assistant","timestamp":"2025-06-15T10:00:03Z","isSidechain":true,"message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-ex","name":"Task","input":{"subagent_type":"Explore","description":"e"}}]}}`+"\n"+
			`{"uuid":"ar1","type":"user","timestamp":"2025-06-15T10:00:20Z","isSidechain":true,"isMeta":true,"sourceToolUseID":"tool-ex","toolUseResult":{"agentId":"B"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-ex","content":"f"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "agent-A.meta.json"), []byte(`{"agentType":"general-purpose","spawnDepth":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "agent-B.jsonl"), []byte(
		`{"uuid":"bu1","type":"user","timestamp":"2025-06-15T10:00:04Z","isSidechain":true,"message":{"role":"user","content":"e"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, subA
}

func subagentItem(chunks []Chunk) (Item, bool) {
	for _, c := range chunks {
		for _, it := range c.Items {
			if it.Kind == ItemSubagent {
				return it, true
			}
		}
	}
	return Item{}, false
}

func TestStreaming_NestedSubagentLinked(t *testing.T) {
	root, subA := writeNestedStreamFixture(t)
	st := NewStreamingTranscript(subA, root, true)
	chunks, err := st.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	it, ok := subagentItem(chunks)
	if !ok {
		t.Fatal("no subagent item in streamed agent-A trace")
	}
	sa := it.Subagents[0]
	if sa.ID != "B" || !sa.HasTrace {
		t.Fatalf("nested item ID=%q HasTrace=%v, want B/true", sa.ID, sa.HasTrace)
	}
}

// A still-running subagent has no parent tool_result, so link-based refs miss
// it; the meta.json sidecar links it so the item is drillable mid-run.
func TestStreaming_RunningSubagentLinkedViaMeta(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "sess.jsonl")
	if err := os.WriteFile(root, []byte(
		`{"uuid":"u1","type":"user","timestamp":"2025-06-15T10:00:00Z","message":{"role":"user","content":"go"}}`+"\n"+
			`{"uuid":"a1","type":"assistant","timestamp":"2025-06-15T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-run","name":"Task","input":{"subagent_type":"Explore","description":"d"}}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(dir, "sess", "subagents")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "agent-R.jsonl"), []byte(
		`{"uuid":"ru1","type":"user","timestamp":"2025-06-15T10:00:02Z","isSidechain":true,"message":{"role":"user","content":"d"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "agent-R.meta.json"),
		[]byte(`{"agentType":"Explore","description":"d","toolUseId":"tool-run"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	st := NewStreamingTranscript(root, root, false)
	chunks, err := st.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	it, ok := subagentItem(chunks)
	if !ok {
		t.Fatal("no subagent item found")
	}
	sa := it.Subagents[0]
	if sa.ID != "R" || !sa.HasTrace {
		t.Fatalf("running subagent ID=%q HasTrace=%v, want R/true", sa.ID, sa.HasTrace)
	}
}

func TestStreaming_DepthCapSuppressesLink(t *testing.T) {
	root, subA := writeNestedStreamFixture(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(subA), "agent-A.meta.json"),
		[]byte(`{"agentType":"general-purpose","spawnDepth":5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewStreamingTranscript(subA, root, true)
	chunks, _ := st.Refresh()
	it, _ := subagentItem(chunks)
	sa := it.Subagents[0]
	if sa.ID != "" || sa.HasTrace {
		t.Fatalf("capped item ID=%q HasTrace=%v, want empty/false", sa.ID, sa.HasTrace)
	}
}
