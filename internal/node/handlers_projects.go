package node

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gittree"
	"github.com/MunifTanjim/argus/internal/projectreg"
)

// handleProjectList is node-local: ids are not composited.
func (d *Node) handleProjectList(ctx context.Context, _ json.RawMessage) (any, error) {
	if d.projreg == nil {
		return api.ProjectListResult{Projects: []api.ProjectNode{}}, nil
	}
	projects, err := d.projreg.Snapshot(ctx)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	return api.ProjectListResult{Projects: toProjectNodes(projects)}, nil
}

func (d *Node) handleProjectRename(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.ProjectRenameParams](params)
	if err != nil {
		return nil, err
	}
	if d.projreg == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "project registry disabled"}
	}
	name := strings.TrimSpace(p.Name)
	if p.ProjectID == "" || name == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "project_id and name are required"}
	}
	if err := d.projreg.RenameProject(ctx, p.ProjectID, name); err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	return nil, nil
}

func (d *Node) handleProjectSetHidden(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.ProjectFlagParams](params)
	if err != nil {
		return nil, err
	}
	if d.projreg == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "project registry disabled"}
	}
	if err := d.projreg.SetProjectHidden(ctx, p.ProjectID, p.Value); err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	return nil, nil
}

func (d *Node) handleProjectSetPinned(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.ProjectFlagParams](params)
	if err != nil {
		return nil, err
	}
	if d.projreg == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "project registry disabled"}
	}
	if err := d.projreg.SetProjectPinned(ctx, p.ProjectID, p.Value); err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	return nil, nil
}

func toProjectNodes(ps []projectreg.Project) []api.ProjectNode {
	out := make([]api.ProjectNode, 0, len(ps))
	for _, p := range ps {
		wss := make([]api.WorkspaceNode, 0, len(p.Workspaces))
		for _, w := range p.Workspaces {
			wss = append(wss, api.WorkspaceNode{
				ID:           w.ID,
				Dir:          w.Dir,
				IsMain:       w.IsMain,
				IsGone:       w.IsGone,
				Branch:       w.Branch,
				Head:         w.Head,
				TargetBranch: w.TargetBranch,
				CreatedAt:    rfc3339(w.CreatedAt),
				LastSeenAt:   rfc3339(w.LastSeenAt),
			})
		}
		out = append(out, api.ProjectNode{
			ID:            p.ID,
			Name:          p.Name,
			Kind:          p.Kind,
			Dir:           p.Dir,
			Root:          p.Root,
			DefaultBranch: p.DefaultBranch,
			IsGone:        p.IsGone,
			Hidden:        p.Hidden,
			Pinned:        p.Pinned,
			CreatedAt:     rfc3339(p.CreatedAt),
			LastSeenAt:    rfc3339(p.LastSeenAt),
			Workspaces:    wss,
		})
	}
	return out
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// projectMainDir resolves a project id to the directory git and gh run in.
func (d *Node) projectMainDir(ctx context.Context, id string) (string, error) {
	if d.projreg == nil {
		return "", invalid("project registry disabled")
	}
	_, mainDir, ok, err := d.projreg.ProjectInfo(ctx, id)
	switch {
	case err != nil:
		return "", invalid("%s", err)
	case !ok:
		return "", invalid("unknown project: %s", id)
	case mainDir == "":
		return "", invalid("project has no main working tree")
	}
	return mainDir, nil
}

func (d *Node) handleProjectBranches(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.ProjectRef](params)
	if err != nil {
		return nil, err
	}
	dir, err := d.projectMainDir(ctx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	bs, err := gittree.Branches(ctx, dir)
	if err != nil {
		return nil, invalid("%s", err)
	}
	out := make([]api.BranchInfo, len(bs))
	for i, b := range bs {
		out[i] = api.BranchInfo{Name: b.Name, Remote: b.Remote, Local: b.Local, CheckedOut: b.CheckedOut}
	}
	return api.BranchesResult{Branches: out}, nil
}

func (d *Node) handleProjectPRs(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.ProjectRef](params)
	if err != nil {
		return nil, err
	}
	dir, err := d.projectMainDir(ctx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	prov, err := d.forgeFor(ctx, dir)
	if err != nil {
		return nil, invalid("%s", err)
	}
	prs, err := prov.ListPRs(ctx, dir)
	if err != nil {
		return nil, invalid("%s", err)
	}
	out := make([]api.PRInfo, len(prs))
	for i, pr := range prs {
		out[i] = api.PRInfo{Number: pr.Number, Title: pr.Title, Author: pr.Author, HeadBranch: pr.HeadBranch, BaseBranch: pr.BaseBranch, URL: pr.URL}
	}
	return api.PRsResult{PRs: out}, nil
}

func (d *Node) handleProjectIssues(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.ProjectRef](params)
	if err != nil {
		return nil, err
	}
	dir, err := d.projectMainDir(ctx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	prov, err := d.forgeFor(ctx, dir)
	if err != nil {
		return nil, invalid("%s", err)
	}
	is, err := prov.ListIssues(ctx, dir)
	if err != nil {
		return nil, invalid("%s", err)
	}
	out := make([]api.IssueInfo, len(is))
	for i, x := range is {
		out[i] = api.IssueInfo{Number: x.Number, Title: x.Title, Author: x.Author, URL: x.URL}
	}
	return api.IssuesResult{Issues: out}, nil
}
