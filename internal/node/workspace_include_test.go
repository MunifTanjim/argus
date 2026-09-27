package node

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/wsscript"
)

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func createAndCopy(t *testing.T, d *Node, projID string) api.WorkspaceCreateResult {
	t.Helper()
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if run := waitSetup(t, d, res.WorkspaceID); run.State != wsscript.OK {
		t.Fatalf("setup run = %+v output=%q", run, d.scripts.Output(res.WorkspaceID))
	}
	return res
}

func TestCreateCopiesWorktreeIncludeFiles(t *testing.T) {
	d, projID, main := createFixture(t)
	writeFile(t, filepath.Join(main, ".gitignore"), ".env\n*.local\nlink\n", 0o644)
	writeFile(t, filepath.Join(main, ".worktreeinclude"), ".env\n*.local\nlink\n", 0o644)
	runGit(t, main, "add", ".")
	runGit(t, main, "commit", "-m", "include")
	writeFile(t, filepath.Join(main, ".env"), "SECRET=1", 0o600)
	writeFile(t, filepath.Join(main, "a", "b", "conf.local"), "conf", 0o644)
	if err := os.Symlink(".env", filepath.Join(main, "link")); err != nil {
		t.Fatal(err)
	}

	res := createAndCopy(t, d, projID)
	if b, _ := os.ReadFile(filepath.Join(res.Dir, ".env")); string(b) != "SECRET=1" {
		t.Errorf(".env = %q", b)
	}
	if fi, err := os.Stat(filepath.Join(res.Dir, ".env")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %v, %v; want 0600", fi, err)
	}
	if b, _ := os.ReadFile(filepath.Join(res.Dir, "a", "b", "conf.local")); string(b) != "conf" {
		t.Errorf("conf.local = %q", b)
	}
	if target, err := os.Readlink(filepath.Join(res.Dir, "link")); err != nil || target != ".env" {
		t.Errorf("link = %q, %v; want a symlink to .env", target, err)
	}
}

func TestCreateKeepsExistingFileOverWorktreeInclude(t *testing.T) {
	d, projID, main := createFixture(t)
	writeFile(t, filepath.Join(main, ".gitignore"), "*.local\n", 0o644)
	writeFile(t, filepath.Join(main, ".worktreeinclude"), "*.local\n", 0o644)
	runGit(t, main, "add", ".")
	runGit(t, main, "commit", "-m", "include")
	runGit(t, main, "checkout", "-b", "kept")
	writeFile(t, filepath.Join(main, "conf.local"), "branch", 0o644)
	runGit(t, main, "add", "-f", "conf.local")
	runGit(t, main, "commit", "-m", "tracked conf")
	runGit(t, main, "checkout", "main")
	writeFile(t, filepath.Join(main, "conf.local"), "main", 0o644)

	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourceBranch, Branch: "kept"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if run := waitSetup(t, d, res.WorkspaceID); run.State != wsscript.OK {
		t.Fatalf("setup run = %+v output=%q", run, d.scripts.Output(res.WorkspaceID))
	}
	if b, _ := os.ReadFile(filepath.Join(res.Dir, "conf.local")); string(b) != "branch" {
		t.Errorf("conf.local = %q, want the branch's copy", b)
	}
}

func TestCreateCopiesWorktreeIncludeDirsWithModes(t *testing.T) {
	d, projID, main := createFixture(t)
	writeFile(t, filepath.Join(main, ".gitignore"), ".venv/\nsecrets/\n", 0o644)
	writeFile(t, filepath.Join(main, ".worktreeinclude"), ".venv/\nsecrets/\n", 0o644)
	runGit(t, main, "add", ".")
	runGit(t, main, "commit", "-m", "include")
	writeFile(t, filepath.Join(main, ".venv", "bin", "python"), "#!/bin/sh", 0o755)
	writeFile(t, filepath.Join(main, ".venv", "pyvenv.cfg"), "cfg", 0o664)
	writeFile(t, filepath.Join(main, "secrets", "key.pem"), "key", 0o600)
	if err := os.Chmod(filepath.Join(main, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}

	res := createAndCopy(t, d, projID)
	for rel, want := range map[string]os.FileMode{
		".venv/bin/python": 0o755,
		".venv/pyvenv.cfg": 0o664,
		"secrets":          0o700,
		"secrets/key.pem":  0o600,
	} {
		fi, err := os.Stat(filepath.Join(res.Dir, rel))
		if err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s mode = %v, %v; want %v", rel, fi, err, want)
		}
	}
}

