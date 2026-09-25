package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/forge"
	"github.com/MunifTanjim/argus/internal/gittree"
)

type fakeForge struct {
	pr       forge.PullRequest
	issue    forge.Issue
	checkout func(dir, branch string) error
}

func (f fakeForge) ListPRs(context.Context, string) ([]forge.PullRequest, error) {
	return []forge.PullRequest{f.pr}, nil
}
func (f fakeForge) GetPR(context.Context, string, int) (forge.PullRequest, error) { return f.pr, nil }
func (f fakeForge) ListIssues(context.Context, string) ([]forge.Issue, error) {
	return []forge.Issue{f.issue}, nil
}
func (f fakeForge) GetIssue(context.Context, string, int) (forge.Issue, error) { return f.issue, nil }
func (f fakeForge) CheckoutPR(_ context.Context, dir string, _ int, branch string) error {
	return f.checkout(dir, branch)
}

func checkoutBranch(dir, branch string) error {
	return exec.Command("git", "-C", dir, "checkout", "-b", branch).Run()
}

// createFixture returns a node with a registry and a one-commit repo adopted as
// a project, plus that project's id and main dir.
func createFixture(t *testing.T) (*Node, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")
	d := nodeWithRegistry(t)
	d.SetWorktreeDirTemplate(".worktrees/{{.Branch}}")
	d.SetIssueBranchTemplate("{{.Number}}-{{.Slug}}")
	if _, err := d.projreg.AdoptSession(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	ps, _ := d.projreg.Snapshot(context.Background())
	loc, _ := gittree.Resolve(context.Background(), dir)
	return d, ps[0].ID, loc.WorktreeRoot
}

func create(t *testing.T, d *Node, p api.WorkspaceCreateParams) (api.WorkspaceCreateResult, error) {
	t.Helper()
	raw, _ := json.Marshal(p)
	res, err := d.handleWorkspaceCreate(context.Background(), raw)
	if err != nil {
		return api.WorkspaceCreateResult{}, err
	}
	return res.(api.WorkspaceCreateResult), nil
}

func storedTarget(t *testing.T, d *Node, wsID string) string {
	t.Helper()
	b, _, err := d.projreg.TargetBranch(context.Background(), wsID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCreateNewWithoutOriginHasNoWarning(t *testing.T) {
	d, projID, _ := createFixture(t)
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat/login"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Warning != "" {
		t.Errorf("local-only repo got a warning: %q", res.Warning)
	}
	if !strings.HasSuffix(res.Dir, filepath.Join(".worktrees", "feat", "login")) {
		t.Errorf("slash branch dir = %q", res.Dir)
	}
	if loc, _ := gittree.Resolve(context.Background(), res.Dir); loc.Branch != "feat/login" {
		t.Errorf("branch = %q", loc.Branch)
	}
}

func TestCreateNewFetchFailureFallsBackWithWarning(t *testing.T) {
	d, projID, main := createFixture(t)
	runGit(t, main, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing"))
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourceNew, Branch: "x", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(res.Warning, "main") {
		t.Errorf("warning = %q, want it to name the local fallback", res.Warning)
	}
	if got := storedTarget(t, d, res.WorkspaceID); got != "main" {
		t.Errorf("target = %q", got)
	}
}

func TestCreateNewWithUnknownTargetLeavesNothing(t *testing.T) {
	d, projID, main := createFixture(t)
	_, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "y", TargetBranch: "nope"})
	if rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("err = %v, want CodeInvalidRequest", err)
	}
	if _, serr := os.Stat(filepath.Join(main, ".worktrees", "y")); !os.IsNotExist(serr) {
		t.Error("a failed create left a worktree")
	}
	ps, _ := d.projreg.Snapshot(context.Background())
	if len(ps[0].Workspaces) != 1 {
		t.Errorf("a failed create left a workspace row: %+v", ps[0].Workspaces)
	}
}

func TestCreateFromExistingBranch(t *testing.T) {
	d, projID, main := createFixture(t)
	runGit(t, main, "branch", "existing")
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourceBranch, Branch: "existing", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if loc, _ := gittree.Resolve(context.Background(), res.Dir); loc.Branch != "existing" {
		t.Errorf("branch = %q", loc.Branch)
	}
	if _, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourceBranch, Branch: "ghost"}); err == nil {
		t.Error("an unknown branch should fail")
	}
}

