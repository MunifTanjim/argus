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

func TestWorkingDiffUnchangedTrackedFileIsEmpty(t *testing.T) {
	ctx := context.Background()
	dir := gitInit(t)
	write(t, dir, "a.txt", "one\n")
	commit(t, dir)

	diff, notShown, err := WorkingDiff(ctx, dir, "a.txt")
	if err != nil || notShown || diff != "" {
		t.Errorf("a reverted file should have no diff, got %q (notShown=%v err=%v)", diff, notShown, err)
	}
}

func TestWorkingDiffInNewRepoIsAllAdded(t *testing.T) {
	ctx := context.Background()
	dir := gitInit(t)
	write(t, dir, "staged.txt", "s\n")
	gitCmd(t, dir, "add", "staged.txt")
	write(t, dir, "loose.txt", "l\n")

	for p, want := range map[string]string{"staged.txt": "+s", "loose.txt": "+l"} {
		diff, _, err := WorkingDiff(ctx, dir, p)
		if err != nil || !strings.Contains(diff, want) {
			t.Errorf("%s in a repo with no commits: diff %q, err %v", p, diff, err)
		}
	}
}

func TestDiffSinceReportsGitFailure(t *testing.T) {
	ctx := context.Background()
	dir := gitInit(t)
	write(t, dir, "a.txt", "one\n")
	commit(t, dir)
	write(t, dir, "a.txt", "two\n")
	write(t, dir, ".git/index", "corrupt")

	if _, _, err := DiffSince(ctx, dir, "HEAD", "a.txt", ""); err == nil {
		t.Error("a failing git diff should return an error, not an all-added diff")
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

func symlinkRepo(t *testing.T) (dir, outside string) {
	t.Helper()
	dir = gitInit(t)
	write(t, dir, "real/a.txt", "inside\n")
	outside = t.TempDir()
	write(t, outside, "secret.txt", "outside\n")
	for link, target := range map[string]string{
		"inlink": "real", "flink": "real/a.txt",
		"outlink": outside, "outfile": filepath.Join(outside, "secret.txt"),
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	return dir, outside
}

func TestListDirMarksSymlinks(t *testing.T) {
	ctx := context.Background()
	dir, outside := symlinkRepo(t)
	_, entries, err := ListDir(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DirEntry{}
	for _, e := range entries {
		got[e.Name] = e
	}
	if e := got["inlink"]; !e.Symlink || !e.IsDir || e.Target != "real" {
		t.Errorf("a link to a folder in the repo should be a folder symlink: %+v", e)
	}
	if e := got["outlink"]; !e.Symlink || e.IsDir || e.Target != outside {
		t.Errorf("a link out of the repo should not open as a folder: %+v", e)
	}
	if e := got["flink"]; !e.Symlink || e.IsDir {
		t.Errorf("a link to a file should be a file symlink: %+v", e)
	}
	if _, inner, err := ListDir(ctx, dir, "inlink"); err != nil || len(inner) != 1 || inner[0].Name != "a.txt" {
		t.Errorf("an in-repo folder link should list its target: %+v %v", inner, err)
	}
	if _, _, err := ListDir(ctx, dir, "outlink"); err == nil {
		t.Error("listing through a link out of the repo should fail")
	}
}

func TestReadFileFollowsOnlyInRepoSymlinks(t *testing.T) {
	ctx := context.Background()
	dir, outside := symlinkRepo(t)
	if c, _, err := ReadFile(ctx, dir, "flink"); err != nil || c != "inside\n" {
		t.Errorf("a link to a repo file should show the file: %q %v", c, err)
	}
	if c, _, err := ReadFile(ctx, dir, "outfile"); err != nil || c != filepath.Join(outside, "secret.txt") {
		t.Errorf("a link out of the repo should show its target, not the file: %q %v", c, err)
	}
	if _, _, err := ReadFile(ctx, dir, "outlink/secret.txt"); err == nil {
		t.Error("reading through a link out of the repo should fail")
	}
}

func TestUntrackedSymlinkDiffShowsLinkText(t *testing.T) {
	ctx := context.Background()
	dir, _ := symlinkRepo(t)
	diff, _, err := WorkingDiff(ctx, dir, "flink")
	if err != nil || !strings.Contains(diff, "+real/a.txt") || strings.Contains(diff, "+inside") {
		t.Errorf("an untracked symlink diffs as its link text, as git stores it: %q %v", diff, err)
	}
}
