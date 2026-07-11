package antigravity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBrainTranscriptAt(t *testing.T, home, convID, body string) string {
	t.Helper()
	dir := filepath.Join(home, "brain", convID, ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "transcript_full.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCollectSessionFilesIncludesSubagents(t *testing.T) {
	home := t.TempDir()
	homeDirOverride = home
	t.Cleanup(func() { homeDirOverride = "" })

	const childID = "acec302c-335c-46b5-b75a-4d7695c26a3c"
	parentBody := `{"type":"PLANNER_RESPONSE","source":"MODEL","tool_calls":[{"name":"invoke_subagent","args":{"toolSummary":"Run subagent","Subagents":[{"name":"test_subagent","TypeName":"General"}]}}],"step_index":0}` + "\n" +
		`{"type":"INVOKE_SUBAGENT","source":"MODEL","content":` + jsonQuote(invokeResult) + `,"step_index":1}` + "\n"
	parent := writeBrainTranscriptAt(t, home, "parent-conv", parentBody)
	writeBrainTranscriptAt(t, home, childID, `{"type":"USER_INPUT","content":"<USER_REQUEST>\nGo\n</USER_REQUEST>","step_index":0}`+"\n")

	files, err := collectSessionFiles(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 transcripts (parent + child), got %d: %+v", len(files), files)
	}
	var haveChild bool
	for _, f := range files {
		if !strings.HasPrefix(f.RelPath, "root/brain/") {
			t.Fatalf("RelPath %q not rooted under root/brain/", f.RelPath)
		}
		if strings.Contains(f.RelPath, childID) {
			haveChild = true
		}
	}
	if !haveChild {
		t.Fatalf("child transcript not collected: %+v", files)
	}
}

// TestSubagentFilePathRootRelative verifies resolution follows the passed rootPath
// (an extracted bundle), not the live antigravity home.
func TestSubagentFilePathRootRelative(t *testing.T) {
	liveHome := t.TempDir() // live home has no matching child
	homeDirOverride = liveHome
	t.Cleanup(func() { homeDirOverride = "" })

	const childID = "acec302c-335c-46b5-b75a-4d7695c26a3c"
	bundleHome := t.TempDir()
	child := writeBrainTranscriptAt(t, bundleHome, childID, `{"type":"USER_INPUT","content":"hi","step_index":0}`+"\n")
	entry := writeBrainTranscriptAt(t, bundleHome, "main-conv", `{"type":"USER_INPUT","content":"hi","step_index":0}`+"\n")

	p, ok := SubagentFilePath(entry, childID)
	if !ok || p != child {
		t.Fatalf("SubagentFilePath = (%q, %v), want (%q, true) from bundle root", p, ok, child)
	}
	if _, ok := SubagentFilePath("", childID); ok {
		t.Fatal("empty root must not resolve against the (childless) live home")
	}
}
