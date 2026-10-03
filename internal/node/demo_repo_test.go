package node

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestMaterializeDemoReposCreatesDirtyRepo(t *testing.T) {
	repoSpec, err := filepath.Abs("testdata/repo/webapp")
	if err != nil {
		t.Fatal(err)
	}
	dd := &DemoData{Nodes: []DemoNode{{
		ID:       "n1",
		Sessions: []session.Session{{ID: "s1", Agent: "claude"}},
		Repos:    map[string]string{"s1": repoSpec},
	}}}
	cleanup, err := MaterializeDemoRepos(dd)
	if err != nil {
		t.Fatalf("MaterializeDemoRepos: %v", err)
	}
	defer cleanup()

	cwd := dd.Nodes[0].Sessions[0].Cwd
	if cwd == "" {
		t.Fatal("session cwd not rewritten")
	}
	if _, err := os.Stat(filepath.Join(cwd, ".git")); err != nil {
		t.Fatalf("no git repo at cwd: %v", err)
	}
	out, err := gitOut(cwd, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if !strings.Contains(out, "main.go") {
		t.Fatalf("expected dirty main.go, got %q", out)
	}
}

func TestMaterializeDemoReposBuildsWorkspaceRepo(t *testing.T) {
	repoSpec, err := filepath.Abs("testdata/repo/webapp")
	if err != nil {
		t.Fatal(err)
	}
	dd := &DemoData{Nodes: []DemoNode{{
		ID:             "n1",
		Label:          "n1",
		WorkspaceRepos: map[string]string{"w1": repoSpec},
	}}}
	cleanup, err := MaterializeDemoRepos(dd)
	if err != nil {
		t.Fatalf("MaterializeDemoRepos: %v", err)
	}
	defer cleanup()

	dir := dd.Nodes[0].WorkspaceRepos["w1"]
	if dir == repoSpec {
		t.Fatal("workspace repo not materialized")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("no git repo at %s: %v", dir, err)
	}

	nodes, err := BuildDemoNodes(dd, "test")
	if err != nil {
		t.Fatalf("BuildDemoNodes: %v", err)
	}
	got, err := nodes[0].workspaceDir(context.Background(), "w1")
	if err != nil || got != dir {
		t.Fatalf("workspaceDir(w1) = %q, %v; want %q", got, err, dir)
	}
	if _, err := nodes[0].workspaceDir(context.Background(), "w2"); err == nil {
		t.Fatal("workspaceDir for a workspace with no repo must fail")
	}
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	return string(b), err
}

func TestDemoWorkspaceTargetHandlers(t *testing.T) {
	repoSpec, err := filepath.Abs("testdata/repo/webapp")
	if err != nil {
		t.Fatal(err)
	}
	dd := &DemoData{Nodes: []DemoNode{{
		ID:    "n1",
		Label: "n1",
		Projects: []api.ProjectNode{{
			ID: "p1", Name: "web", Kind: "git", DefaultBranch: "main",
			Workspaces: []api.WorkspaceNode{{ID: "w1", IsMain: true, Branch: "main", TargetBranch: "main"}},
		}},
		WorkspaceRepos: map[string]string{"w1": repoSpec},
	}}}
	cleanup, err := MaterializeDemoRepos(dd)
	if err != nil {
		t.Fatalf("MaterializeDemoRepos: %v", err)
	}
	defer cleanup()
	nodes, err := BuildDemoNodes(dd, "test")
	if err != nil {
		t.Fatalf("BuildDemoNodes: %v", err)
	}
	d, ctx := nodes[0], context.Background()

	if _, err := d.handleWorkspaceCommits(ctx, mustJSON(api.WorkspaceRef{WorkspaceID: "w1"})); err != nil {
		t.Fatalf("handleWorkspaceCommits: %v", err)
	}
	if _, err := d.handleWorkspaceChangedFiles(ctx, mustJSON(api.WorkspaceRef{WorkspaceID: "w1", Against: api.AgainstTarget})); err != nil {
		t.Fatalf("handleWorkspaceChangedFiles against target: %v", err)
	}
	if _, err := d.handleWorkspaceSetTarget(ctx, mustJSON(api.WorkspaceSetTargetParams{WorkspaceID: "w1", TargetBranch: "dev"})); err == nil {
		t.Fatal("handleWorkspaceSetTarget on a demo node must fail")
	}
}

func TestBuildRepoBranchCommitsAndStaged(t *testing.T) {
	spec, err := filepath.Abs("testdata/repo/branchy")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := buildRepo(dir, spec); err != nil {
		t.Fatalf("buildRepo: %v", err)
	}
	branch, _ := gitOut(dir, "branch", "--show-current")
	if strings.TrimSpace(branch) != "feature" {
		t.Fatalf("branch = %q, want feature", branch)
	}
	log, _ := gitOut(dir, "log", "--format=%s", "main..HEAD")
	if strings.TrimSpace(log) != "add n\nchange a" {
		t.Fatalf("commits ahead of main = %q, want add n, change a", log)
	}
	status, _ := gitOut(dir, "status", "--porcelain")
	for _, want := range []string{"M  b.txt", " M c.txt", "?? u.txt"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status missing %q:\n%s", want, status)
		}
	}
}
