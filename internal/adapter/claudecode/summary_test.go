package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummarizeChunks(t *testing.T) {
	chunks := []Chunk{
		{Kind: ChunkUser, Text: "Revamp the list\nmore detail", Timestamp: "2026-06-14T10:00:00Z"},
		{Kind: ChunkAI, ModelName: "Opus 4.8", ModelColor: "#d3869b", HasContext: true, ContextPct: 42,
			Usage: Usage{Input: 100000, CacheRead: 28000}, Timestamp: "2026-06-14T10:00:05Z"},
	}
	s := summarizeChunks(chunks)
	if s == nil {
		t.Fatal("expected a summary")
	}
	if s.ModelName != "Opus 4.8" {
		t.Errorf("model = %q", s.ModelName)
	}
	if !s.HasContext || s.ContextPct != 42 {
		t.Errorf("context = %v/%v", s.HasContext, s.ContextPct)
	}
	if s.Tokens != 128000 { // Usage.Context() = input + cacheRead
		t.Errorf("tokens = %d, want 128000", s.Tokens)
	}
	if s.Task != "Revamp the list" { // first line of the latest user prompt
		t.Errorf("task = %q", s.Task)
	}
	if s.LastActivity != "2026-06-14T10:00:05Z" { // last chunk's timestamp
		t.Errorf("lastActivity = %q", s.LastActivity)
	}
}

func TestSummarizeChunksEmpty(t *testing.T) {
	if s := summarizeChunks(nil); s != nil {
		t.Errorf("nil chunks should yield nil summary, got %+v", s)
	}
	if s := summarizeChunks([]Chunk{{Kind: ChunkAI}}); s != nil {
		t.Errorf("a contentless chunk should yield nil summary, got %+v", s)
	}
}

func TestSummarize_CapturesTaskSessionID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current.jsonl")
	lines := `{"uuid":"u1","type":"user","session_id":"root-x","message":{"role":"user","content":"do it"}}` + "\n" +
		`{"uuid":"u2","type":"assistant","session_id":"root-x","message":{"role":"assistant","model":"claude-opus-4","content":[{"type":"tool_use","id":"t1","name":"TaskCreate","input":{"subject":"x"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	s := summarize(path)
	if s == nil {
		t.Fatal("expected a summary")
	}
	if s.TaskSessionKeys != "root-x" {
		t.Errorf("TaskSessionKeys = %q, want %q (session_id of the TaskCreate line)", s.TaskSessionKeys, "root-x")
	}
}

// TestSummarize_TaskOnlyTranscriptStillCapturesKeys covers the s == nil branch:
// a line with no timestamp, model, or user text yields no display summary, but its
// session_id must still surface as TaskSessionKeys.
func TestSummarize_TaskOnlyTranscriptStillCapturesKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current.jsonl")
	line := `{"uuid":"u1","type":"assistant","session_id":"root-x","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"TaskCreate","input":{"subject":"x"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	s := summarize(path)
	if s == nil {
		t.Fatal("expected a summary carrying only TaskSessionKeys")
	}
	if s.TaskSessionKeys != "root-x" {
		t.Errorf("TaskSessionKeys = %q, want %q", s.TaskSessionKeys, "root-x")
	}
	if s.ModelName != "" || s.Task != "" || s.LastActivity != "" {
		t.Errorf("expected empty display fields, got %+v", s)
	}
}

// TestResumeFlow_TaskSessionIDKeysReadTasks wires the two halves of the resume
// fix: summarize captures the root session_id from the transcript, and ReadTasks
// uses it to read the dir keyed by that id rather than the (new) filename.
func TestResumeFlow_TaskSessionIDKeysReadTasks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := filepath.Join(home, ".claude")

	rootID := "root-1111-2222-3333"
	tp := filepath.Join(claude, "projects", "-proj", "new-filename-uuid.jsonl")
	if err := os.MkdirAll(filepath.Dir(tp), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"uuid":"u1","type":"assistant","session_id":"` + rootID + `","message":{"role":"assistant","model":"claude-opus-4","content":[{"type":"tool_use","id":"t1","name":"TaskCreate","input":{"subject":"x"}}]}}` + "\n"
	if err := os.WriteFile(tp, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(claude, "tasks", rootID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTask(t, dir, "7", "in_progress")

	s := summarize(tp)
	if s == nil || s.TaskSessionKeys != rootID {
		t.Fatalf("summarize TaskSessionKeys = %+v, want %q", s, rootID)
	}
	tasks, err := ReadTasks(strings.Fields(s.TaskSessionKeys), tp)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "7" {
		t.Fatalf("resume flow should read tasks from the root-keyed dir: got %+v", tasks)
	}
}

func TestRefreshesSummary(t *testing.T) {
	refresh := []string{"SessionStart", "UserPromptSubmit", "Stop", "Notification", "PermissionRequest"}
	for _, e := range refresh {
		if !refreshesSummary(e) {
			t.Errorf("%s should refresh the summary", e)
		}
	}
	for _, e := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "PreCompact", "SessionEnd", ""} {
		if refreshesSummary(e) {
			t.Errorf("%s should NOT refresh the summary", e)
		}
	}
}

func TestRepoName(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "myrepo")
	nested := filepath.Join(repo, "internal", "pkg")
	plain := filepath.Join(root, "plaindir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := repoName(repo); got != "myrepo" {
		t.Errorf("repoName(repo) = %q, want myrepo", got)
	}
	if got := repoName(nested); got != "myrepo" { // walks up to the repo root
		t.Errorf("repoName(nested) = %q, want myrepo", got)
	}
	if got := repoName(plain); got != "plaindir" { // not a repo: basename of dir
		t.Errorf("repoName(non-repo) = %q, want plaindir", got)
	}
	if got := repoName(""); got != "" {
		t.Errorf("repoName(\"\") = %q, want empty", got)
	}
}
