package gittree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
}

func initRepo(t *testing.T) (root, worktree string) {
	t.Helper()
	root = t.TempDir()
	run(t, root, "git", "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", ".")
	run(t, root, "git", "commit", "-m", "init")
	worktree = filepath.Join(t.TempDir(), "wt")
	run(t, root, "git", "worktree", "add", "-b", "feature", worktree)
	return root, worktree
}

func TestResolveMainAndLinked(t *testing.T) {
	ctx := context.Background()
	root, worktree := initRepo(t)

	main, err := Resolve(ctx, root)
	if err != nil {
		t.Fatalf("Resolve main: %v", err)
	}
	if !main.IsMain {
		t.Errorf("main: IsMain = false, want true")
	}
	if main.Branch != "main" {
		t.Errorf("main: Branch = %q, want main", main.Branch)
	}
	if main.WorktreeRoot == "" || main.Head == "" || main.GitDir == "" {
		t.Errorf("main: empty field in %+v", main)
	}

	linked, err := Resolve(ctx, worktree)
	if err != nil {
		t.Fatalf("Resolve linked: %v", err)
	}
	if linked.IsMain {
		t.Errorf("linked: IsMain = true, want false")
	}
	if linked.Branch != "feature" {
		t.Errorf("linked: Branch = %q, want feature", linked.Branch)
	}
	if linked.GitDir != main.GitDir {
		t.Errorf("linked GitDir %q != main GitDir %q", linked.GitDir, main.GitDir)
	}
}

