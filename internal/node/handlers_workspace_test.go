package node

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestWorkspaceCreateAndRemove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()

	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")

	d := nodeWithRegistry(t)
	d.SetWorktreeDirTemplate(".worktrees/{{.Branch}}")
	if _, err := d.projreg.AdoptSession(ctx, dir); err != nil {
		t.Fatal(err)
	}
	projects, _ := d.projreg.Snapshot(ctx)
	projID := projects[0].ID

	raw, _ := json.Marshal(api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	res, err := d.handleWorkspaceCreate(ctx, raw)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	cr := res.(api.WorkspaceCreateResult)
	if cr.Dir == "" {
		t.Fatal("create returned no dir")
	}
	if _, err := os.Stat(cr.Dir); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}

	var mainWsID string
	for _, w := range projects[0].Workspaces {
		if w.IsMain {
			mainWsID = w.ID
		}
	}
	badRaw, _ := json.Marshal(api.WorkspaceRemoveParams{WorkspaceID: mainWsID})
	if _, err := d.handleWorkspaceRemove(ctx, badRaw); rpcCode(err) != api.CodeInvalidRequest {
		t.Errorf("removing main worktree: err = %v, want CodeInvalidRequest", err)
	}

	rmRaw, _ := json.Marshal(api.WorkspaceRemoveParams{WorkspaceID: cr.WorkspaceID})
	if _, err := d.handleWorkspaceRemove(ctx, rmRaw); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(cr.Dir); !os.IsNotExist(err) {
		t.Errorf("worktree not removed: %v", err)
	}
}

func TestRemoveWorkspaceGuardsLiveSessions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()

	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")

	d := nodeWithRegistry(t)
	d.SetWorktreeDirTemplate(".worktrees/{{.Branch}}")
	if _, err := d.projreg.AdoptSession(ctx, dir); err != nil {
		t.Fatal(err)
	}
	projects, _ := d.projreg.Snapshot(ctx)

	raw, _ := json.Marshal(api.WorkspaceCreateParams{ProjectID: projects[0].ID, Branch: "feat"})
	res, err := d.handleWorkspaceCreate(ctx, raw)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	cr := res.(api.WorkspaceCreateResult)

	// A live session inside the new worktree blocks a non-force remove.
	d.reg.ReconcileSessions("claude", []registry.DiscoveredSession{{
		AgentSessionID: "s1", Cwd: cr.Dir, Frontend: session.FrontendTmux,
	}})
	for _, s := range d.reg.Snapshot() {
		d.adoptSessionWorkspace(ctx, s)
	}

	rm, _ := json.Marshal(api.WorkspaceRemoveParams{WorkspaceID: cr.WorkspaceID})
	if _, err := d.handleWorkspaceRemove(ctx, rm); rpcCode(err) != api.CodeInvalidRequest {
		t.Errorf("remove with a live session: err = %v, want CodeInvalidRequest", err)
	}

	// Force removes despite the live session.
	rmF, _ := json.Marshal(api.WorkspaceRemoveParams{WorkspaceID: cr.WorkspaceID, Force: true})
	if _, err := d.handleWorkspaceRemove(ctx, rmF); err != nil {
		t.Fatalf("force remove: %v", err)
	}
	if _, err := os.Stat(cr.Dir); !os.IsNotExist(err) {
		t.Errorf("worktree not removed by force: %v", err)
	}
}

func TestWorkspaceListDirAndUnknownID(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()

	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")

	d := nodeWithRegistry(t)
	wsID, err := d.projreg.AdoptSession(ctx, dir)
	if err != nil || wsID == "" {
		t.Fatalf("AdoptSession: %v", err)
	}

	raw, _ := json.Marshal(api.WorkspaceFileParams{WorkspaceID: wsID})
	res, err := d.handleWorkspaceListDir(ctx, raw)
	if err != nil {
		t.Fatalf("handleWorkspaceListDir: %v", err)
	}
	found := false
	for _, e := range res.(api.ListDirResult).Entries {
		if e.Name == "main.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("main.go not listed: %+v", res)
	}

	bad, _ := json.Marshal(api.WorkspaceFileParams{WorkspaceID: "nope"})
	if _, err := d.handleWorkspaceListDir(ctx, bad); rpcCode(err) != api.CodeInvalidRequest {
		t.Errorf("unknown workspace: err = %v, want CodeInvalidRequest", err)
	}
}

