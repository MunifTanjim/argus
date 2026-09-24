package client

import (
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestRouteByWorkspaceSplitsCompositeID(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()

	var gotWorkspaceID string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method == api.MethodWorkspaceListDir {
			var p api.WorkspaceFileParams
			_ = json.Unmarshal(params, &p)
			gotWorkspaceID = p.WorkspaceID
			b, _ := json.Marshal(api.ListDirResult{Entries: []api.DirEntry{{Name: "x", Path: "x"}}})
			return b, nil, nil
		}
		return nil, nil, nil
	}

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	var res api.ListDirResult
	if err := c.Call(api.MethodWorkspaceListDir, api.WorkspaceFileParams{WorkspaceID: "n1:w1"}, &res); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotWorkspaceID != "w1" {
		t.Errorf("node received workspace_id %q, want the node-local w1", gotWorkspaceID)
	}
	if len(res.Entries) != 1 {
		t.Errorf("expected 1 entry back, got %+v", res.Entries)
	}
}

func TestRouteByProjectSplitsCompositeID(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()

	var gotProjectID string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method == api.MethodProjectRename {
			var p api.ProjectRenameParams
			_ = json.Unmarshal(params, &p)
			gotProjectID = p.ProjectID
		}
		return nil, nil, nil
	}

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.Call(api.MethodProjectRename, api.ProjectRenameParams{ProjectID: "n1:p1", Name: "x"}, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotProjectID != "p1" {
		t.Errorf("node received project_id %q, want the node-local p1", gotProjectID)
	}
}

func TestWorkspaceCreateCompositesResultID(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()

	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method == api.MethodWorkspaceCreate {
			b, _ := json.Marshal(api.WorkspaceCreateResult{WorkspaceID: "w9", Dir: "/r/.worktrees/feat"})
			return b, nil, nil
		}
		return nil, nil, nil
	}

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	var res api.WorkspaceCreateResult
	if err := c.Call(api.MethodWorkspaceCreate, api.WorkspaceCreateParams{ProjectID: "n1:p1", Branch: "feat"}, &res); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.WorkspaceID != "n1:w9" {
		t.Errorf("workspace_id = %q, want the composite n1:w9", res.WorkspaceID)
	}
	if res.Dir != "/r/.worktrees/feat" {
		t.Errorf("dir = %q, want it passed through", res.Dir)
	}
}
