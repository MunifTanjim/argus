package gitmeta

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes content to path, creating parent dirs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBranchNormal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
	if got := Branch(dir); got != "main" {
		t.Fatalf("Branch = %q, want %q", got, "main")
	}
}

func TestBranchWithSlashes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/feat/session-git-branch\n")
	if got := Branch(dir); got != "feat/session-git-branch" {
		t.Fatalf("Branch = %q, want %q", got, "feat/session-git-branch")
	}
}

func TestBranchDetachedHead(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "1234567890abcdef1234567890abcdef12345678\n")
	if got := Branch(dir); got != "1234567" {
		t.Fatalf("Branch = %q, want %q (short sha)", got, "1234567")
	}
}

func TestBranchNestedSubdir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
	sub := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Branch(sub); got != "main" {
		t.Fatalf("Branch = %q, want %q", got, "main")
	}
}

func TestBranchWorktreeGitFile(t *testing.T) {
	dir := t.TempDir()
	// Real git dir for the worktree, elsewhere on disk.
	realGitDir := filepath.Join(t.TempDir(), "worktrees", "wt1")
	writeFile(t, filepath.Join(realGitDir, "HEAD"), "ref: refs/heads/feature\n")
	// The worktree's .git is a file pointing at the real git dir.
	writeFile(t, filepath.Join(dir, ".git"), "gitdir: "+realGitDir+"\n")
	if got := Branch(dir); got != "feature" {
		t.Fatalf("Branch = %q, want %q", got, "feature")
	}
}

func TestBranchNotARepo(t *testing.T) {
	dir := t.TempDir()
	if got := Branch(dir); got != "" {
		t.Fatalf("Branch = %q, want empty", got)
	}
}

func TestBranchEmptyDir(t *testing.T) {
	if got := Branch(""); got != "" {
		t.Fatalf("Branch = %q, want empty", got)
	}
}

func TestBranchUnreadableHead(t *testing.T) {
	dir := t.TempDir()
	// .git dir exists but no HEAD file.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Branch(dir); got != "" {
		t.Fatalf("Branch = %q, want empty", got)
	}
}

func TestBranchSymbolicRefNotHeads(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/tags/v1.0\n")
	if got := Branch(dir); got != "" {
		t.Fatalf("Branch = %q, want empty (non-heads symbolic ref)", got)
	}
}

func TestBranchWorktreeRelativeGitdir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git-real", "HEAD"), "ref: refs/heads/feature\n")
	writeFile(t, filepath.Join(dir, ".git"), "gitdir: ./.git-real\n")
	if got := Branch(dir); got != "feature" {
		t.Fatalf("Branch = %q, want %q", got, "feature")
	}
}

func TestBranchHeadWithCRLF(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\r\n")
	if got := Branch(dir); got != "main" {
		t.Fatalf("Branch = %q, want %q", got, "main")
	}
}
