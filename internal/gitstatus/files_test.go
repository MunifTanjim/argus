package gitstatus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func gitInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func commit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "c"}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestListDirHidesGitAndIgnored(t *testing.T) {
	ctx := context.Background()
	dir := gitInit(t)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.txt\nnode_modules/\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "keep.go"), []byte("package x"), 0o644)
	os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(dir, "node_modules"), 0o755)
	os.Mkdir(filepath.Join(dir, "pkg"), 0o755)
	commit(t, dir)

	_, entries, err := ListDir(ctx, dir, "")
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if names[".git"] || names["ignored.txt"] || names["node_modules"] {
		t.Errorf("hidden entries leaked: %+v", entries)
	}
	if !names["keep.go"] || !names["pkg"] {
		t.Errorf("expected keep.go and pkg: %+v", entries)
	}
	// Directories sort first.
	if entries[0].Name != "pkg" || !entries[0].IsDir {
		t.Errorf("dirs should sort first, got %+v", entries[0])
	}
}

func TestReadFileBinaryNotShown(t *testing.T) {
	ctx := context.Background()
	dir := gitInit(t)
	os.WriteFile(filepath.Join(dir, "text.txt"), []byte("hello\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0x00, 0x01, 0x02}, 0o644)

	content, notShown, err := ReadFile(ctx, dir, "text.txt")
	if err != nil || notShown || content != "hello\n" {
		t.Fatalf("text read: content=%q notShown=%v err=%v", content, notShown, err)
	}
	_, notShown, err = ReadFile(ctx, dir, "bin.dat")
	if err != nil || !notShown {
		t.Fatalf("binary: notShown=%v err=%v", notShown, err)
	}
}

func TestReadFileRejectsEscape(t *testing.T) {
	if _, _, err := ReadFile(context.Background(), gitInit(t), "../escape"); err == nil {
		t.Error("expected an error for a path escaping the repo")
	}
}

func TestReadFileRefusesFIFO(t *testing.T) {
	dir := gitInit(t)
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := ReadFile(context.Background(), dir, "pipe")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected an error for a FIFO")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadFile blocked on a FIFO")
	}
}

func TestWorkingDiffModifiedAndUntracked(t *testing.T) {
	ctx := context.Background()
	dir := gitInit(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	commit(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644)

	diff, notShown, err := WorkingDiff(ctx, dir, "a.txt")
	if err != nil || notShown {
		t.Fatalf("modified diff: notShown=%v err=%v", notShown, err)
	}
	if !strings.Contains(diff, "+two") {
		t.Errorf("modified diff missing +two:\n%s", diff)
	}

	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("fresh\n"), 0o644)
	diff, _, err = WorkingDiff(ctx, dir, "new.txt")
	if err != nil {
		t.Fatalf("untracked diff: %v", err)
	}
	if !strings.Contains(diff, "+fresh") || !strings.Contains(diff, "/dev/null") {
		t.Errorf("untracked diff not synthesized:\n%s", diff)
	}
}

func TestChangedFilesSinceTarget(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "base.txt", "a")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")
	gitCmd(t, dir, "checkout", "-b", "feat")
	write(t, dir, "committed.txt", "c")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "c")
	write(t, dir, "base.txt", "changed")
	write(t, dir, "new.txt", "n")

	base, err := TargetBase(ctx, dir, "main")
	if err != nil || base == "" {
		t.Fatalf("TargetBase = %q, %v", base, err)
	}
	_, files, err := ChangedFilesSince(ctx, dir, base)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ChangeType{}
	for _, f := range files {
		got[f.Path] = f.Change
	}
	want := map[string]ChangeType{"committed.txt": ChangeAdded, "base.txt": ChangeModified, "new.txt": ChangeUntracked}
	for p, c := range want {
		if got[p] != c {
			t.Errorf("%s = %q, want %q (all: %v)", p, got[p], c, got)
		}
	}
	diff, _, err := DiffSince(ctx, dir, base, "committed.txt", "")
	if err != nil || !strings.Contains(diff, "+c") {
		t.Errorf("DiffSince committed = %q, %v", diff, err)
	}
	if _, err := TargetBase(ctx, dir, "nope"); err == nil {
		t.Error("an unknown target should fail")
	}
}

func TestDiffSinceShowsRename(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "a.txt", "one\ntwo\nthree\nfour\nfive\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")
	gitCmd(t, dir, "checkout", "-b", "feat")
	gitCmd(t, dir, "mv", "a.txt", "b.txt")
	write(t, dir, "b.txt", "one\ntwo\nthree\nfour\nFIVE\n")
	gitCmd(t, dir, "commit", "-am", "rename")

	base, err := TargetBase(ctx, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	diff, _, err := DiffSince(ctx, dir, base, "b.txt", "a.txt")
	if err != nil || !strings.Contains(diff, "rename from a.txt") || strings.Contains(diff, "+one") {
		t.Errorf("want a rename diff, got:\n%s (err %v)", diff, err)
	}
}