func TestCreateFromPRStoresBaseAsTarget(t *testing.T) {
	d, projID, _ := createFixture(t)
	d.forgeFor = func(context.Context, string) (forge.Provider, error) {
		return fakeForge{
			pr:       forge.PullRequest{Number: 9, HeadBranch: "pr-head", BaseBranch: "release"},
			checkout: checkoutBranch,
		}, nil
	}
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourcePR, Number: 9})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasSuffix(res.Dir, "pr-9") {
		t.Errorf("dir = %q, want it named after the PR number", res.Dir)
	}
	if got := storedTarget(t, d, res.WorkspaceID); got != "release" {
		t.Errorf("target = %q, want the PR base", got)
	}
}

func TestCreateFromPRsWithSameHeadName(t *testing.T) {
	d, projID, main := createFixture(t)
	runGit(t, main, "branch", "patch-1")
	for _, n := range []int{3, 4} {
		d.forgeFor = func(context.Context, string) (forge.Provider, error) {
			return fakeForge{pr: forge.PullRequest{Number: n, HeadBranch: "patch-1", BaseBranch: "main"}, checkout: checkoutBranch}, nil
		}
		res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourcePR, Number: n})
		if err != nil {
			t.Fatalf("PR %d: %v", n, err)
		}
		want := "pr-" + strconv.Itoa(n)
		if loc, _ := gittree.Resolve(context.Background(), res.Dir); loc.Branch != want || filepath.Base(res.Dir) != want {
			t.Errorf("PR %d: branch = %q, dir = %q; want both %q", n, loc.Branch, res.Dir, want)
		}
	}
}

func TestCreateFromPRRollsBackOnCheckoutFailure(t *testing.T) {
	d, projID, main := createFixture(t)
	d.forgeFor = func(context.Context, string) (forge.Provider, error) {
		return fakeForge{
			pr:       forge.PullRequest{Number: 9, HeadBranch: "bad", BaseBranch: "main"},
			checkout: func(string, string) error { return errors.New("checkout failed") },
		}, nil
	}
	if _, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourcePR, Number: 9}); err == nil {
		t.Fatal("expected the checkout failure")
	}
	if _, err := os.Stat(filepath.Join(main, ".worktrees", "pr-9")); !os.IsNotExist(err) {
		t.Error("rollback left the worktree")
	}
}

func TestCreateRollbackDeletesOnlyBranchesItCreated(t *testing.T) {
	d, projID, main := createFixture(t)
	runGit(t, main, "branch", "pr-8")
	for n, kept := range map[int]bool{7: false, 8: true} {
		d.forgeFor = func(context.Context, string) (forge.Provider, error) {
			return fakeForge{
				pr: forge.PullRequest{Number: n, BaseBranch: "main"},
				checkout: func(dir, branch string) error {
					_ = exec.Command("git", "-C", dir, "checkout", "-B", branch).Run()
					return errors.New("checkout failed")
				},
			}, nil
		}
		if _, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourcePR, Number: n}); err == nil {
			t.Fatalf("PR %d: expected the checkout failure", n)
		}
		branch := "pr-" + strconv.Itoa(n)
		if got := gittree.RefExists(context.Background(), main, "refs/heads/"+branch); got != kept {
			t.Errorf("after rollback, branch %s exists = %v, want %v", branch, got, kept)
		}
	}
}

func TestCreateFromIssueNamesBranchAndReturnsPrompt(t *testing.T) {
	d, projID, _ := createFixture(t)
	d.forgeFor = func(context.Context, string) (forge.Provider, error) {
		return fakeForge{issue: forge.Issue{Number: 42, Title: "Fix login crash", Body: "steps", URL: "u"}}, nil
	}
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourceIssue, Number: 42})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if loc, _ := gittree.Resolve(context.Background(), res.Dir); loc.Branch != "42-fix-login-crash" {
		t.Errorf("branch = %q", loc.Branch)
	}
	if res.Prompt != "Fix login crash\n\nsteps\n\nu" {
		t.Errorf("prompt = %q", res.Prompt)
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	d, projID, _ := createFixture(t)
	for _, p := range []api.WorkspaceCreateParams{
		{ProjectID: projID, Source: "nope", Branch: "a"},
		{ProjectID: projID, Source: api.SourceNew},
		{ProjectID: projID, Source: api.SourcePR},
		{ProjectID: projID, Branch: "a..b"},
	} {
		if _, err := create(t, d, p); rpcCode(err) != api.CodeInvalidRequest {
			t.Errorf("%+v: err = %v, want CodeInvalidRequest", p, err)
		}
	}
}

