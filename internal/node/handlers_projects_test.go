package node

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/projectreg"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func nodeWithRegistry(t *testing.T) *Node {
	t.Helper()
	d := newTestNode(t)
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "argus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	d.SetProjectRegistry(projectreg.New(sqlDB))
	return d
}

func TestAdoptSessionWorkspaceAndProjectList(t *testing.T) {
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
	d.reg.ReconcileSessions("claude", []registry.DiscoveredSession{{
		AgentSessionID: "s1", Cwd: dir, Frontend: session.FrontendTmux,
	}})
	var id string
	for _, s := range d.reg.Snapshot() {
		id = s.ID
	}

	s, _ := d.reg.Get(id)
	d.adoptSessionWorkspace(ctx, s)

	got, _ := d.reg.Get(id)
	if got.WorkspaceID == "" {
		t.Fatal("session workspace_id not set")
	}

	res, err := d.handleProjectList(ctx, nil)
	if err != nil {
		t.Fatalf("handleProjectList: %v", err)
	}
	plr := res.(api.ProjectListResult)
	if len(plr.Projects) != 1 || plr.Projects[0].Kind != "git" {
		t.Fatalf("want 1 git project, got %+v", plr.Projects)
	}
	found := false
	for _, w := range plr.Projects[0].Workspaces {
		if w.ID == got.WorkspaceID {
			found = true
		}
	}
	if !found {
		t.Errorf("session workspace_id %q not found in project tree", got.WorkspaceID)
	}
}

func TestProjectListEmptyWithoutRegistry(t *testing.T) {
	d := newTestNode(t)
	res, err := d.handleProjectList(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(res.(api.ProjectListResult).Projects); got != 0 {
		t.Fatalf("want 0 projects without registry, got %d", got)
	}
}
