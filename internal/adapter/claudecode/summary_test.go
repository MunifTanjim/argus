package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSummarizeChunks(t *testing.T) {
	chunks := []Chunk{
		{Kind: ChunkUser, Text: "Revamp the list\nmore detail", Timestamp: "2026-06-14T10:00:00Z"},
		{Kind: ChunkAI, Model: "claude-opus-4-8", HasContext: true, ContextPct: 42,
			Usage: Usage{Input: 100000, CacheRead: 28000}, Timestamp: "2026-06-14T10:00:05Z"},
	}
	s := summarizeChunks(chunks)
	if s == nil {
		t.Fatal("expected a summary")
	}
	if s.Model != "claude-opus-4-8" {
		t.Errorf("model = %q", s.Model)
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