func TestRenderIssueBranchEmptySlug(t *testing.T) {
	got, err := renderIssueBranch("{{.Number}}-{{.Slug}}", forge.Issue{Number: 42, Title: "修复"})
	if err != nil || got != "42" {
		t.Errorf("renderIssueBranch = %q, %v; want 42", got, err)
	}
}

func TestPickerReadCallsAndSetTarget(t *testing.T) {
	d, projID, main := createFixture(t)
	runGit(t, main, "branch", "other")
	ctx := context.Background()

	raw, _ := json.Marshal(api.ProjectRef{ProjectID: projID})
	res, err := d.handleProjectBranches(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]api.BranchInfo{}
	for _, b := range res.(api.BranchesResult).Branches {
		names[b.Name] = b
	}
	if !names["main"].CheckedOut || names["other"].CheckedOut || !names["other"].Local {
		t.Errorf("branches = %+v", names)
	}

	d.forgeFor = func(context.Context, string) (forge.Provider, error) {
		return fakeForge{pr: forge.PullRequest{Number: 1, Title: "t", HeadBranch: "h", BaseBranch: "b"}, issue: forge.Issue{Number: 2, Title: "i"}}, nil
	}
	if res, err := d.handleProjectPRs(ctx, raw); err != nil || len(res.(api.PRsResult).PRs) != 1 {
		t.Errorf("prs = %+v, %v", res, err)
	}
	if res, err := d.handleProjectIssues(ctx, raw); err != nil || res.(api.IssuesResult).Issues[0].Title != "i" {
		t.Errorf("issues = %+v, %v", res, err)
	}
	d.forgeFor = func(context.Context, string) (forge.Provider, error) { return nil, forge.ErrNoProvider }
	if _, err := d.handleProjectPRs(ctx, raw); rpcCode(err) != api.CodeInvalidRequest || err.Error() != forge.ErrNoProvider.Error() {
		t.Errorf("no provider: err = %v", err)
	}

	ps, _ := d.projreg.Snapshot(ctx)
	wsID := ps[0].Workspaces[0].ID
	st, _ := json.Marshal(api.WorkspaceSetTargetParams{WorkspaceID: wsID, TargetBranch: "other"})
	if _, err := d.handleWorkspaceSetTarget(ctx, st); err != nil {
		t.Fatal(err)
	}
	pl, _ := d.handleProjectList(ctx, nil)
	if got := pl.(api.ProjectListResult).Projects[0].Workspaces[0].TargetBranch; got != "other" {
		t.Errorf("project.list target = %q, want other", got)
	}
}

func TestWorkspaceDiffAgainstTarget(t *testing.T) {
	d, projID, _ := createFixture(t)
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat", TargetBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(res.Dir, "c.txt"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, res.Dir, "add", ".")
	runGit(t, res.Dir, "commit", "-m", "c")
	ctx := context.Background()

	wt, _ := json.Marshal(api.WorkspaceRef{WorkspaceID: res.WorkspaceID})
	r, _ := d.handleWorkspaceChangedFiles(ctx, wt)
	if n := len(r.(api.ChangedFilesResult).Files); n != 0 {
		t.Errorf("uncommitted mode: %d files, want 0", n)
	}
	tg, _ := json.Marshal(api.WorkspaceRef{WorkspaceID: res.WorkspaceID, Against: api.AgainstTarget})
	r, err = d.handleWorkspaceChangedFiles(ctx, tg)
	if err != nil || len(r.(api.ChangedFilesResult).Files) != 1 {
		t.Fatalf("target mode = %+v, %v; want c.txt", r, err)
	}
	df, _ := json.Marshal(api.WorkspaceFileParams{WorkspaceID: res.WorkspaceID, Path: "c.txt", Against: api.AgainstTarget})
	dr, err := d.handleWorkspaceDiff(ctx, df)
	if err != nil || !strings.Contains(dr.(api.WorkspaceDiffResult).Diff, "+c") {
		t.Errorf("target diff = %+v, %v", dr, err)
	}
}

