package agentgit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setupEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Tester", "-c", "user.email=tester@example.com"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo returns an outer repo at <tmp>/outer with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	setupEnv(t)
	outer := filepath.Join(t.TempDir(), "outer")
	if err := os.MkdirAll(outer, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, outer, "init", "-q")
	write(t, filepath.Join(outer, "README"), "hi\n")
	run(t, outer, "add", "README")
	run(t, outer, "commit", "-q", "-m", "init")
	return outer
}

func count(t *testing.T, dir string) string {
	return run(t, dir, "rev-list", "--count", "HEAD")
}

func TestOpenNestedInsideOuterRepo(t *testing.T) {
	outer := newRepo(t)
	dir := filepath.Join(outer, "agents", "a")
	ctx := context.Background()
	r, err := Open(ctx, dir, "nested")
	if err != nil {
		t.Fatal(err)
	}
	if r.Outer {
		t.Fatal("Outer = true, want false")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("nested .git missing: %v", err)
	}
	before := count(t, outer)
	write(t, filepath.Join(dir, "memory.md"), "x\n")
	hash, err := r.CommitPaths(ctx, "add memory", nil, []string{"memory.md"}, true)
	if err != nil || hash == "" {
		t.Fatalf("CommitPaths = %q, %v", hash, err)
	}
	if after := count(t, outer); after != before {
		t.Fatalf("outer commits %s -> %s", before, after)
	}
	if _, err := Open(ctx, dir, "nested"); err != nil {
		t.Fatalf("reopen: %v", err)
	}
}

func TestOpenOuterUsesEnclosingRepo(t *testing.T) {
	outer := newRepo(t)
	dir := filepath.Join(outer, "agents", "a")
	ctx := context.Background()
	r, err := Open(ctx, dir, "outer")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Outer {
		t.Fatal("Outer = false, want true")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Fatal("unexpected nested .git")
	}
	write(t, filepath.Join(outer, "other.txt"), "other\n")
	run(t, outer, "add", "other.txt")
	write(t, filepath.Join(dir, "memory.md"), "x\n")
	before := count(t, outer)
	hash, err := r.CommitPaths(ctx, "add memory", nil, []string{"memory.md"}, true)
	if err != nil || hash == "" {
		t.Fatalf("CommitPaths = %q, %v", hash, err)
	}
	if after := count(t, outer); after == before {
		t.Fatal("outer HEAD did not advance")
	}
	files := run(t, outer, "show", "--name-only", "--format=", "HEAD")
	if files != "agents/a/memory.md" {
		t.Fatalf("commit touched %q", files)
	}
	if staged := run(t, outer, "diff", "--cached", "--name-only"); staged != "other.txt" {
		t.Fatalf("staged = %q, want other.txt", staged)
	}
}

func TestOpenOuterInitsWhenNoRepo(t *testing.T) {
	setupEnv(t)
	dir := filepath.Join(t.TempDir(), "a")
	r, err := Open(context.Background(), dir, "outer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf(".git missing: %v", err)
	}
	if r.Outer {
		t.Fatal("Outer = true for freshly initialized repo")
	}
}

func TestCommitPathsTrailersAndNoop(t *testing.T) {
	setupEnv(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "a")
	r, err := Open(ctx, dir, "nested")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "memory.md"), "x\n")
	trailers := []Trailer{
		{"Argus-Agent", "scout"},
		{"Argus-Agent-Id", "ag1"},
		{"Argus-Source", "capture"},
		{"Argus-Thread", "th1"},
	}
	hash, err := r.CommitPaths(ctx, "capture notes", trailers, []string{"memory.md"}, true)
	if err != nil || hash == "" {
		t.Fatalf("CommitPaths = %q, %v", hash, err)
	}
	log, err := r.Log(ctx, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 1 || log[0].Hash != hash || log[0].Subject != "capture notes" || log[0].When.IsZero() {
		t.Fatalf("log = %+v", log)
	}
	for _, tr := range trailers {
		if got := log[0].Trailers[tr.Key]; got != tr.Value {
			t.Errorf("trailer %s = %q, want %q", tr.Key, got, tr.Value)
		}
	}
	if name := run(t, dir, "log", "-1", "--format=%an <%ae>"); name != "Argus <argus@localhost>" {
		t.Errorf("author = %q", name)
	}
	hash, err = r.CommitPaths(ctx, "again", trailers, []string{"memory.md"}, true)
	if hash != "" || err != nil {
		t.Fatalf("noop CommitPaths = %q, %v", hash, err)
	}
}

