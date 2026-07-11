package gitmeta

import (
	"context"
	"os"
	"os/exec"
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

func TestIdentity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("config", "user.name", "Ada Lovelace")
	git("config", "user.email", "ada@example.com")

	name, email := Identity(ctx, dir)
	if name != "Ada Lovelace" {
		t.Errorf("name = %q, want %q", name, "Ada Lovelace")
	}
	if email != "ada@example.com" {
		t.Errorf("email = %q, want %q", email, "ada@example.com")
	}
}

func TestIdentityEmptyDir(t *testing.T) {
	name, email := Identity(context.Background(), "")
	if name != "" || email != "" {
		t.Fatalf("Identity(\"\") = (%q, %q), want empty", name, email)
	}
}

func TestIdentityNotARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// Isolate config so a developer's global/system identity can't leak in.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	name, email := Identity(context.Background(), dir)
	if name != "" || email != "" {
		t.Fatalf("Identity(non-repo) = (%q, %q), want empty", name, email)
	}
}
