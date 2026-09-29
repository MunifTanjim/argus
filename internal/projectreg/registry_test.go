package projectreg

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

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
	t.Setenv("HOME", os.TempDir())
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

func TestPlainProjectBecomesGitAfterInit(t *testing.T) {
	ctx := context.Background()
	for _, readopt := range []bool{false, true} {
		r := newRegistry(t)
		dir := cleanDir(t.TempDir())
		wsID, err := r.AdoptSession(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		git(t, dir, "init", "-b", "main")
		if readopt {
			if again, err := r.AdoptSession(ctx, dir); err != nil || again != wsID {
				t.Fatalf("re-adopt after git init: id=%q err=%v, want %q", again, err, wsID)
			}
		}
		projects, err := r.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var live []Project
		for _, p := range projects {
			if !p.IsGone {
				live = append(live, p)
			}
		}
		if len(live) != 1 || live[0].Kind != "git" || len(live[0].Workspaces) != 1 || live[0].Workspaces[0].ID != wsID {
			t.Errorf("readopt=%v: want one git project owning %s, got %+v", readopt, wsID, live)
		}

		if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
			t.Fatal(err)
		}
		projects, err = r.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		live = live[:0]
		for _, p := range projects {
			if !p.IsGone {
				live = append(live, p)
			}
		}
		if len(live) != 1 || live[0].Kind != "plain" || len(live[0].Workspaces) != 1 || live[0].Workspaces[0].IsGone {
			t.Errorf("readopt=%v: after removing .git want the plain project back, got %+v", readopt, live)
		}
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

func setAutoAdoptDirs(t *testing.T, r *Registry, dirs ...string) {
	t.Helper()
	if err := r.SetAutoAdoptDirs(dirs); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptSkipsWorkspaceOutsideAutoAdoptDirs(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	setAutoAdoptDirs(t, r, t.TempDir())
	root, _ := repoWithWorktree(t)

	for _, dir := range []string{t.TempDir(), root} {
		wsID, err := r.AdoptSession(ctx, dir)
		if err != nil || wsID != "" {
			t.Fatalf("AdoptSession(%s): id=%q err=%v, want a no-op", dir, wsID, err)
		}
	}
	projects, err := r.Snapshot(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("Snapshot: %d projects err=%v, want none", len(projects), err)
	}
}

func TestAdoptSkipsProjectOutsideAutoAdoptDirs(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	_, worktree := repoWithWorktree(t)
	setAutoAdoptDirs(t, r, filepath.Dir(worktree))

	wsID, err := r.AdoptSession(ctx, worktree)
	if err != nil || wsID != "" {
		t.Fatalf("AdoptSession: id=%q err=%v, want a no-op", wsID, err)
	}
	projects, err := r.Snapshot(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("Snapshot: %d projects err=%v, want none", len(projects), err)
	}
}

func TestReconcileSkipsLiveWorktreeOutsideAutoAdoptDirs(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	root, worktree := repoWithWorktree(t)
	setAutoAdoptDirs(t, r, root)

	if _, err := r.AdoptSession(ctx, root); err != nil {
		t.Fatal(err)
	}
	projects, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(projects[0].Workspaces); n != 1 {
		t.Fatalf("want only the main workspace, got %d", n)
	}

	if _, err := r.AdoptWorkspace(ctx, worktree, "main"); err != nil {
		t.Fatal(err)
	}
	projects, err = r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range projects[0].Workspaces {
		if w.IsGone {
			t.Fatalf("adopted worktree outside auto-adopt dirs marked gone: %s", w.Dir)
		}
	}
	if n := len(projects[0].Workspaces); n != 2 {
		t.Fatalf("want the adopted worktree kept, got %d workspaces", n)
	}
}

func TestAdoptInsideAnyAutoAdoptDir(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	a, b := t.TempDir(), t.TempDir()
	setAutoAdoptDirs(t, r, a, b)

	for _, dir := range []string{a, filepath.Join(b, "notes")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if wsID, err := r.AdoptSession(ctx, dir); err != nil || wsID == "" {
			t.Fatalf("AdoptSession(%s): id=%q err=%v, want adopted", dir, wsID, err)
		}
	}
	if wsID, err := r.AdoptSession(ctx, t.TempDir()); err != nil || wsID != "" {
		t.Fatalf("AdoptSession outside: id=%q err=%v, want a no-op", wsID, err)
	}
}

func TestEmptyAutoAdoptDirsAdoptNothing(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	setAutoAdoptDirs(t, r)

	if wsID, err := r.AdoptSession(ctx, t.TempDir()); err != nil || wsID != "" {
		t.Fatalf("AdoptSession: id=%q err=%v, want a no-op", wsID, err)
	}
}

func TestSetAutoAdoptDirs(t *testing.T) {
	r := newRegistry(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	setAutoAdoptDirs(t, r, "~", "~/Dev")
	if want := []string{cleanDir(home), cleanDir(filepath.Join(home, "Dev"))}; !slices.Equal(r.adoptDirs, want) {
		t.Errorf("adoptDirs = %v, want %v", r.adoptDirs, want)
	}
	for _, bad := range []string{"Dev", "~user/Dev", ""} {
		if err := r.SetAutoAdoptDirs([]string{bad}); err == nil {
			t.Errorf("SetAutoAdoptDirs(%q): want an error", bad)
		}
	}
}

func TestGitFailureKeepsProjectAndReportsIt(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	root, _ := repoWithWorktree(t)
	if _, err := r.AdoptSession(ctx, root); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}

	path := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir()) // git is missing, the repo is not
	ps, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := ps[0]; p.IsGone || p.Error == "" || len(p.Workspaces) != 2 || p.Workspaces[0].IsGone {
		t.Errorf("a git failure should keep the project and report it: gone=%v error=%q workspaces=%+v", p.IsGone, p.Error, p.Workspaces)
	}

	t.Setenv("PATH", path)
	ps, _ = r.Snapshot(ctx)
	if p := ps[0]; p.IsGone || p.Error != "" {
		t.Errorf("the next list with git working should clear the error: gone=%v error=%q", p.IsGone, p.Error)
	}
}

func TestLastSeenMovesOnlyOnAdopt(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	root, _ := repoWithWorktree(t)
	if _, err := r.AdoptSession(ctx, root); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, table := range []string{"project", "workspace"} {
		if _, err := r.db.Exec("UPDATE "+table+" SET last_seen_at = ?", old); err != nil {
			t.Fatal(err)
		}
	}
	ps, _ := r.Snapshot(ctx)
	if !ps[0].LastSeenAt.Equal(old) || !ps[0].Workspaces[0].LastSeenAt.Equal(old) {
		t.Errorf("a list should not move last_seen_at: project=%v workspace=%v", ps[0].LastSeenAt, ps[0].Workspaces[0].LastSeenAt)
	}
	if _, err := r.AdoptSession(ctx, root); err != nil {
		t.Fatal(err)
	}
	ps, _ = r.Snapshot(ctx)
	if !ps[0].LastSeenAt.After(old) || !ps[0].Workspaces[0].LastSeenAt.After(old) {
		t.Errorf("an adopt should move last_seen_at: project=%v workspace=%v", ps[0].LastSeenAt, ps[0].Workspaces[0].LastSeenAt)
	}
}

func TestSnapshotSkipsProjectForgottenMidList(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	root, _ := repoWithWorktree(t)
	if _, err := r.AdoptSession(ctx, root); err != nil {
		t.Fatal(err)
	}
	ps, _ := r.Snapshot(ctx)
	rows, err := r.q.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	probes := probeAll(ctx, rows) // git runs here, without the lock
	if err := r.ForgetProject(ctx, ps[0].ID); err != nil {
		t.Fatal(err)
	}
	out, _, err := r.apply(ctx, rows, probes, r.records)
	if err != nil || len(out) != 0 {
		t.Errorf("a project forgotten between probe and apply should be skipped: out=%+v err=%v", out, err)
	}
}

func TestSnapshotKeepsWorkspaceAddedMidList(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	root, _ := repoWithWorktree(t)
	if _, err := r.AdoptSession(ctx, root); err != nil {
		t.Fatal(err)
	}
	rows, err := r.q.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	since := r.records
	probes := probeAll(ctx, rows)
	added := filepath.Join(t.TempDir(), "added")
	git(t, root, "worktree", "add", "-b", "added", added)
	wsID, err := r.AdoptWorkspace(ctx, added, "")
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := r.apply(ctx, rows, probes, since)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range out[0].Workspaces {
		if w.ID == wsID && w.IsGone {
			t.Error("a workspace recorded between probe and apply was marked gone")
		}
	}
}

func TestSnapshotReportsGoneInTheSameList(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	dir := filepath.Join(t.TempDir(), "p")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AdoptSession(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	projects, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || !projects[0].IsGone {
		t.Errorf("the first list after the dir went should show it gone: %+v", projects)
	}
}

func TestWorkspaceDir(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	dir := t.TempDir()
	wsID, err := r.AdoptSession(ctx, dir)
	if err != nil || wsID == "" {
		t.Fatalf("AdoptSession: %v", err)
	}
	got, ok, err := r.WorkspaceDir(ctx, wsID)
	if err != nil || !ok || got == "" {
		t.Fatalf("WorkspaceDir: got=%q ok=%v err=%v", got, ok, err)
	}
	if _, ok, _ := r.WorkspaceDir(ctx, "nonexistent"); ok {
		t.Error("unknown workspace id must return ok=false")
	}
}

func TestProjectCuration(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	if _, err := r.AdoptSession(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ps, _ := r.Snapshot(ctx)
	id := ps[0].ID

	if err := r.RenameProject(ctx, id, "My Project"); err != nil {
		t.Fatal(err)
	}
	for range 2 { // setting the same value again is not "unknown"
		if err := r.SetProjectHidden(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.SetProjectPinned(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	ps, _ = r.Snapshot(ctx)
	if p := ps[0]; p.Name != "My Project" || !p.Hidden || !p.Pinned {
		t.Fatalf("curation not applied: %+v", p)
	}
}

func TestForgetProjectDropsItsRows(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	root, _ := repoWithWorktree(t)
	if _, err := r.AdoptSession(ctx, root); err != nil {
		t.Fatal(err)
	}
	ps, _ := r.Snapshot(ctx)
	id, wsID := ps[0].ID, ps[0].Workspaces[0].ID
	if err := r.ForgetProject(ctx, id); err != nil {
		t.Fatal(err)
	}
	if ps, _ := r.Snapshot(ctx); len(ps) != 0 {
		t.Errorf("a forgotten project should leave the list: %+v", ps)
	}
	if _, ok, _ := r.WorkspaceDir(ctx, wsID); ok {
		t.Error("a forgotten project's workspaces should be gone too")
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("forget must not touch the files: %v", err)
	}
	if err := r.ForgetProject(ctx, id); !errors.Is(err, ErrUnknownProject) {
		t.Errorf("forget of an unknown project: err=%v", err)
	}
}

func TestCurationRejectsUnknownProject(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	for name, err := range map[string]error{
		"rename": r.RenameProject(ctx, "nope", "x"),
		"hide":   r.SetProjectHidden(ctx, "nope", true),
		"pin":    r.SetProjectPinned(ctx, "nope", true),
	} {
		if !errors.Is(err, ErrUnknownProject) {
			t.Errorf("%s of an unknown project: err=%v, want ErrUnknownProject", name, err)
		}
	}
}

func TestStatePersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "argus.db")
	dir := t.TempDir()
	t.Setenv("HOME", os.TempDir())

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

func TestWorkspaceTargetResolvesAndStores(t *testing.T) {
	ctx := context.Background()
	root, _ := repoWithWorktree(t)
	r := newRegistry(t)
	wsID, err := r.AdoptSession(ctx, root)
	if err != nil {
		t.Fatal(err)
	}

	ps, _ := r.Snapshot(ctx)
	if ps[0].DefaultBranch != "main" || ps[0].Workspaces[0].TargetBranch != "main" {
		t.Fatalf("unset target should resolve to the default: project=%q ws=%q", ps[0].DefaultBranch, ps[0].Workspaces[0].TargetBranch)
	}
	if err := r.SetWorkspaceTarget(ctx, wsID, "develop"); err != nil {
		t.Fatal(err)
	}
	if b, ok, err := r.TargetBranch(ctx, wsID); err != nil || !ok || b != "develop" {
		t.Fatalf("TargetBranch = %q ok=%v err=%v, want develop", b, ok, err)
	}
	if err := r.SetWorkspaceTarget(ctx, wsID, ""); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := r.TargetBranch(ctx, wsID); b != "main" {
		t.Errorf("reset target = %q, want main", b)
	}
}

func TestAdoptWorkspaceStoresTargetAtomically(t *testing.T) {
	ctx := context.Background()
	root, wt := repoWithWorktree(t)
	r := newRegistry(t)
	if _, err := r.AdoptSession(ctx, root); err != nil {
		t.Fatal(err)
	}
	wsID, err := r.AdoptWorkspace(ctx, wt, "develop")
	if err != nil {
		t.Fatal(err)
	}
	if b, _, _ := r.TargetBranch(ctx, wsID); b != "develop" {
		t.Errorf("target = %q, want develop", b)
	}
	if _, err := r.AdoptSession(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := r.TargetBranch(ctx, wsID); b != "develop" {
		t.Errorf("a later session adoption changed the target to %q", b)
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

func TestAdoptReportsNewWorkspace(t *testing.T) {
	ctx := context.Background()
	r := newRegistry(t)
	dir := t.TempDir()
	if _, isNew, err := r.Adopt(ctx, dir); err != nil || !isNew {
		t.Fatalf("first adopt: isNew=%v err=%v", isNew, err)
	}
	if _, isNew, err := r.Adopt(ctx, dir); err != nil || isNew {
		t.Errorf("second adopt: isNew=%v err=%v", isNew, err)
	}
}
