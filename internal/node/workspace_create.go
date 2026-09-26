package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/forge"
	"github.com/MunifTanjim/argus/internal/gittree"
)

const fetchTimeout = 15 * time.Second

// createTimeout bounds a whole create below the client's 30s call limit, so
// a slow gh or fetch fails here instead of finishing after the client gave up.
var createTimeout = 25 * time.Second

// createPlan is what a source resolves to before any git change.
type createPlan struct {
	branch string // branch the worktree ends up on
	target string // stored target ("" = default branch)
	prompt string
	pr     int // > 0: check out this PR after a detached add
}

func invalid(format string, a ...any) error {
	return &api.RPCError{Code: api.CodeInvalidRequest, Message: fmt.Sprintf(format, a...)}
}

func (d *Node) handleWorkspaceCreate(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkspaceCreateParams](params)
	if err != nil {
		return nil, err
	}
	if d.projreg == nil {
		return nil, invalid("project registry disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	if p.Source == "" {
		p.Source = api.SourceNew
	}
	p.Branch, p.TargetBranch = strings.TrimSpace(p.Branch), strings.TrimSpace(p.TargetBranch)
	if err := checkCreateInput(p); err != nil {
		return nil, err
	}
	if p.TargetBranch != "" && !gittree.ValidBranchName(ctx, p.TargetBranch) {
		return nil, invalid("invalid target branch: %q", p.TargetBranch)
	}
	_, mainDir, ok, err := d.projreg.ProjectInfo(ctx, p.ProjectID)
	if err != nil {
		return nil, invalid("%s", err)
	}
	if !ok {
		return nil, invalid("unknown project: %s", p.ProjectID)
	}
	kind, err := d.projreg.ProjectKind(ctx, p.ProjectID)
	if err != nil {
		return nil, invalid("%s", err)
	}
	if kind != "git" {
		return nil, invalid("not a git project; workspaces need a git repository")
	}
	if mainDir == "" {
		return nil, invalid("project has no main working tree to branch from")
	}

	plan, err := d.planCreate(ctx, p, mainDir)
	if err != nil {
		return nil, invalid("%s", err)
	}
	if !gittree.ValidBranchName(ctx, plan.branch) {
		return nil, invalid("invalid branch name: %q", plan.branch)
	}
	// .Repo is the main worktree's directory, not the renamable display name.
	path, err := renderWorktreePath(d.worktreeDirTmpl, filepath.Base(mainDir), plan.branch, mainDir)
	if err != nil {
		return nil, invalid("worktree template: %s", err)
	}
	// git creates a -b branch before it checks the path, so a busy path would
	// strand the new branch.
	if pathBusy(path) {
		return nil, invalid("worktree path already exists: %s", path)
	}

	branchExisted := gittree.RefExists(ctx, mainDir, "refs/heads/"+plan.branch)
	warning, err := d.addWorktree(ctx, p, plan, mainDir, path)
	if err != nil {
		return nil, invalid("%s", err)
	}
	rollback := func(cause error) (any, error) {
		// The request may already be cancelled (client gone or deadline hit);
		// cleanup must still run.
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer rcancel()
		_ = gittree.RemoveWorktree(rctx, mainDir, path, true)
		if !branchExisted {
			_ = gittree.DeleteBranch(rctx, mainDir, plan.branch)
		}
		return nil, invalid("%s", cause)
	}
	if plan.pr > 0 {
		prov, err := d.forgeFor(ctx, mainDir)
		if err != nil {
			return rollback(err)
		}
		if err := prov.CheckoutPR(ctx, path, plan.pr, plan.branch); err != nil {
			return rollback(err)
		}
	}
	wsID, err := d.projreg.AdoptWorkspace(ctx, path, plan.target)
	if err != nil {
		return rollback(err)
	}
	res := api.WorkspaceCreateResult{WorkspaceID: wsID, Dir: path, Warning: warning, Prompt: plan.prompt}
	// Setup outlives this request, whose context ends with the reply.
	res.Setup = d.startSetup(context.WithoutCancel(ctx), wsID, mainDir)
	d.notifyProjectsChanged()
	return res, nil
}

func pathBusy(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !fi.IsDir() {
		return true
	}
	entries, err := os.ReadDir(path)
	return err != nil || len(entries) > 0
}