func TestCommitPathsDeletedFile(t *testing.T) {
	setupEnv(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "a")
	r, _ := Open(ctx, dir, "nested")
	write(t, filepath.Join(dir, "memory.md"), "x\n")
	if _, err := r.CommitPaths(ctx, "add", nil, []string{"memory.md"}, true); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "memory.md"))
	hash, err := r.CommitPaths(ctx, "remove", nil, []string{"memory.md", "never-existed.md"}, true)
	if err != nil || hash == "" {
		t.Fatalf("CommitPaths = %q, %v", hash, err)
	}
}

func TestOuterBusyDuringMerge(t *testing.T) {
	outer := newRepo(t)
	ctx := context.Background()
	r, err := Open(ctx, filepath.Join(outer, "agents", "a"), "outer")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(r.Dir, "memory.md"), "x\n")
	write(t, filepath.Join(r.GitDir, "MERGE_HEAD"), "0000\n")
	before := count(t, outer)
	if _, err := r.CommitPaths(ctx, "x", nil, []string{"memory.md"}, true); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if after := count(t, outer); after != before {
		t.Fatalf("commit created while busy")
	}
}

func TestCommitUserEditsSeparateCommit(t *testing.T) {
	setupEnv(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "a")
	r, _ := Open(ctx, dir, "nested")
	write(t, filepath.Join(dir, "memory.md"), "x\n")
	if _, err := r.CommitPaths(ctx, "add", nil, []string{"memory.md"}, true); err != nil {
		t.Fatal(err)
	}
	if hash, err := r.CommitUserEdits(ctx, []string{"memory.md"}, "scout", "ag1"); hash != "" || err != nil {
		t.Fatalf("clean CommitUserEdits = %q, %v", hash, err)
	}
	write(t, filepath.Join(dir, "memory.md"), "edited\n")
	hash, err := r.CommitUserEdits(ctx, []string{"memory.md"}, "scout", "ag1")
	if err != nil || hash == "" {
		t.Fatalf("CommitUserEdits = %q, %v", hash, err)
	}
	log, _ := r.Log(ctx, nil, 1)
	if log[0].Subject != "manual edit" || log[0].Trailers["Argus-Source"] != "you" ||
		log[0].Trailers["Argus-Agent"] != "scout" || log[0].Trailers["Argus-Agent-Id"] != "ag1" {
		t.Fatalf("log = %+v", log[0])
	}
	// No identity configured: falls back to the Argus identity.
	if a := run(t, dir, "log", "-1", "--format=%an"); a != "Argus" {
		t.Errorf("fallback author = %q", a)
	}
}

func TestCommitUserEditsUsesConfiguredIdentity(t *testing.T) {
	setupEnv(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "a")
	r, _ := Open(ctx, dir, "nested")
	run(t, dir, "config", "user.name", "Me")
	run(t, dir, "config", "user.email", "me@example.com")
	write(t, filepath.Join(dir, "memory.md"), "x\n")
	if _, err := r.CommitUserEdits(ctx, []string{"memory.md"}, "scout", "ag1"); err != nil {
		t.Fatal(err)
	}
	if a := run(t, dir, "log", "-1", "--format=%an"); a != "Me" {
		t.Errorf("author = %q, want Me", a)
	}
}