func TestResolveDetachedHasNoBranch(t *testing.T) {
	ctx := context.Background()
	root, _ := initRepo(t)
	run(t, root, "git", "checkout", "--detach", "HEAD")

	loc, err := Resolve(ctx, root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if loc.Branch != "" {
		t.Errorf("detached: Branch = %q, want empty", loc.Branch)
	}
	if loc.Head == "" {
		t.Errorf("detached: Head is empty")
	}
}

func TestResolveNotRepo(t *testing.T) {
	if _, err := Resolve(context.Background(), t.TempDir()); err != ErrNotRepo {
		t.Errorf("err = %v, want ErrNotRepo", err)
	}
}

func TestAddRemoveWorktreeAndDefaultBranch(t *testing.T) {
	ctx := context.Background()
	root, _ := initRepo(t)

	if b := DefaultBranch(ctx, root); b != "main" {
		t.Errorf("DefaultBranch = %q, want main", b)
	}

	wt := filepath.Join(t.TempDir(), "new")
	if err := AddWorktree(ctx, root, wt, "newbranch", DefaultBranch(ctx, root)); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	loc, err := Resolve(ctx, wt)
	if err != nil || loc.Branch != "newbranch" {
		t.Fatalf("resolve new worktree: branch=%q err=%v", loc.Branch, err)
	}
	if err := RemoveWorktree(ctx, root, wt, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree dir not removed: %v", err)
	}
}

func TestListWorktrees(t *testing.T) {
	ctx := context.Background()
	root, worktree := initRepo(t)

	mainLoc, err := Resolve(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	linkedLoc, err := Resolve(ctx, worktree)
	if err != nil {
		t.Fatal(err)
	}
	wts, err := ListWorktrees(ctx, mainLoc.GitDir)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("got %d worktrees, want 2: %+v", len(wts), wts)
	}
	byDir := map[string]Worktree{}
	for _, w := range wts {
		byDir[w.Dir] = w
	}
	// git reports resolved (real) paths, so key off Resolve's WorktreeRoot.
	if m := byDir[mainLoc.WorktreeRoot]; !m.IsMain || m.Branch != "main" {
		t.Errorf("main worktree = %+v", m)
	}
	if l := byDir[linkedLoc.WorktreeRoot]; l.IsMain || l.Branch != "feature" {
		t.Errorf("linked worktree = %+v", l)
	}
}

func TestAddWorktreeReusesExistingBranch(t *testing.T) {
	ctx := context.Background()
	root, _ := initRepo(t)

	wt := filepath.Join(t.TempDir(), "again")
	if err := AddWorktree(ctx, root, wt, "reuse", "main"); err != nil {
		t.Fatalf("first AddWorktree: %v", err)
	}
	if err := RemoveWorktree(ctx, root, wt, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if err := AddWorktree(ctx, root, wt, "reuse", "main"); err != nil {
		t.Fatalf("AddWorktree on the kept branch: %v", err)
	}
	if loc, err := Resolve(ctx, wt); err != nil || loc.Branch != "reuse" {
		t.Fatalf("resolve re-added worktree: branch=%q err=%v", loc.Branch, err)
	}
}

func TestBranchesMergesLocalAndRemote(t *testing.T) {
	ctx := context.Background()
	root, _ := initRepo(t)

	remote := t.TempDir()
	run(t, remote, "git", "init", "--bare", "-b", "main")
	run(t, root, "git", "remote", "add", "origin", remote)
	run(t, root, "git", "push", "origin", "main")
	run(t, root, "git", "branch", "only-local")
	run(t, root, "git", "push", "origin", "main:only-remote")
	run(t, root, "git", "fetch", "origin")

	bs, err := Branches(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Branch{}
	for _, b := range bs {
		got[b.Name] = b
	}
	if b := got["main"]; !b.Local || !b.Remote || !b.CheckedOut {
		t.Errorf("main = %+v, want local, remote, checked out", b)
	}
	if b := got["feature"]; !b.Local || b.Remote || !b.CheckedOut {
		t.Errorf("feature = %+v, want local and checked out in the linked worktree", b)
	}
	if b := got["only-local"]; !b.Local || b.Remote || b.CheckedOut {
		t.Errorf("only-local = %+v", b)
	}
	if b := got["only-remote"]; b.Local || !b.Remote {
		t.Errorf("only-remote = %+v", b)
	}
	if _, ok := got["HEAD"]; ok {
		t.Error("origin/HEAD must be skipped")
	}
}

func TestFetchAndRemoteHelpers(t *testing.T) {
	ctx := context.Background()
	root, _ := initRepo(t)
	if HasRemote(ctx, root, "origin") {
		t.Fatal("fresh repo has no origin")
	}
	run(t, root, "git", "remote", "add", "origin", filepath.Join(t.TempDir(), "missing"))
	if !HasRemote(ctx, root, "origin") {
		t.Fatal("origin not detected")
	}
	if err := Fetch(ctx, root, "origin", "main"); err == nil {
		t.Error("fetch from a missing remote should fail")
	}
	if !RefExists(ctx, root, "main") || RefExists(ctx, root, "origin/main") {
		t.Error("RefExists wrong for main / origin/main")
	}
}

func TestAddWorktreeTrackingAndDetached(t *testing.T) {
	ctx := context.Background()
	root, _ := initRepo(t)
	remote := t.TempDir()
	run(t, remote, "git", "init", "--bare", "-b", "main")
	run(t, root, "git", "remote", "add", "origin", remote)
	run(t, root, "git", "push", "origin", "main:rbranch")
	run(t, root, "git", "fetch", "origin")

	tr := filepath.Join(t.TempDir(), "tr")
	if err := AddWorktreeTracking(ctx, root, tr, "rbranch"); err != nil {
		t.Fatalf("AddWorktreeTracking: %v", err)
	}
	if loc, _ := Resolve(ctx, tr); loc.Branch != "rbranch" {
		t.Errorf("tracking worktree branch = %q", loc.Branch)
	}

	dt := filepath.Join(t.TempDir(), "dt")
	if err := AddWorktreeDetached(ctx, root, dt, "main"); err != nil {
		t.Fatalf("AddWorktreeDetached: %v", err)
	}
	if loc, _ := Resolve(ctx, dt); loc.Branch != "" {
		t.Errorf("detached worktree branch = %q, want empty", loc.Branch)
	}
}

func TestValidBranchName(t *testing.T) {
	ctx := context.Background()
	for name, want := range map[string]bool{
		"feat/login": true, "42-fix": true, "": false, "a..b": false, "-x": false, "has space": false,
	} {
		if got := ValidBranchName(ctx, name); got != want {
			t.Errorf("ValidBranchName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestFetchTreatsBranchAsRefNotOption(t *testing.T) {
	ctx := context.Background()
	root, _ := initRepo(t)
	remote := t.TempDir()
	run(t, remote, "git", "init", "--bare", "-b", "main")
	run(t, root, "git", "remote", "add", "origin", remote)
	run(t, root, "git", "push", "origin", "main")
	if err := Fetch(ctx, root, "origin", "--dry-run"); err == nil {
		t.Fatal("a branch named like an option must not be read as an option")
	}
}

func TestFetchUpdatesTrackingRefInSingleBranchClone(t *testing.T) {
	ctx := context.Background()
	root, _ := initRepo(t)
	remote := t.TempDir()
	run(t, remote, "git", "init", "--bare", "-b", "main")
	run(t, root, "git", "remote", "add", "origin", remote)
	run(t, root, "git", "push", "origin", "main", "main:dev")
	clone := filepath.Join(t.TempDir(), "c")
	run(t, root, "git", "clone", "-q", "--single-branch", "--branch", "main", remote, clone)

	if err := Fetch(ctx, clone, "origin", "dev"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !RefExists(ctx, clone, "origin/dev") {
		t.Error("origin/dev not updated; a single-branch clone's refspec skipped it")
	}
}

func TestRepoName(t *testing.T) {
	root, wt := initRepo(t) // main repo + a linked worktree named "wt"
	sub := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()
	for name, c := range map[string]struct{ dir, want string }{
		"main root":       {root, filepath.Base(root)},
		"main subdir":     {sub, filepath.Base(root)},
		"linked worktree": {wt, filepath.Base(root)},
		"not a repo":      {plain, filepath.Base(plain)},
		"empty":           {"", ""},
	} {
		if got := RepoName(c.dir); got != c.want {
			t.Errorf("%s: RepoName(%q) = %q, want %q", name, c.dir, got, c.want)
		}
	}
	if got := NameFromGitDir("/x/proj.git"); got != "proj" {
		t.Errorf("bare repo name = %q, want proj", got)
	}
	if got := NameFromGitDir("/x/proj/.git"); got != "proj" {
		t.Errorf("repo name from .git = %q, want proj", got)
	}
}

func TestRepoNameFromFiles(t *testing.T) {
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

	if got := RepoName(repo); got != "myrepo" {
		t.Errorf("RepoName(repo) = %q, want myrepo", got)
	}
	if got := RepoName(nested); got != "myrepo" { // walks up to the repo root
		t.Errorf("RepoName(nested) = %q, want myrepo", got)
	}
	if got := RepoName(plain); got != "plaindir" { // not a repo: basename of dir
		t.Errorf("RepoName(non-repo) = %q, want plaindir", got)
	}
	if got := RepoName(""); got != "" {
		t.Errorf("RepoName(\"\") = %q, want empty", got)
	}

	// A linked worktree's .git is a file pointing into the main repo's git dir.
	wtGit := filepath.Join(repo, ".git", "worktrees", "wt")
	wt := filepath.Join(root, "wt")
	for _, d := range []string{wtGit, wt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(wtGit, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wtGit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RepoName(wt); got != "myrepo" {
		t.Errorf("RepoName(worktree) = %q, want the main repo myrepo", got)
	}
}
