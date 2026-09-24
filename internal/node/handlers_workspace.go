package node

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gitstatus"
	"github.com/MunifTanjim/argus/internal/gittree"
	"github.com/MunifTanjim/argus/internal/session"
)

func (d *Node) workspaceDir(ctx context.Context, id string) (string, error) {
	if d.projreg == nil {
		return "", &api.RPCError{Code: api.CodeInvalidRequest, Message: "project registry disabled"}
	}
	if id == "" {
		return "", &api.RPCError{Code: api.CodeInvalidRequest, Message: "workspace_id is required"}
	}
	dir, ok, err := d.projreg.WorkspaceDir(ctx, id)
	if err != nil {
		return "", &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	if !ok {
		return "", &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown workspace: " + id}
	}
	return dir, nil
}

func (d *Node) handleWorkspaceChangedFiles(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkspaceRef](params)
	if err != nil {
		return nil, err
	}
	dir, err := d.workspaceDir(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	base, err := d.diffBase(ctx, p.WorkspaceID, dir, p.Against)
	if err != nil {
		return nil, err
	}
	var root string
	var files []gitstatus.ChangedFile
	if base == "" {
		root, files, err = gitstatus.ChangedFiles(ctx, dir)
	} else {
		root, files, err = gitstatus.ChangedFilesSince(ctx, dir, base)
	}
	if err != nil {
		return nil, invalid("%s", err)
	}
	out := make([]api.ChangedFile, len(files))
	for i, f := range files {
		out[i] = api.ChangedFile{
			Path: f.Path, OrigPath: f.OrigPath, Change: string(f.Change),
			Staged: f.Staged, Unstaged: f.Unstaged,
		}
	}
	return api.ChangedFilesResult{Root: root, Files: out}, nil
}

func (d *Node) handleWorkspaceDiff(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkspaceFileParams](params)
	if err != nil {
		return nil, err
	}
	if p.Path == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "path is required"}
	}
	dir, err := d.workspaceDir(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	base, err := d.diffBase(ctx, p.WorkspaceID, dir, p.Against)
	if err != nil {
		return nil, err
	}
	if base == "" {
		base = "HEAD"
	}
	diff, notShown, err := gitstatus.DiffSince(ctx, dir, base, p.Path, p.OrigPath)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	return api.WorkspaceDiffResult{Path: p.Path, Diff: diff, NotShown: notShown}, nil
}

func (d *Node) handleWorkspaceListDir(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkspaceFileParams](params)
	if err != nil {
		return nil, err
	}
	dir, err := d.workspaceDir(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	root, entries, err := gitstatus.ListDir(ctx, dir, p.Path)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	out := make([]api.DirEntry, len(entries))
	for i, e := range entries {
		out[i] = api.DirEntry{Name: e.Name, Path: e.Path, IsDir: e.IsDir}
	}
	return api.ListDirResult{Root: root, Path: p.Path, Entries: out}, nil
}

func (d *Node) handleWorkspaceRemove(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkspaceRemoveParams](params)
	if err != nil {
		return nil, err
	}
	if d.projreg == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "project registry disabled"}
	}
	dir, isMain, projID, ok, err := d.projreg.WorkspaceInfo(ctx, p.WorkspaceID)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown workspace: " + p.WorkspaceID}
	}
	if isMain {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "cannot remove the main worktree"}
	}
	if !p.Force {
		if n := d.liveSessionsInWorkspace(p.WorkspaceID); n > 0 {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: fmt.Sprintf("%d live session(s) in this workspace; close them or force-remove", n)}
		}
	}
	runDir := dir
	if _, mainDir, ok, _ := d.projreg.ProjectInfo(ctx, projID); ok && mainDir != "" {
		runDir = mainDir // remove must not run from inside the worktree being removed
	}
	if err := gittree.RemoveWorktree(ctx, runDir, dir, p.Force); err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	return nil, nil
}

// liveSessionsInWorkspace counts non-dead sessions whose (node-local) workspace
// id matches wsID.
func (d *Node) liveSessionsInWorkspace(wsID string) int {
	n := 0
	for _, s := range d.reg.Snapshot() {
		if s.WorkspaceID == wsID && s.Status != session.StatusDead {
			n++
		}
	}
	return n
}

// renderWorktreePath resolves the worktree path template against the project's
// main working tree. An absolute result is used as-is.
func renderWorktreePath(tmpl, repo, branch, mainDir string) (string, error) {
	if tmpl == "" {
		tmpl = ".worktrees/{{.Branch}}"
	}
	t, err := template.New("worktree").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, map[string]string{"Repo": repo, "Branch": branch}); err != nil {
		return "", err
	}
	rel := b.String()
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel), nil
	}
	return filepath.Clean(filepath.Join(mainDir, rel)), nil
}

func (d *Node) handleWorkspaceReadFile(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkspaceFileParams](params)
	if err != nil {
		return nil, err
	}
	if p.Path == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "path is required"}
	}
	dir, err := d.workspaceDir(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	content, notShown, err := gitstatus.ReadFile(ctx, dir, p.Path)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	return api.ReadFileResult{Path: p.Path, Content: content, NotShown: notShown}, nil
}

func (d *Node) handleWorkspaceSetTarget(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkspaceSetTargetParams](params)
	if err != nil {
		return nil, err
	}
	if _, err := d.workspaceDir(ctx, p.WorkspaceID); err != nil {
		return nil, err
	}
	target := strings.TrimSpace(p.TargetBranch)
	if target != "" && !gittree.ValidBranchName(ctx, target) {
		return nil, invalid("invalid branch name: %q", target)
	}
	if err := d.projreg.SetWorkspaceTarget(ctx, p.WorkspaceID, target); err != nil {
		return nil, invalid("%s", err)
	}
	return nil, nil
}

// diffBase returns "" for the uncommitted view, or the merge base with the
// workspace's target branch for AgainstTarget.
func (d *Node) diffBase(ctx context.Context, wsID, dir, against string) (string, error) {
	if against != api.AgainstTarget {
		return "", nil
	}
	target, _, err := d.projreg.TargetBranch(ctx, wsID)
	if err != nil {
		return "", invalid("%s", err)
	}
	if target == "" {
		return "", invalid("workspace has no target branch")
	}
	base, err := gitstatus.TargetBase(ctx, dir, target)
	if err != nil {
		return "", invalid("%s", err)
	}
	return base, nil
}
