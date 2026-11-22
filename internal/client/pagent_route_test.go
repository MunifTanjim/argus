package client

import (
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestPAgentListFanoutComposites(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodPAgentList {
			return nil, nil, nil
		}
		b, _ := json.Marshal(api.PAgentListResult{Agents: []api.PAgentInfo{{ID: "a1", Name: "x", Status: "idle", Primary: true}}, Primary: "a1"})
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
	var res api.PAgentListResult
	if err := c.Call(api.MethodPAgentList, api.PAgentListParams{}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 {
		t.Fatalf("agents = %d", len(res.Agents))
	}
	a := res.Agents[0]
	if a.ID != session.CompositeID("n1", "a1") || a.NodeID != "n1" || res.Primary != a.ID {
		t.Errorf("agent %+v primary %q", a, res.Primary)
	}
}

func TestPAgentAddressedRoutesToNode(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var gotAgent, gotThread string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodPAgentThreadSend:
			var p api.PAgentSendParams
			_ = json.Unmarshal(params, &p)
			gotAgent, gotThread = p.AgentID, p.ThreadID
			b, _ := json.Marshal(api.PAgentSendResult{ThreadID: "t1", SessionID: "s1"})
			return b, nil, nil
		case api.MethodPAgentThreadList:
			b, _ := json.Marshal(api.PAgentThreadsResult{Threads: []api.PAgentThreadInfo{{ID: "t1", AgentID: "a1", SessionID: "s1"}}})
			return b, nil, nil
		case api.MethodPAgentThreadTimeline:
			var p api.PAgentTimelineParams
			_ = json.Unmarshal(params, &p)
			gotThread = p.ThreadID
			b, _ := json.Marshal(api.PAgentTimeline{ThreadID: "t1", Sessions: []api.PAgentTimelineSession{{SessionID: "s1"}}})
			return b, nil, nil
		case api.MethodPAgentCreate:
			b, _ := json.Marshal(api.PAgentInfo{ID: "a9", Name: "n"})
			return b, nil, nil
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
	var sr api.PAgentSendResult
	if err := c.Call(api.MethodPAgentThreadSend, api.PAgentSendParams{AgentID: "n1:a1", ThreadID: "n1:t1", Text: "hi"}, &sr); err != nil {
		t.Fatal(err)
	}
	if gotAgent != "a1" || gotThread != "t1" {
		t.Errorf("node got agent %q thread %q", gotAgent, gotThread)
	}
	if sr.ThreadID != "n1:t1" || sr.SessionID != "n1:s1" {
		t.Errorf("send result %+v", sr)
	}
	var tr api.PAgentThreadsResult
	if err := c.Call(api.MethodPAgentThreadList, api.PAgentRef{AgentID: "n1:a1"}, &tr); err != nil {
		t.Fatal(err)
	}
	if th := tr.Threads[0]; th.ID != "n1:t1" || th.AgentID != "n1:a1" || th.SessionID != "n1:s1" {
		t.Errorf("thread %+v", th)
	}
	var tl api.PAgentTimeline
	if err := c.Call(api.MethodPAgentThreadTimeline, api.PAgentTimelineParams{ThreadID: "n1:t1"}, &tl); err != nil {
		t.Fatal(err)
	}
	if gotThread != "t1" || tl.ThreadID != "n1:t1" || tl.Sessions[0].SessionID != "n1:s1" {
		t.Errorf("timeline %+v node thread %q", tl, gotThread)
	}
	var info api.PAgentInfo
	if err := c.Call(api.MethodPAgentCreate, api.PAgentCreateParams{Name: "n"}, &info); err != nil {
		t.Fatal(err)
	}
	if info.ID != "n1:a9" || info.NodeID != "n1" {
		t.Errorf("create %+v", info)
	}
	if err := c.Call(api.MethodPAgentShow, api.PAgentRef{AgentID: "bare"}, nil); err == nil {
		t.Error("unqualified agent id accepted")
	}
}

func TestPAgentUnreachableWhenNodeLost(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodPAgentList {
			return nil, nil, nil
		}
		b, _ := json.Marshal(api.PAgentListResult{Agents: []api.PAgentInfo{{ID: "a1", Name: "x", Status: "working"}}})
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
	var res api.PAgentListResult
	if err := c.Call(api.MethodPAgentList, nil, &res); err != nil || res.Agents[0].Status != "working" {
		t.Fatalf("err %v res %+v", err, res)
	}
	c.loseNode("n1", false)
	res = api.PAgentListResult{}
	if err := c.Call(api.MethodPAgentList, nil, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 || res.Agents[0].Status != "unreachable" || res.Agents[0].ID != "n1:a1" {
		t.Fatalf("offline list = %+v", res.Agents)
	}
	c.loseNode("n1", true)
	res = api.PAgentListResult{}
	if err := c.Call(api.MethodPAgentList, nil, &res); err != nil || len(res.Agents) != 0 {
		t.Fatalf("after gone: %v %+v", err, res.Agents)
	}
}
