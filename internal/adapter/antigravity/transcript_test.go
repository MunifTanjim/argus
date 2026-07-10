package antigravity

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestScanTranscriptDropsUnterminatedFinalLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript_full.jsonl")

	terminated := `{"type":"USER_INPUT","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>","step_index":0}` + "\n"
	unterminated := `{"type":"PLANNER_RESPONSE","source":"MODEL","content":"partial","step_index":1}`
	if err := os.WriteFile(path, []byte(terminated+unterminated), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, err := scanTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (unterminated final line dropped)", len(lines))
	}
}

func TestScanTranscriptFromDefersPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript_full.jsonl")

	full := `{"type":"USER_INPUT","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>","step_index":0}
{"type":"PLANNER_RESPONSE","source":"MODEL","content":"ok","step_index":1}
`
	partial := `{"type":"PLANNER_RESPONSE","source":"MODEL","content":"par`
	if err := os.WriteFile(path, []byte(full+partial), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, off, err := scanTranscriptFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (partial deferred)", len(lines))
	}
	if off != int64(len(full)) {
		t.Fatalf("offset = %d, want %d (last newline)", off, len(full))
	}

	rest := `tial done","step_index":2}` + "\n"
	appendFile(t, path, rest)

	lines2, off2, err := scanTranscriptFrom(path, off)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines2) != 1 {
		t.Fatalf("got %d lines, want 1 (completed line)", len(lines2))
	}
	if want := int64(len(full) + len(partial) + len(rest)); off2 != want {
		t.Fatalf("offset = %d, want %d (end of file)", off2, want)
	}
}

func TestStreamingRefreshIncrementalEqualsWholeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript_full.jsonl")

	seg1 := `{"type":"USER_INPUT","content":"<USER_REQUEST>\nlist\n</USER_REQUEST>","step_index":0,"created_at":"2026-07-05T01:00:00Z"}
`
	seg2 := `{"type":"PLANNER_RESPONSE","source":"MODEL","thinking":"think","content":"listing","step_index":1,"created_at":"2026-07-05T01:00:01Z"}
{"type":"PLANNER_RESPONSE","source":"MODEL","tool_calls":[{"name":"list_dir","args":{"DirectoryPath":"/x","toolSummary":"List /x"}}],"step_index":2,"created_at":"2026-07-05T01:00:02Z"}
{"type":"LIST_DIRECTORY","source":"MODEL","content":"- a.go","step_index":3,"created_at":"2026-07-05T01:00:03Z"}
`
	if err := os.WriteFile(path, []byte(seg1), 0o644); err != nil {
		t.Fatal(err)
	}

	st := NewStreamingTranscript(path, "", false)
	if _, err := st.Refresh(); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	appendFile(t, path, seg2)
	got, err := st.Refresh()
	if err != nil {
		t.Fatalf("second Refresh: %v", err)
	}

	want, err := ReadTranscriptView(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want.Chunks) {
		t.Fatalf("incremental chunks != whole-file parse\n got: %+v\nwant: %+v", got, want.Chunks)
	}
}

func TestStreamingRefreshTruncationReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript_full.jsonl")

	big := `{"type":"USER_INPUT","content":"<USER_REQUEST>\nfirst session request line\n</USER_REQUEST>","step_index":0}
{"type":"PLANNER_RESPONSE","source":"MODEL","content":"first session reply","step_index":1}
`
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewStreamingTranscript(path, "", false)
	if _, err := st.Refresh(); err != nil {
		t.Fatal(err)
	}

	small := `{"type":"USER_INPUT","content":"<USER_REQUEST>\nnew\n</USER_REQUEST>","step_index":0}
`
	if err := os.WriteFile(path, []byte(small), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := st.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	want, err := ReadTranscriptView(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want.Chunks) {
		t.Fatalf("after truncation, chunks != whole-file parse\n got: %+v\nwant: %+v", got, want.Chunks)
	}
}

func TestReadTranscriptView(t *testing.T) {
	v, err := ReadTranscriptView(writeLines(t, sampleTranscript))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Chunks) != 2 {
		t.Fatalf("want 2 chunks, got %d", len(v.Chunks))
	}
}

func TestFindToolDetail(t *testing.T) {
	path := writeLines(t, sampleTranscript)
	v, _ := ReadTranscriptView(path)
	var id string
	for _, c := range v.Chunks {
		for _, it := range c.Items {
			if it.ToolName == "list_dir" {
				id = it.ToolID
			}
		}
	}
	d, ok, err := FindToolDetail(path, "", id)
	if err != nil || !ok {
		t.Fatalf("detail not found: ok=%v err=%v", ok, err)
	}
	if d.Result != "- a.go\n- b.go" {
		t.Fatalf("detail result = %q", d.Result)
	}
}

func TestReadSubagentViewRecursive(t *testing.T) {
	root := t.TempDir()
	homeDirOverride = root
	t.Cleanup(func() { homeDirOverride = "" })
	child := "acec302c-335c-46b5-b75a-4d7695c26a3c"
	dir := filepath.Join(root, "brain", child, ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript_full.jsonl"),
		[]byte(`{"type":"USER_INPUT","content":"<USER_REQUEST>\nStart\n</USER_REQUEST>","step_index":0}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	p, ok := SubagentFilePath("", child)
	if !ok {
		t.Fatal("SubagentFilePath should resolve existing child")
	}
	if filepath.Base(p) != "transcript_full.jsonl" {
		t.Fatalf("unexpected child path %q", p)
	}
	v, ok, err := ReadSubagentView("", child)
	if err != nil || !ok {
		t.Fatalf("subagent view: ok=%v err=%v", ok, err)
	}
	if len(v.Chunks) != 1 || v.Chunks[0].Kind != transcript.ChunkUser {
		t.Fatalf("child not parsed: %+v", v.Chunks)
	}
}