func TestCreateNewFromFetchedTargetHasNoUpstream(t *testing.T) {
	d, projID, main := createFixture(t)
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare", "-b", "main")
	runGit(t, main, "remote", "add", "origin", remote)
	runGit(t, main, "push", "origin", "main")
	res, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "feat-x", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Warning != "" {
		t.Errorf("fetch from a reachable origin warned: %q", res.Warning)
	}
	out, _ := exec.Command("git", "-C", res.Dir, "config", "--get", "branch.feat-x.merge").Output()
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("new branch tracks %q; a push could target the base branch", got)
	}
}

func TestCreateRejectsOptionLikeTarget(t *testing.T) {
	d, projID, _ := createFixture(t)
	_, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "z", TargetBranch: "--force"})
	if rpcCode(err) != api.CodeInvalidRequest || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want an invalid target error", err)
	}
}

type slowForge struct{ fakeForge }

func (f slowForge) GetPR(ctx context.Context, _ string, _ int) (forge.PullRequest, error) {
	select {
	case <-time.After(2 * time.Second):
		return f.pr, nil
	case <-ctx.Done():
		return forge.PullRequest{}, ctx.Err()
	}
}

func TestCreateHasOneDeadline(t *testing.T) {
	d, projID, _ := createFixture(t)
	orig := createTimeout
	createTimeout = 200 * time.Millisecond
	t.Cleanup(func() { createTimeout = orig })
	d.forgeFor = func(context.Context, string) (forge.Provider, error) {
		return slowForge{fakeForge{
			pr:       forge.PullRequest{Number: 9, HeadBranch: "slow", BaseBranch: "main"},
			checkout: checkoutBranch,
		}}, nil
	}
	start := time.Now()
	_, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourcePR, Number: 9})
	if err == nil || time.Since(start) > time.Second {
		t.Errorf("create should stop at its deadline: err=%v after %v", err, time.Since(start))
	}
}

func TestCreateRollbackSurvivesCancelledRequest(t *testing.T) {
	d, projID, main := createFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.forgeFor = func(context.Context, string) (forge.Provider, error) {
		return fakeForge{
			pr:       forge.PullRequest{Number: 9, HeadBranch: "gone", BaseBranch: "main"},
			checkout: func(string, string) error { cancel(); return errors.New("killed") },
		}, nil
	}
	raw, _ := json.Marshal(api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourcePR, Number: 9})
	if _, err := d.handleWorkspaceCreate(ctx, raw); err == nil {
		t.Fatal("expected the checkout failure")
	}
	if _, err := os.Stat(filepath.Join(main, ".worktrees", "pr-9")); !os.IsNotExist(err) {
		t.Error("rollback after a cancelled request left the worktree")
	}
}

func TestCreateFromPRWithoutLocalDefaultBranch(t *testing.T) {
	d, projID, main := createFixture(t)
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare", "-b", "main")
	runGit(t, main, "remote", "add", "origin", remote)
	runGit(t, main, "push", "origin", "main")
	runGit(t, main, "remote", "set-head", "origin", "main")
	runGit(t, main, "branch", "-m", "main", "trunk") // origin/HEAD still says main
	d.forgeFor = func(context.Context, string) (forge.Provider, error) {
		return fakeForge{
			pr:       forge.PullRequest{Number: 5, HeadBranch: "pr5", BaseBranch: "main"},
			checkout: checkoutBranch,
		}, nil
	}
	if _, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Source: api.SourcePR, Number: 5}); err != nil {
		t.Fatalf("PR create must not depend on a local default branch: %v", err)
	}
}

func TestCreateRefusesBusyPathWithoutStrandingBranch(t *testing.T) {
	d, projID, main := createFixture(t)
	busy := filepath.Join(main, ".worktrees", "busy")
	if err := os.MkdirAll(busy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(busy, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := create(t, d, api.WorkspaceCreateParams{ProjectID: projID, Branch: "busy"})
	if rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("err = %v, want CodeInvalidRequest", err)
	}
	if gittree.RefExists(context.Background(), main, "refs/heads/busy") {
		t.Error("a refused create left the new branch behind")
	}
}
