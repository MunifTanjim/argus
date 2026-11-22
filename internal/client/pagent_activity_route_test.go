package client

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestPAgentActivityCompositesIDs(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var gotAgent string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodPAgentActivity {
			return nil, nil, nil
		}
		var p api.PAgentActivityParams
		_ = json.Unmarshal(params, &p)
		gotAgent = p.AgentID
		b, _ := json.Marshal(api.PAgentActivityResult{Activity: []api.PAgentActivity{{ThreadID: "t1", SessionID: "s1", Decision: "deny"}}})
		return b, nil, nil
	}
	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	var res api.PAgentActivityResult
	if err := c.Call(api.MethodPAgentActivity, api.PAgentActivityParams{AgentID: "n1:a1"}, &res); err != nil {
		t.Fatal(err)
	}
	if gotAgent != "a1" || len(res.Activity) != 1 || res.Activity[0].ThreadID != "n1:t1" || res.Activity[0].SessionID != "n1:s1" {
		t.Errorf("agent %q activity %+v", gotAgent, res.Activity)
	}
}

func TestPAgentSecretRoutesByAgent(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var got []string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodPAgentSecretSet, api.MethodPAgentSecretList, api.MethodPAgentSecretRm:
			var p struct {
				AgentID string `json:"agent_id"`
			}
			_ = json.Unmarshal(params, &p)
			got = append(got, method+" "+p.AgentID)
			if method == api.MethodPAgentSecretList {
				b, _ := json.Marshal(api.PAgentSecretListResult{Secrets: []api.PAgentSecretInfo{{Name: "GH_TOKEN"}}})
				return b, nil, nil
			}
			return json.RawMessage(`{}`), nil, nil
		}
		return nil, nil, nil
	}
	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(api.MethodPAgentSecretSet, api.PAgentSecretSetParams{AgentID: "n1:a1", Name: "GH_TOKEN", Value: "v"}, nil); err != nil {
		t.Fatal(err)
	}
	var list api.PAgentSecretListResult
	if err := c.Call(api.MethodPAgentSecretList, api.PAgentRef{AgentID: "n1:a1"}, &list); err != nil || len(list.Secrets) != 1 {
		t.Fatalf("list = %+v %v", list, err)
	}
	if err := c.Call(api.MethodPAgentSecretRm, api.PAgentSecretRef{AgentID: "n1:a1", Name: "GH_TOKEN"}, nil); err != nil {
		t.Fatal(err)
	}
	want := api.MethodPAgentSecretSet + " a1," + api.MethodPAgentSecretList + " a1," + api.MethodPAgentSecretRm + " a1"
	if strings.Join(got, ",") != want {
		t.Errorf("node got %v", got)
	}
}
