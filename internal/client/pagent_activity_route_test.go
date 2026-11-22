package client

import (
	"encoding/json"
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
