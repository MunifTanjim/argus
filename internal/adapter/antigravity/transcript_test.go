package antigravity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

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
