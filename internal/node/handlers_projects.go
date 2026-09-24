package node

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
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
				ID:         w.ID,
				Dir:        w.Dir,
				IsMain:     w.IsMain,
				IsGone:     w.IsGone,
				Branch:     w.Branch,
				Head:       w.Head,
				CreatedAt:  rfc3339(w.CreatedAt),
				LastSeenAt: rfc3339(w.LastSeenAt),
			})
		}
		out = append(out, api.ProjectNode{
			ID:         p.ID,
			Name:       p.Name,
			Kind:       p.Kind,
			Dir:        p.Dir,
			Root:       p.Root,
			IsGone:     p.IsGone,
			Hidden:     p.Hidden,
			Pinned:     p.Pinned,
			CreatedAt:  rfc3339(p.CreatedAt),
			LastSeenAt: rfc3339(p.LastSeenAt),
			Workspaces: wss,
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
