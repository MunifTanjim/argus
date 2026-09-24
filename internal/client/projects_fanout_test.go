package client

import (
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestFanoutProjectsCompositesIDs(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodProjectList {
			return nil, nil, nil
		}
		b, _ := json.Marshal(api.ProjectListResult{Projects: []api.ProjectNode{{
			ID: "p1", Name: "argus", Kind: "git", Dir: "/repo/.git",
			Workspaces: []api.WorkspaceNode{{ID: "w1", Dir: "/repo", IsMain: true, Branch: "main"}},
		}}})
		return b, nil, nil
	}

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	var res api.ProjectListResult
	if err := c.Call(api.MethodProjectList, nil, &res); err != nil {
		t.Fatalf("Call project.list: %v", err)
	}
	if len(res.Projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(res.Projects))
	}
	p := res.Projects[0]
	if want := session.CompositeID("n1", "p1"); p.ID != want {
		t.Errorf("project ID = %q, want %q", p.ID, want)
	}
	if p.NodeID != "n1" {
		t.Errorf("project NodeID = %q, want n1", p.NodeID)
	}
	if want := session.CompositeID("n1", "w1"); p.Workspaces[0].ID != want {
		t.Errorf("workspace ID = %q, want %q", p.Workspaces[0].ID, want)
	}
}
