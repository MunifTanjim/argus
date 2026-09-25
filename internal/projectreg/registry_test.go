package projectreg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/db"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newRegistry(t *testing.T) *Registry {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "argus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return New(sqlDB)
}

func repoWithWorktree(t *testing.T) (root, worktree string) {
	t.Helper()
	root = t.TempDir()
	git(t, root, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "init")
	worktree = filepath.Join(t.TempDir(), "wt")
	git(t, root, "worktree", "add", "-b", "feature", worktree)
	return root, worktree
}

func TestAdoptGitProjectAndSnapshot(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	root, _ := repoWithWorktree(t)

	wsID, err := r.AdoptSession(ctx, root)
	if err != nil || wsID == "" {
		t.Fatalf("AdoptSession: id=%q err=%v", wsID, err)
	}
	again, err := r.AdoptSession(ctx, root)
	if err != nil || again != wsID {
		t.Fatalf("AdoptSession not idempotent: %q vs %q (err %v)", again, wsID, err)
	}

	projects, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	p := projects[0]
	if p.Kind != "git" {
		t.Errorf("kind = %q, want git", p.Kind)
	}
	if len(p.Workspaces) != 2 {
		t.Fatalf("got %d workspaces, want 2 (main enumerated its sibling)", len(p.Workspaces))
	}
	var mainWS *Workspace
	for i := range p.Workspaces {
		if p.Workspaces[i].IsMain {
			mainWS = &p.Workspaces[i]
		}
	}
	if mainWS == nil || mainWS.Branch != "main" {
		t.Fatalf("main workspace live branch wrong: %+v", mainWS)
	}
	if p.Root != mainWS.Dir {
		t.Errorf("project Root = %q, want %q", p.Root, mainWS.Dir)
	}
}

func TestAdoptPlainProject(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	dir := t.TempDir()

	wsID, err := r.AdoptSession(ctx, dir)
	if err != nil || wsID == "" {
		t.Fatalf("AdoptSession plain: id=%q err=%v", wsID, err)
	}
	projects, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Kind != "plain" {
		t.Fatalf("want 1 plain project, got %+v", projects)
	}
	if len(projects[0].Workspaces) != 1 || !projects[0].Workspaces[0].IsMain {
		t.Fatalf("plain project needs one main workspace: %+v", projects[0].Workspaces)
	}
	if projects[0].ID == wsID {
		t.Fatalf("plain project id %s must differ from its workspace id", wsID)
	}
}

func TestReconcileMarksRemovedWorktreeGone(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	root, worktree := repoWithWorktree(t)

	if _, err := r.AdoptSession(ctx, root); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	git(t, root, "worktree", "remove", worktree)

	projects, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var gone, live int
	for _, w := range projects[0].Workspaces {
		if w.IsGone {
			gone++
		} else {
			live++
		}
	}
	if gone != 1 || live != 1 {
		t.Fatalf("want 1 gone + 1 live workspace, got gone=%d live=%d", gone, live)
	}
}

func TestStatePersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "argus.db")
	dir := t.TempDir()

	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(sqlDB).AdoptSession(ctx, dir); err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()

	sqlDB2, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB2.Close()
	projects, err := New(sqlDB2).Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("state lost across reopen: got %d projects", len(projects))
	}
}

func TestProjectNamedAfterRepoWhenFirstSeenInWorktree(t *testing.T) {
	ctx := context.Background()
	root, wt := repoWithWorktree(t)
	r := newRegistry(t)
	if _, err := r.AdoptSession(ctx, wt); err != nil { // the linked worktree comes first
		t.Fatal(err)
	}
	ps, _ := r.Snapshot(ctx)
	if got, want := ps[0].Name, filepath.Base(root); got != want {
		t.Errorf("project name = %q, want the repo name %q", got, want)
	}
}