func checkCreateInput(p api.WorkspaceCreateParams) error {
	if p.ProjectID == "" {
		return invalid("project_id is required")
	}
	switch p.Source {
	case api.SourceNew, api.SourceBranch:
		if p.Branch == "" {
			return invalid("branch is required")
		}
	case api.SourcePR, api.SourceIssue:
		if p.Number <= 0 {
			return invalid("number is required")
		}
	default:
		return invalid("unknown source: %q", p.Source)
	}
	return nil
}

// planCreate reads the forge where needed but changes nothing on disk.
func (d *Node) planCreate(ctx context.Context, p api.WorkspaceCreateParams, mainDir string) (createPlan, error) {
	switch p.Source {
	case api.SourcePR:
		prov, err := d.forgeFor(ctx, mainDir)
		if err != nil {
			return createPlan{}, err
		}
		pr, err := prov.GetPR(ctx, mainDir, p.Number)
		if err != nil {
			return createPlan{}, err
		}
		// Head names are not unique across forks (patch-1), so the local branch is keyed by number.
		return createPlan{branch: prBranch(pr.Number), target: pr.BaseBranch, pr: pr.Number}, nil
	case api.SourceIssue:
		prov, err := d.forgeFor(ctx, mainDir)
		if err != nil {
			return createPlan{}, err
		}
		is, err := prov.GetIssue(ctx, mainDir, p.Number)
		if err != nil {
			return createPlan{}, err
		}
		branch, err := renderIssueBranch(d.issueTmpl, is)
		if err != nil {
			return createPlan{}, fmt.Errorf("issue branch template: %w", err)
		}
		return createPlan{branch: branch, target: p.TargetBranch, prompt: issuePrompt(is)}, nil
	}
	return createPlan{branch: p.Branch, target: p.TargetBranch}, nil
}

func (d *Node) addWorktree(ctx context.Context, p api.WorkspaceCreateParams, plan createPlan, mainDir, path string) (string, error) {
	switch p.Source {
	case api.SourceBranch:
		if gittree.RefExists(ctx, mainDir, "refs/heads/"+plan.branch) {
			return "", gittree.AddWorktree(ctx, mainDir, path, plan.branch, "")
		}
		if gittree.RefExists(ctx, mainDir, "origin/"+plan.branch) {
			return "", gittree.AddWorktreeTracking(ctx, mainDir, path, plan.branch)
		}
		return "", fmt.Errorf("unknown branch: %s", plan.branch)
	case api.SourcePR:
		// The start point is irrelevant: CheckoutPR moves the worktree to the PR head.
		return "", gittree.AddWorktreeDetached(ctx, mainDir, path, "HEAD")
	}
	if gittree.RefExists(ctx, mainDir, "refs/heads/"+plan.branch) {
		// A removed workspace keeps its branch; reuse it rather than fail.
		return "branch " + plan.branch + " exists; checked it out as is", gittree.AddWorktree(ctx, mainDir, path, plan.branch, "")
	}
	target := plan.target
	if target == "" {
		target = gittree.DefaultBranch(ctx, mainDir)
	}
	if target == "" {
		return "", fmt.Errorf("cannot resolve a target branch")
	}
	start, warning := startRef(ctx, mainDir, target)
	return warning, gittree.AddWorktree(ctx, mainDir, path, plan.branch, start)
}

func startRef(ctx context.Context, mainDir, target string) (ref, warning string) {
	if !gittree.HasRemote(ctx, mainDir, "origin") {
		return target, ""
	}
	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	if err := gittree.Fetch(fctx, mainDir, "origin", target); err == nil && gittree.RefExists(ctx, mainDir, "origin/"+target) {
		return "origin/" + target, ""
	}
	return target, "fetch failed; branched from local " + target
}

func prBranch(number int) string {
	return "pr-" + strconv.Itoa(number)
}

func renderIssueBranch(tmpl string, is forge.Issue) (string, error) {
	if tmpl == "" {
		tmpl = "issue-{{.Issue.Number}}-{{.Issue.Slug}}"
	}
	t, err := parseTemplate("issue-branch", tmpl, issueBranchVars)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	data := map[string]any{"Issue": map[string]any{"Number": is.Number, "Title": is.Title, "Slug": forge.Slug(is.Title)}}
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return strings.Trim(b.String(), "-"), nil
}

func issuePrompt(is forge.Issue) string {
	parts := []string{is.Title}
	if strings.TrimSpace(is.Body) != "" {
		parts = append(parts, is.Body)
	}
	if is.URL != "" {
		parts = append(parts, is.URL)
	}
	return strings.Join(parts, "\n\n")
}