func TestRevertConflictAborts(t *testing.T) {
	setupEnv(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "a")
	r, _ := Open(ctx, dir, "nested")
	p := filepath.Join(dir, "memory.md")
	write(t, p, "base\n")
	if _, err := r.CommitPaths(ctx, "base", nil, []string{"memory.md"}, true); err != nil {
		t.Fatal(err)
	}
	write(t, p, "one\n")
	first, err := r.CommitPaths(ctx, "one", nil, []string{"memory.md"}, true)
	if err != nil {
		t.Fatal(err)
	}
	write(t, p, "two\n")
	if _, err := r.CommitPaths(ctx, "two", nil, []string{"memory.md"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Revert(ctx, first); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if st := run(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("worktree not clean: %q", st)
	}
	if _, err := os.Stat(filepath.Join(r.GitDir, "REVERT_HEAD")); err == nil {
		t.Fatal("revert still in progress")
	}
}

func TestRevertSuccess(t *testing.T) {
	setupEnv(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "a")
	r, _ := Open(ctx, dir, "nested")
	write(t, filepath.Join(dir, "a.md"), "a\n")
	r.CommitPaths(ctx, "a", nil, []string{"a.md"}, true)
	write(t, filepath.Join(dir, "b.md"), "b\n")
	b, _ := r.CommitPaths(ctx, "b", nil, []string{"b.md"}, true)
	hash, err := r.Revert(ctx, b)
	if err != nil || hash == "" {
		t.Fatalf("Revert = %q, %v", hash, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.md")); err == nil {
		t.Fatal("b.md still present")
	}
}

func TestBlameSources(t *testing.T) {
	setupEnv(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "a")
	r, _ := Open(ctx, dir, "nested")
	p := filepath.Join(dir, "memory.md")
	write(t, p, "mine\n")
	if _, err := r.CommitPaths(ctx, "you", []Trailer{{"Argus-Source", "you"}}, []string{"memory.md"}, true); err != nil {
		t.Fatal(err)
	}
	write(t, p, "mine\ncaptured\n")
	if _, err := r.CommitPaths(ctx, "cap", []Trailer{{"Argus-Source", "capture"}}, []string{"memory.md"}, true); err != nil {
		t.Fatal(err)
	}
	got, err := r.BlameSources(ctx, "memory.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1] != "you" || got[2] != "capture" {
		t.Fatalf("blame = %v", got)
	}
}

func TestEnsureExcludedAppends(t *testing.T) {
	setupEnv(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "a")
	r, _ := Open(ctx, dir, "nested")
	write(t, filepath.Join(dir, ".gitignore"), "keep\nsecrets/")
	for range 2 {
		if err := r.EnsureExcluded(ctx, []string{"secrets/", "tmp/"}); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(b) != "keep\nsecrets/\ntmp/\n" {
		t.Fatalf(".gitignore = %q", b)
	}
}

func TestEnsureExcludedOuter(t *testing.T) {
	outer := newRepo(t)
	ctx := context.Background()
	r, _ := Open(ctx, filepath.Join(outer, "agents", "a"), "outer")
	if err := r.EnsureExcluded(ctx, []string{"agents/a/tmp/"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(r.GitDir, "info", "exclude"))
	if err != nil || !strings.Contains(string(b), "agents/a/tmp/\n") {
		t.Fatalf("exclude = %q, %v", b, err)
	}
}

func TestOuterHookFailureReturnsError(t *testing.T) {
	outer := newRepo(t)
	ctx := context.Background()
	r, _ := Open(ctx, filepath.Join(outer, "agents", "a"), "outer")
	hook := filepath.Join(r.GitDir, "hooks", "pre-commit")
	write(t, hook, "#!/bin/sh\necho nope >&2\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(r.Dir, "memory.md"), "x\n")
	before := count(t, outer)
	hash, err := r.CommitPaths(ctx, "x", nil, []string{"memory.md"}, true)
	if err == nil || hash != "" {
		t.Fatalf("CommitPaths = %q, %v; want hook error", hash, err)
	}
	if after := count(t, outer); after != before {
		t.Fatal("commit created despite failing hook")
	}
}

func TestRevertBusyLeavesUserRevertAlone(t *testing.T) {
	setupEnv(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "a")
	r, _ := Open(ctx, dir, "nested")
	write(t, filepath.Join(dir, "a.md"), "a\n")
	a, _ := r.CommitPaths(ctx, "a", nil, []string{"a.md"}, true)
	marker := filepath.Join(r.GitDir, "REVERT_HEAD")
	write(t, marker, a+"\n")
	if _, err := r.Revert(ctx, a); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("REVERT_HEAD removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.md")); err != nil {
		t.Fatal("a.md touched")
	}
}

func TestAddedLines(t *testing.T) {
	setupEnv(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "a")
	r, _ := Open(ctx, dir, "nested")
	write(t, filepath.Join(dir, "m.md"), "one\ntwo\n")
	first, err := r.CommitPaths(ctx, "add", nil, []string{"m.md"}, true)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "m.md"), "one\nthree\n++ plus\n")
	second, err := r.CommitPaths(ctx, "edit", nil, []string{"m.md"}, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.AddedLines(ctx, second)
	if err != nil || strings.Join(got, "|") != "three|++ plus" {
		t.Fatalf("AddedLines = %q, %v", got, err)
	}
	if got, err := r.AddedLines(ctx, first); err != nil || strings.Join(got, "|") != "one|two" {
		t.Fatalf("root AddedLines = %q, %v", got, err)
	}
}
