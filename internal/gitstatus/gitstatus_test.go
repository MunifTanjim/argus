package gitstatus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- pure parser unit test (deterministic, no git) ---

func TestParsePorcelainZ(t *testing.T) {
	// A rename record ("R  <dest>") is followed by a NUL token with the source.
	// Then an unstaged modify, a staged add, an AM (staged add + later modify),
	// an untracked file, and a path containing a space.
	out := "R  dst name\x00src name\x00 M mod.txt\x00A  added.go\x00AM both.go\x00?? unt.txt\x00?? with space.txt\x00"
	files := parsePorcelainZ(out)

	byPath := map[string]ChangedFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	if len(files) != 6 {
		t.Fatalf("got %d files, want 6: %+v", len(files), files)
	}

	if f := byPath["dst name"]; f.Change != ChangeRenamed || f.OrigPath != "src name" || !f.Staged {
		t.Errorf("rename: got %+v", f)
	}
	if f := byPath["mod.txt"]; f.Change != ChangeModified || f.Staged || !f.Unstaged {
		t.Errorf("unstaged modify: got %+v", f)
	}
	if f := byPath["added.go"]; f.Change != ChangeAdded || !f.Staged || f.Unstaged {
		t.Errorf("staged add: got %+v", f)
	}
	if f := byPath["both.go"]; f.Change != ChangeAdded || !f.Staged || !f.Unstaged {
		t.Errorf("AM (staged + unstaged): got %+v", f)
	}
	if f := byPath["unt.txt"]; f.Change != ChangeUntracked || f.Staged {
		t.Errorf("untracked: got %+v", f)
	}
	if f := byPath["with space.txt"]; f.Change != ChangeUntracked {
		t.Errorf("space path: got %+v", f)
	}
}

// --- integration tests over real temp repos ---

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func headSHA(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestChangedFilesIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")

	// Committed baseline.
	write(t, dir, "mod.txt", "one\n")
	write(t, dir, "del.txt", "gone\n")
	write(t, dir, "ren_src.txt", "moved\n")
	write(t, dir, "staged_mod.txt", "base\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")

	// Working-tree mutations.
	write(t, dir, "mod.txt", "one\ntwo\n")             // " M" unstaged modify
	os.Remove(filepath.Join(dir, "del.txt"))           // " D" unstaged delete
	gitCmd(t, dir, "mv", "ren_src.txt", "ren_dst.txt") // "R " staged rename
	write(t, dir, "staged_mod.txt", "changed\n")
	gitCmd(t, dir, "add", "staged_mod.txt") // "M " staged modify
	write(t, dir, "added.txt", "new\n")
	gitCmd(t, dir, "add", "added.txt")  // "A " staged add
	write(t, dir, "unt.txt", "loose\n") // "??" untracked

	root, files, err := ChangedFiles(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if root == "" {
		t.Error("empty root")
	}
	byPath := map[string]ChangedFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}

	checks := map[string]ChangeType{
		"mod.txt":        ChangeModified,
		"del.txt":        ChangeDeleted,
		"ren_dst.txt":    ChangeRenamed,
		"staged_mod.txt": ChangeModified,
		"added.txt":      ChangeAdded,
		"unt.txt":        ChangeUntracked,
	}
	for path, want := range checks {
		f, ok := byPath[path]
		if !ok {
			t.Errorf("%s missing from status", path)
			continue
		}
		if f.Change != want {
			t.Errorf("%s: change=%q want %q", path, f.Change, want)
		}
	}
	if f := byPath["ren_dst.txt"]; f.OrigPath != "ren_src.txt" {
		t.Errorf("rename orig path = %q, want ren_src.txt", f.OrigPath)
	}
	if !byPath["staged_mod.txt"].Staged {
		t.Error("staged_mod.txt should be staged")
	}
	if byPath["mod.txt"].Staged {
		t.Error("mod.txt should not be staged")
	}
}

func TestFileContents(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "mod.txt", "one\n")
	write(t, dir, "del.txt", "gone\n")
	write(t, dir, "bin.dat", "safe\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")

	write(t, dir, "mod.txt", "one\ntwo\n")
	os.Remove(filepath.Join(dir, "del.txt"))
	write(t, dir, "unt.txt", "loose\n")
	write(t, dir, "bin.dat", "x\x00y\n") // now contains a NUL

	ctx := context.Background()

	t.Run("modified", func(t *testing.T) {
		old, nw, ns, err := FileContents(ctx, dir, "mod.txt", "")
		if err != nil || ns {
			t.Fatalf("err=%v notShown=%v", err, ns)
		}
		if old != "one\n" || nw != "one\ntwo\n" {
			t.Errorf("old=%q new=%q", old, nw)
		}
	})
	t.Run("untracked", func(t *testing.T) {
		old, nw, _, err := FileContents(ctx, dir, "unt.txt", "")
		if err != nil || old != "" || nw != "loose\n" {
			t.Errorf("old=%q new=%q err=%v", old, nw, err)
		}
	})
	t.Run("deleted", func(t *testing.T) {
		old, nw, _, err := FileContents(ctx, dir, "del.txt", "")
		if err != nil || old != "gone\n" || nw != "" {
			t.Errorf("old=%q new=%q err=%v", old, nw, err)
		}
	})
	t.Run("binary not shown", func(t *testing.T) {
		old, nw, ns, err := FileContents(ctx, dir, "bin.dat", "")
		if err != nil || !ns || old != "" || nw != "" {
			t.Errorf("old=%q new=%q notShown=%v err=%v", old, nw, ns, err)
		}
	})
}

