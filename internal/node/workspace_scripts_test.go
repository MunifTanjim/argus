package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gittree"
	"github.com/MunifTanjim/argus/internal/wsscript"
)

func writeArgusSettings(t *testing.T, mainDir, body string) {
	t.Helper()
	p := filepath.Join(mainDir, ".argus", "settings.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitSetup(t *testing.T, d *Node, wsID string) wsscript.Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if run, ok := d.scripts.Status(wsID); ok && run.State != wsscript.Running {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("setup did not finish")
	return wsscript.Run{}
}

func TestCreateStartsSetupInTheBackground(t *testing.T) {
	d, projID, main := createFixture(t)
	writeArgusSettings(t, main, "[scripts]\nsetup = \"git branch --show-current > setup-ran; sleep 0.3\"\n")
	start := time.Now()
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Setup == "" || time.Since(start) > 5*time.Second {
		t.Fatalf("create should report setup and return at once: %+v", res)
	}
	if run := waitSetup(t, d, res.WorkspaceID); run.State != wsscript.OK {
		t.Fatalf("setup run = %+v output=%q", run, d.scripts.Output(res.WorkspaceID))
	}
	if b, _ := os.ReadFile(filepath.Join(res.Dir, "setup-ran")); strings.TrimSpace(string(b)) != "feat" {
		t.Errorf("setup should run in the new worktree: %q", b)
	}

	lr, err := d.handleProjectList(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var ws *api.WorkspaceNode
	p := lr.(api.ProjectListResult).Projects[0]
	for i := range p.Workspaces {
		if p.Workspaces[i].ID == res.WorkspaceID {
			ws = &p.Workspaces[i]
		}
	}
	if p.Scripts == nil || p.Scripts.Setup == "" || ws == nil || ws.Setup == nil || ws.Setup.State != "ok" {
		t.Errorf("project.list should carry scripts and setup state: scripts=%+v ws=%+v", p.Scripts, ws)
	}
}

func TestSettingsParseErrorFailsSetupButKeepsWorkspace(t *testing.T) {
	d, projID, main := createFixture(t)
	writeArgusSettings(t, main, "[scripts\n")
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	if err != nil {
		t.Fatalf("a bad settings file must not fail the create: %v", err)
	}
	if run := waitSetup(t, d, res.WorkspaceID); run.State != wsscript.Failed || !strings.Contains(d.scripts.Output(res.WorkspaceID), "settings.toml") {
		t.Errorf("setup should be failed with the parse error: %+v %q", run, d.scripts.Output(res.WorkspaceID))
	}
}

func TestRunSetupAndSetupLog(t *testing.T) {
	d, projID, main := createFixture(t)
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(api.WorkspaceRef{WorkspaceID: res.WorkspaceID})
	if _, err := d.handleWorkspaceRunSetup(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "no setup script") {
		t.Errorf("runSetup with no script: %v", err)
	}
	writeArgusSettings(t, main, "[scripts]\nsetup = \"echo hello; sleep 1\"\n")
	if _, err := d.handleWorkspaceRunSetup(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := d.handleWorkspaceRunSetup(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second runSetup: %v", err)
	}
	waitSetup(t, d, res.WorkspaceID)
	out, err := d.handleWorkspaceSetupLog(context.Background(), raw)
	if err != nil || !strings.Contains(out.(api.SetupLogResult).Output, "hello") {
		t.Errorf("setupLog = %+v, %v", out, err)
	}
}

func removeWS(t *testing.T, d *Node, wsID string, force bool) (api.WorkspaceRemoveResult, error) {
	t.Helper()
	raw, _ := json.Marshal(api.WorkspaceRemoveParams{WorkspaceID: wsID, Force: force})
	res, err := d.handleWorkspaceRemove(context.Background(), raw)
	if err != nil {
		return api.WorkspaceRemoveResult{}, err
	}
	return res.(api.WorkspaceRemoveResult), nil
}

func TestTeardownRunsBeforeRemoveAndSeesFiles(t *testing.T) {
	d, projID, main := createFixture(t)
	out := filepath.Join(t.TempDir(), "teardown-saw")
	writeArgusSettings(t, main, "[scripts]\nteardown = \"ls > '"+out+"'\"\n")
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := removeWS(t, d, res.WorkspaceID, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); !strings.Contains(string(b), "f.txt") {
		t.Errorf("teardown should run in the worktree before it is removed: %q", b)
	}
	if _, err := os.Stat(res.Dir); !os.IsNotExist(err) {
		t.Error("the worktree should be removed")
	}
}

func TestFailedTeardownStopsRemoveUnlessForced(t *testing.T) {
	d, projID, main := createFixture(t)
	writeArgusSettings(t, main, "[scripts]\nteardown = \"echo cannot stop db; exit 4\"\n")
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := removeWS(t, d, res.WorkspaceID, false); err == nil || err.Error() != "teardown failed (exit 4): cannot stop db" {
		t.Errorf("remove with a failing teardown: %v", err)
	}
	if _, err := os.Stat(res.Dir); err != nil {
		t.Fatal("a failed teardown must keep the worktree")
	}
	rr, err := removeWS(t, d, res.WorkspaceID, true)
	if err != nil || rr.Warning != "teardown failed (exit 4): cannot stop db" {
		t.Errorf("forced remove: warning=%q err=%v", rr.Warning, err)
	}
}

func TestTeardownTimeout(t *testing.T) {
	old := teardownTimeout
	teardownTimeout = 300 * time.Millisecond
	defer func() { teardownTimeout = old }()
	d, projID, main := createFixture(t)
	writeArgusSettings(t, main, "[scripts]\nteardown = \"echo stopping; sleep 30\"\n")
	res, _ := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	if _, err := removeWS(t, d, res.WorkspaceID, false); err == nil || err.Error() != "teardown timed out after 300ms: stopping" {
		t.Errorf("teardown timeout: %v", err)
	}
}

func TestRemoveStopsARunningSetupFirst(t *testing.T) {
	d, projID, main := createFixture(t)
	writeArgusSettings(t, main, "[scripts]\nsetup = \"sleep 30\"\n")
	res, _ := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	start := time.Now()
	if _, err := removeWS(t, d, res.WorkspaceID, true); err != nil {
		t.Fatal(err)
	}
	if run, _ := d.scripts.Status(res.WorkspaceID); run.State == wsscript.Running || time.Since(start) > 10*time.Second {
		t.Errorf("remove should stop the setup first: %+v after %v", run, time.Since(start))
	}
}

func TestGoneWorkspaceSkipsTeardown(t *testing.T) {
	d, projID, main := createFixture(t)
	res, _ := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	writeArgusSettings(t, main, "[scripts]\nteardown = \"exit 1\"\n")
	if err := os.RemoveAll(res.Dir); err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "worktree", "prune")
	if msg := d.runTeardown(context.Background(), res.WorkspaceID, res.Dir, main); msg != "" {
		t.Errorf("a gone workspace should skip teardown: %q", msg)
	}
}

func TestNonForcedRemoveOfADirtyWorkspaceRefusesBeforeScripts(t *testing.T) {
	d, projID, main := createFixture(t)
	marker := filepath.Join(t.TempDir(), "teardown-ran")
	writeArgusSettings(t, main, "[scripts]\nsetup = \"sleep 30\"\nteardown = \"touch '"+marker+"'\"\n")
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.scripts.StopSetup(res.WorkspaceID, 5*time.Second) })
	if err := os.WriteFile(filepath.Join(res.Dir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := removeWS(t, d, res.WorkspaceID, false); err == nil || err.Error() != "workspace has uncommitted changes; force-remove discards them" {
		t.Errorf("remove of a dirty workspace: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("teardown must not run when the remove is refused")
	}
	if run, _ := d.scripts.Status(res.WorkspaceID); run.State != wsscript.Running {
		t.Errorf("the setup must keep running when the remove is refused: %+v", run)
	}
}

func TestIgnoredFilesDoNotBlockRemove(t *testing.T) {
	d, projID, main := createFixture(t)
	if err := os.WriteFile(filepath.Join(main, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "add", ".gitignore")
	runGit(t, main, "commit", "-m", "ignore")
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(res.Dir, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(res.Dir, "ignored", "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := removeWS(t, d, res.WorkspaceID, false); err != nil {
		t.Errorf("ignored files should not block a remove: %v", err)
	}
}

func TestRemoveForgetsTheSetupRun(t *testing.T) {
	d, projID, main := createFixture(t)
	writeArgusSettings(t, main, "[scripts]\nsetup = \"exit 3\"\n")
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat"})
	if err != nil {
		t.Fatal(err)
	}
	waitSetup(t, d, res.WorkspaceID)
	if _, err := removeWS(t, d, res.WorkspaceID, true); err != nil {
		t.Fatal(err)
	}
	writeArgusSettings(t, main, "")
	again, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourceBranch, Branch: "feat"})
	if err != nil {
		t.Fatal(err)
	}
	if again.WorkspaceID != res.WorkspaceID {
		t.Fatalf("the re-created workspace should reuse the id: %s vs %s", again.WorkspaceID, res.WorkspaceID)
	}
	if run, ok := d.scripts.Status(again.WorkspaceID); ok {
		t.Errorf("a re-created workspace should not inherit the old setup: %+v", run)
	}
}

// bareFixture returns a node with a bare repository's linked worktree adopted,
// plus that workspace's id and dir. Its project has no main working tree.
func bareFixture(t *testing.T) (*Node, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	seed := t.TempDir()
	runGit(t, seed, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(seed, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-m", "init")
	bare := filepath.Join(t.TempDir(), "repo.git")
	runGit(t, seed, "clone", "--bare", seed, bare)
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, bare, "worktree", "add", wt, "main")
	d := nodeWithRegistry(t)
	wsID, err := d.projreg.AdoptSession(context.Background(), wt)
	if err != nil || wsID == "" {
		t.Fatalf("adopt: %q %v", wsID, err)
	}
	loc, _ := gittree.Resolve(context.Background(), wt)
	return d, wsID, loc.WorktreeRoot
}

func TestBareRepoProjectHasNoScripts(t *testing.T) {
	d, wsID, wt := bareFixture(t)
	cwd := t.TempDir()
	writeArgusSettings(t, cwd, "[scripts]\nsetup = \"echo ran\"\nteardown = \"exit 1\"\n")
	writeArgusSettings(t, wt, "[scripts]\nsetup = \"echo ran\"\nteardown = \"exit 1\"\n")
	t.Chdir(cwd)
	raw, _ := json.Marshal(api.WorkspaceRef{WorkspaceID: wsID})
	if _, err := d.handleWorkspaceRunSetup(context.Background(), raw); err == nil || err.Error() != "no setup script" {
		t.Errorf("runSetup in a bare-repo project: %v", err)
	}
	if got, err := d.startSetup(context.Background(), wsID, ""); got != "" || !errors.Is(err, errNoSetup) {
		t.Errorf("startSetup with no main tree = %q, %v", got, err)
	}
	if _, ok := d.scripts.Status(wsID); ok {
		t.Error("a bare-repo project should record no setup run")
	}
	rr, err := removeWS(t, d, wsID, true)
	if err != nil || rr.Warning != "" {
		t.Errorf("remove should run no teardown: warning=%q err=%v", rr.Warning, err)
	}
}

func TestRunSetupRefusesPlainProject(t *testing.T) {
	d := nodeWithRegistry(t)
	dir := t.TempDir()
	writeArgusSettings(t, dir, "[scripts]\nsetup = \"echo ran\"\n")
	wsID, err := d.projreg.AdoptSession(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(api.WorkspaceRef{WorkspaceID: wsID})
	if _, err := d.handleWorkspaceRunSetup(context.Background(), raw); err == nil || err.Error() != "no setup script" {
		t.Errorf("runSetup in a plain project: %v", err)
	}
}

func TestTeardownEnvErrorIsReported(t *testing.T) {
	d, _, main := createFixture(t)
	writeArgusSettings(t, main, "[scripts]\nteardown = \"true\"\n")
	if msg := d.runTeardown(context.Background(), "nope", main, main); msg != "teardown: unknown workspace: nope" {
		t.Errorf("an env error on an existing dir should be reported: %q", msg)
	}
}