func TestWorkspaceCommitsFilesAndDiff(t *testing.T) {
	d, projID, _ := createFixture(t)
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat", TargetBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(res.Dir, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, res.Dir, "add", ".")
	runGit(t, res.Dir, "commit", "-m", "add c")
	ctx := context.Background()

	ref, _ := json.Marshal(api.WorkspaceRef{WorkspaceID: res.WorkspaceID})
	r, err := d.handleWorkspaceCommits(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	cr := r.(api.CommitsResult)
	if len(cr.Commits) != 1 || cr.Commits[0].Subject != "add c" || cr.Unpushed {
		t.Fatalf("commits = %+v, want only the workspace's commit since main", cr)
	}
	sha := cr.Commits[0].SHA

	cf, _ := json.Marshal(api.WorkspaceCommitParams{WorkspaceID: res.WorkspaceID, SHA: sha})
	fr, err := d.handleWorkspaceCommitFiles(ctx, cf)
	if err != nil {
		t.Fatal(err)
	}
	if files := fr.(api.ChangedFilesResult).Files; len(files) != 1 || files[0].Path != "c.txt" || files[0].Change != "added" {
		t.Errorf("commit files = %+v, want c.txt added", files)
	}

	df, _ := json.Marshal(api.WorkspaceFileParams{WorkspaceID: res.WorkspaceID, Path: "c.txt", Rev: sha})
	dr, err := d.handleWorkspaceDiff(ctx, df)
	if err != nil || !strings.Contains(dr.(api.WorkspaceDiffResult).Diff, "+c") {
		t.Errorf("commit diff = %+v, %v", dr, err)
	}
}

func TestWorkspaceCommitsErrors(t *testing.T) {
	d, projID, _ := createFixture(t)
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat", TargetBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := d.projreg.SetWorkspaceTarget(ctx, res.WorkspaceID, "nope"); err != nil {
		t.Fatal(err)
	}
	ref, _ := json.Marshal(api.WorkspaceRef{WorkspaceID: res.WorkspaceID})
	if _, err := d.handleWorkspaceCommits(ctx, ref); rpcCode(err) != api.CodeInvalidRequest {
		t.Errorf("unknown target: err = %v, want CodeInvalidRequest", err)
	}
	bad, _ := json.Marshal(api.WorkspaceRef{WorkspaceID: "nope"})
	if _, err := d.handleWorkspaceCommits(ctx, bad); rpcCode(err) != api.CodeInvalidRequest {
		t.Errorf("unknown workspace: err = %v, want CodeInvalidRequest", err)
	}
	cf, _ := json.Marshal(api.WorkspaceCommitParams{WorkspaceID: res.WorkspaceID, SHA: "--output=x"})
	if _, err := d.handleWorkspaceCommitFiles(ctx, cf); rpcCode(err) != api.CodeInvalidRequest {
		t.Errorf("bad sha: err = %v, want CodeInvalidRequest", err)
	}
	df, _ := json.Marshal(api.WorkspaceFileParams{WorkspaceID: res.WorkspaceID, Path: "f.txt", Rev: "--output=x"})
	if _, err := d.handleWorkspaceDiff(ctx, df); rpcCode(err) != api.CodeInvalidRequest {
		t.Errorf("bad rev: err = %v, want CodeInvalidRequest", err)
	}
}

func TestWorkspaceCommitsNoTargetIsEmpty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "trunk")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")
	runGit(t, dir, "checkout", "--detach")

	d := nodeWithRegistry(t)
	wsID, err := d.projreg.AdoptSession(ctx, dir)
	if err != nil || wsID == "" {
		t.Fatalf("AdoptSession: %q, %v", wsID, err)
	}
	if target, _, _ := d.projreg.TargetBranch(ctx, wsID); target != "" {
		t.Fatalf("fixture should have no target branch, got %q", target)
	}
	ref, _ := json.Marshal(api.WorkspaceRef{WorkspaceID: wsID})
	r, err := d.handleWorkspaceCommits(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if c := r.(api.CommitsResult).Commits; c == nil || len(c) != 0 {
		t.Errorf("no target: commits = %#v, want an empty, non-nil list", c)
	}
}
