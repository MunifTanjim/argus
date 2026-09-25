package node

import (
	"context"
	"encoding/json"
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