func TestCreateFailsSetupOnWorktreeIncludeFailure(t *testing.T) {
	d, projID, main := createFixture(t)
	writeFile(t, filepath.Join(main, ".gitignore"), "*.local\n", 0o644)
	writeFile(t, filepath.Join(main, ".worktreeinclude"), "*.local\n", 0o644)
	runGit(t, main, "add", ".")
	runGit(t, main, "commit", "-m", "include")
	writeFile(t, filepath.Join(main, "locked.local"), "x", 0o000)
	writeFile(t, filepath.Join(main, "ok.local"), "ok", 0o644)

	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	out := func() string { return d.scripts.Output(res.WorkspaceID) }
	if run := waitSetup(t, d, res.WorkspaceID); run.State != wsscript.Failed || run.Command != "" {
		t.Errorf("setup run = %+v, want a failed run with no command", run)
	}
	if !strings.Contains(out(), "locked.local") || !strings.Contains(out(), "copied 1 of 2 files") {
		t.Errorf("output = %q, want it to name locked.local", out())
	}
	if _, err := os.Lstat(filepath.Join(res.Dir, "locked.local")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("locked.local left behind: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(res.Dir, "ok.local")); string(b) != "ok" {
		t.Errorf("ok.local = %q", b)
	}

	if err := os.Chmod(filepath.Join(main, "locked.local"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.handleWorkspaceRunSetup(context.Background(), json.RawMessage(`{"workspace_id":"`+res.WorkspaceID+`"}`)); err != nil {
		t.Fatalf("run setup: %v", err)
	}
	if run := waitSetup(t, d, res.WorkspaceID); run.State != wsscript.OK {
		t.Errorf("rerun = %+v output=%q", run, out())
	}
	if _, err := os.Lstat(filepath.Join(res.Dir, "locked.local")); err != nil {
		t.Errorf("rerun did not copy locked.local: %v", err)
	}
}

func TestCopyNewRefusesSymlinkedDirOutsideWorktree(t *testing.T) {
	src, dst, outside := t.TempDir(), t.TempDir(), t.TempDir()
	dst, _ = filepath.EvalSymlinks(dst)
	writeFile(t, filepath.Join(src, "cfg", "sub", "x.local"), "x", 0o644)
	if err := os.Symlink(outside, filepath.Join(dst, "cfg")); err != nil {
		t.Fatal(err)
	}
	if err := copyNew(src, dst, "cfg/sub/x.local"); err == nil {
		t.Error("copyNew wrote through a symlink outside the worktree")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("outside dir has %d entries, want none", len(entries))
	}
}

func TestCopyIncludedStopsOnCancel(t *testing.T) {
	d, projID, main := createFixture(t)
	writeFile(t, filepath.Join(main, ".gitignore"), "*.local\n", 0o644)
	writeFile(t, filepath.Join(main, ".worktreeinclude"), "*.local\n", 0o644)
	runGit(t, main, "add", ".")
	runGit(t, main, "commit", "-m", "include")
	writeFile(t, filepath.Join(main, "a.local"), "a", 0o644)
	res := createAndCopy(t, d, projID)
	writeFile(t, filepath.Join(main, "b.local"), "b", 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out strings.Builder
	if copyIncluded(ctx, main, res.Dir, &out) {
		t.Errorf("cancelled copy reported %q", out.String())
	}
	if _, err := os.Lstat(filepath.Join(res.Dir, "b.local")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("b.local copied after cancel: %v", err)
	}
}