func TestFileContentsRenamed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "ren_src.txt", "moved\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")
	gitCmd(t, dir, "mv", "ren_src.txt", "ren_dst.txt")
	write(t, dir, "ren_dst.txt", "moved\nmore\n")

	old, nw, ns, err := FileContents(context.Background(), dir, "ren_dst.txt", "ren_src.txt")
	if err != nil || ns {
		t.Fatalf("err=%v notShown=%v", err, ns)
	}
	if old != "moved\n" || nw != "moved\nmore\n" {
		t.Errorf("old=%q new=%q", old, nw)
	}
}

func TestFileContentsOversized(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	big := strings.Repeat("x", maxContentBytes+1)
	write(t, dir, "head_big.txt", big)
	write(t, dir, "work_big.txt", "small\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")

	write(t, dir, "head_big.txt", "small\n") // HEAD oversized, working small
	write(t, dir, "work_big.txt", big)       // working oversized

	t.Run("head side oversized", func(t *testing.T) {
		old, nw, ns, err := FileContents(context.Background(), dir, "head_big.txt", "")
		if err != nil || !ns || old != "" || nw != "" {
			t.Errorf("old=%q new=%q notShown=%v err=%v", old, nw, ns, err)
		}
	})
	t.Run("working side oversized", func(t *testing.T) {
		_, _, ns, err := FileContents(context.Background(), dir, "work_big.txt", "")
		if err != nil || !ns {
			t.Errorf("notShown=%v err=%v", ns, err)
		}
	})
}

func TestFileContentsPathEscape(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "a.txt", "hi\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")

	if _, _, _, err := FileContents(context.Background(), dir, "../escape.txt", ""); err == nil {
		t.Error("expected error for path escaping repo root")
	}
}

func TestCommitsBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "a.txt", "1\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")
	gitCmd(t, dir, "checkout", "-b", "feat")
	write(t, dir, "a.txt", "1\n2\n")
	gitCmd(t, dir, "commit", "-am", "second")
	write(t, dir, "b.txt", "x\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "third")

	log, err := Commits(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(log.Commits) != 2 {
		t.Fatalf("got %d commits, want 2: %+v", len(log.Commits), log.Commits)
	}
	if log.Commits[0].Subject != "third" || log.Commits[1].Subject != "second" {
		t.Errorf("order/subjects: %+v", log.Commits)
	}
	if log.Commits[0].Short == "" || log.Commits[0].SHA == "" || log.Commits[0].UnixSec == 0 {
		t.Errorf("missing fields: %+v", log.Commits[0])
	}
	if log.Unpushed {
		t.Error("no remote configured; Unpushed should be false")
	}
}

func TestCommitsInSyncEmpty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "a.txt", "1\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")

	log, err := Commits(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(log.Commits) != 0 {
		t.Fatalf("on base branch with no divergence, want 0 commits: %+v", log.Commits)
	}
}

func TestCommitsSkipsMerges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "a.txt", "1\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")
	gitCmd(t, dir, "checkout", "-b", "feat")
	write(t, dir, "a.txt", "1\n2\n")
	gitCmd(t, dir, "commit", "-am", "feat work")
	gitCmd(t, dir, "checkout", "main")
	write(t, dir, "c.txt", "c\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "main work")
	gitCmd(t, dir, "checkout", "feat")
	gitCmd(t, dir, "merge", "--no-ff", "-m", "merge main", "main")

	log, err := Commits(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range log.Commits {
		if c.Subject == "merge main" {
			t.Errorf("merge commit should be skipped: %+v", log.Commits)
		}
	}
}

func TestNotARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, _, err := ChangedFiles(context.Background(), t.TempDir()); err == nil {
		t.Error("expected error for non-repo directory")
	}
}

func TestEmptyRepoNoHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "a.txt", "hello\n")

	_, files, err := ChangedFiles(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Change != ChangeUntracked {
		t.Fatalf("got %+v", files)
	}
	// No HEAD yet: old side must be empty, new side the working content.
	old, nw, _, err := FileContents(context.Background(), dir, "a.txt", "")
	if err != nil || old != "" || nw != "hello\n" {
		t.Errorf("old=%q new=%q err=%v", old, nw, err)
	}
}

func TestCommitFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "keep.txt", "k\n")
	write(t, dir, "gone.txt", "g\n")
	write(t, dir, "ren_src.txt", "same content here\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")

	write(t, dir, "keep.txt", "k\nk2\n")               // modified
	os.Remove(filepath.Join(dir, "gone.txt"))          // deleted
	gitCmd(t, dir, "mv", "ren_src.txt", "ren_dst.txt") // renamed
	write(t, dir, "added.txt", "n\n")                  // added
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "changes")

	sha := headSHA(t, dir)
	files, err := CommitFiles(context.Background(), dir, sha)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]ChangedFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	if byPath["keep.txt"].Change != ChangeModified {
		t.Errorf("keep.txt: %+v", byPath["keep.txt"])
	}
	if byPath["gone.txt"].Change != ChangeDeleted {
		t.Errorf("gone.txt: %+v", byPath["gone.txt"])
	}
	if byPath["added.txt"].Change != ChangeAdded {
		t.Errorf("added.txt: %+v", byPath["added.txt"])
	}
	if f := byPath["ren_dst.txt"]; f.Change != ChangeRenamed || f.OrigPath != "ren_src.txt" {
		t.Errorf("rename: %+v", f)
	}
}

func TestCommitFilesRootCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "a.txt", "1\n")
	write(t, dir, "b.txt", "2\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "root")

	files, err := CommitFiles(context.Background(), dir, headSHA(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("root commit should list all files, got %+v", files)
	}
	for _, f := range files {
		if f.Change != ChangeAdded {
			t.Errorf("root commit files should be added: %+v", f)
		}
	}
}

func TestIsHexSHA(t *testing.T) {
	ok := []string{
		"abcd", "0000", "DEADBEEF",
		"9714570255b0e9e81fe6dde43f192861bce35c9a", // sha-1
		strings.Repeat("a", 64),                    // sha-256 length
	}
	for _, s := range ok {
		if !isHexSHA(s) {
			t.Errorf("isHexSHA(%q) = false, want true", s)
		}
	}
	bad := []string{
		"", "abc", // too short
		strings.Repeat("a", 65), // too long
		"--output=/tmp/pwned",   // option injection
		"HEAD", "main",          // refs, not object names
		"abc g", "abcz", // non-hex
		"abc\n123", // embedded newline
	}
	for _, s := range bad {
		if isHexSHA(s) {
			t.Errorf("isHexSHA(%q) = true, want false", s)
		}
	}
}

// A crafted sha must never be parsed as a git option: "--output=FILE" would
// otherwise make diff-tree write to an arbitrary path.
func TestCommitFilesRejectsInjection(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "a.txt", "1\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")

	victim := filepath.Join(dir, "PWNED.txt")
	_, err := CommitFiles(context.Background(), dir, "--output="+victim)
	if err == nil {
		t.Error("expected error for option-like sha")
	}
	if _, serr := os.Stat(victim); serr == nil {
		t.Fatalf("argument injection wrote %s", victim)
	}
}

func TestCommitFileContentsRejectsInvalidSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "a.txt", "1\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")

	if _, _, _, err := CommitFileContents(context.Background(), dir, "--output=x", "a.txt", ""); err == nil {
		t.Error("expected error for option-like sha")
	}
}

// A changed symlink must yield its target text (as git stores it), never the
// content of the file it points to — even when that file is outside the repo.
func TestFileContentsSymlink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	if err := os.Symlink("target_a.txt", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	write(t, dir, "target_a.txt", "A\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")

	// Repoint the symlink at a secret file outside the repo.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	old, nw, ns, err := FileContents(context.Background(), dir, "link", "")
	if err != nil || ns {
		t.Fatalf("err=%v notShown=%v", err, ns)
	}
	if strings.Contains(nw, "SECRET") {
		t.Errorf("symlink content disclosure: new=%q leaked the target file's content", nw)
	}
	if nw != outside {
		t.Errorf("new = %q, want the link target path %q", nw, outside)
	}
	if old != "target_a.txt" {
		t.Errorf("old = %q, want the HEAD link target %q", old, "target_a.txt")
	}
}

func TestCommitFileContents(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	write(t, dir, "mod.txt", "one\n")
	write(t, dir, "del.txt", "bye\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")

	write(t, dir, "mod.txt", "one\ntwo\n")
	os.Remove(filepath.Join(dir, "del.txt"))
	write(t, dir, "add.txt", "fresh\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "changes")
	sha := headSHA(t, dir)

	t.Run("modified", func(t *testing.T) {
		old, nw, ns, err := CommitFileContents(context.Background(), dir, sha, "mod.txt", "")
		if err != nil || ns || old != "one\n" || nw != "one\ntwo\n" {
			t.Errorf("old=%q new=%q ns=%v err=%v", old, nw, ns, err)
		}
	})
	t.Run("added", func(t *testing.T) {
		old, nw, _, err := CommitFileContents(context.Background(), dir, sha, "add.txt", "")
		if err != nil || old != "" || nw != "fresh\n" {
			t.Errorf("old=%q new=%q err=%v", old, nw, err)
		}
	})
	t.Run("deleted", func(t *testing.T) {
		old, nw, _, err := CommitFileContents(context.Background(), dir, sha, "del.txt", "")
		if err != nil || old != "bye\n" || nw != "" {
			t.Errorf("old=%q new=%q err=%v", old, nw, err)
		}
	})
}
