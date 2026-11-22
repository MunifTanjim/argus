package client

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
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

func TestPAgentMemoryRoutesByAgentAndProposal(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var gotAgent, gotProposal, gotImport string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodPAgentMemoryProposals:
			var p api.PAgentRef
			_ = json.Unmarshal(params, &p)
			gotAgent = p.AgentID
			b, _ := json.Marshal(api.PAgentMemoryProposalsResult{Proposals: []api.PAgentMemoryProposal{{ID: "mp1", AgentID: "a1", ThreadID: "t1"}}})
			return b, nil, nil
		case api.MethodPAgentMemoryLog:
			b, _ := json.Marshal(api.PAgentMemoryLogResult{Commits: []api.PAgentMemoryCommit{{Hash: "abc", ThreadID: "t1"}, {Hash: "def"}}})
			return b, nil, nil
		case api.MethodPAgentMemoryImport:
			var p api.PAgentMemoryImportParams
			_ = json.Unmarshal(params, &p)
			gotImport = p.AgentID
			b, _ := json.Marshal(api.PAgentMemoryImportResult{Staged: 1})
			return b, nil, nil
		case api.MethodPAgentMemoryDecide:
			var p api.PAgentMemoryDecideParams
			_ = json.Unmarshal(params, &p)
			gotProposal = p.ProposalID
			b, _ := json.Marshal(api.PAgentMemoryDecideResult{Applied: 1})
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
	var props api.PAgentMemoryProposalsResult
	if err := c.Call(api.MethodPAgentMemoryProposals, api.PAgentRef{AgentID: "n1:a1"}, &props); err != nil {
		t.Fatal(err)
	}
	if pr := props.Proposals[0]; gotAgent != "a1" || pr.ID != "n1:mp1" || pr.AgentID != "n1:a1" || pr.ThreadID != "n1:t1" {
		t.Errorf("proposal %+v node agent %q", pr, gotAgent)
	}
	var log api.PAgentMemoryLogResult
	if err := c.Call(api.MethodPAgentMemoryLog, api.PAgentMemoryLogParams{AgentID: "n1:a1"}, &log); err != nil {
		t.Fatal(err)
	}
	if log.Commits[0].ThreadID != "n1:t1" || log.Commits[1].ThreadID != "" {
		t.Errorf("log %+v", log)
	}
	if err := c.Call(api.MethodPAgentMemoryImport, api.PAgentMemoryImportParams{AgentID: "n1:a1", Content: "x"}, nil); err != nil || gotImport != "a1" {
		t.Errorf("import err %v node agent %q", err, gotImport)
	}
	var dr api.PAgentMemoryDecideResult
	if err := c.Call(api.MethodPAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: props.Proposals[0].ID, Accept: true}, &dr); err != nil {
		t.Fatal(err)
	}
	if gotProposal != "mp1" || dr.Applied != 1 {
		t.Errorf("decide %+v node proposal %q", dr, gotProposal)
	}
	if err := c.Call(api.MethodPAgentMemoryDecide, api.PAgentMemoryDecideParams{ProposalID: "mp1"}, nil); err == nil {
		t.Error("unqualified proposal id accepted")
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
	c.offlineGrace = time.Millisecond
	c.loseNode("n1", false)
	time.Sleep(20 * time.Millisecond)
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

func newPAgentNode(t *testing.T, id string, agents []api.PAgentInfo, primary string, calls *[]string, mu *sync.Mutex) *fakeNode {
	t.Helper()
	k, _ := e2e.GenerateKeyPair()
	return &fakeNode{id: id, key: k, handle: func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodPAgentList:
			b, _ := json.Marshal(api.PAgentListResult{Agents: agents, Primary: primary})
			return b, nil, nil
		case api.MethodPAgentSetPrimary:
			var p api.PAgentPrimaryParams
			_ = json.Unmarshal(params, &p)
			mu.Lock()
			*calls = append(*calls, id+"="+p.AgentID)
			mu.Unlock()
			return json.RawMessage(`null`), nil, nil
		}
		return json.RawMessage(`null`), nil, nil
	}}
}

func connectMulti(t *testing.T, nodes ...*fakeNode) *E2EClient {
	t.Helper()
	g, conn := newFakeMultiGateway(t, nodes...)
	t.Cleanup(func() { g.peer.Close() })
	c, err := NewE2EClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPAgentSetPrimaryUnsetsOtherNodes(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	a := newPAgentNode(t, "n1", nil, "", &calls, &mu)
	b := newPAgentNode(t, "n2", nil, "", &calls, &mu)
	c := connectMulti(t, a, b)

	if err := c.Call(api.MethodPAgentSetPrimary, api.PAgentPrimaryParams{AgentID: "n2:a1"}, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]string(nil), calls...)
	calls = nil
	mu.Unlock()
	if len(got) != 2 || got[0] != "n2=a1" || got[1] != "n1=" {
		t.Errorf("set calls = %v, want n2=a1 then n1=", got)
	}
	if err := c.Call(api.MethodPAgentSetPrimary, api.PAgentPrimaryParams{}, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0] == "n1=a1" {
		t.Errorf("unset calls = %v", calls)
	}
	for _, x := range calls {
		if x != "n1=" && x != "n2=" {
			t.Errorf("unset call %q not empty", x)
		}
	}
}

func TestPAgentListSinglePrimary(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	a := newPAgentNode(t, "n1", []api.PAgentInfo{{ID: "a1", Primary: true}}, "a1", &calls, &mu)
	b := newPAgentNode(t, "n2", []api.PAgentInfo{{ID: "b1", Primary: true}}, "b1", &calls, &mu)
	c := connectMulti(t, a, b)
	var res api.PAgentListResult
	if err := c.Call(api.MethodPAgentList, nil, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 2 || res.Primary != "n1:a1" {
		t.Fatalf("res %+v", res)
	}
	if !res.Agents[0].Primary || res.Agents[1].Primary {
		t.Errorf("flags %+v", res.Agents)
	}
}

func TestPAgentListInFlightLossStaysUnreachable(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodPAgentList {
			return nil, nil, nil
		}
		b, _ := json.Marshal(api.PAgentListResult{Agents: []api.PAgentInfo{{ID: "a1", Status: "idle"}}})
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
	if err := c.Call(api.MethodPAgentList, nil, &res); err != nil {
		t.Fatal(err)
	}
	c.pagentListHook = func(nodeID string) { c.loseNode(nodeID, false) }
	for i := 0; i < 2; i++ {
		res = api.PAgentListResult{}
		if err := c.Call(api.MethodPAgentList, nil, &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Agents) != 1 || res.Agents[0].Status != "unreachable" {
			t.Fatalf("list %d = %+v", i, res.Agents)
		}
		c.pagentListHook = nil
	}
}

func TestPAgentChangedStampedWithNodeID(t *testing.T) {
	f, conn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return json.RawMessage(`{}`), nil, &fakeNote{method: api.MethodPAgentChanged, params: json.RawMessage(`{}`)}
	}
	c, _ := NewE2EClient(conn)
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.callNode("n1", "sessions.refresh", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-c.Events():
		if ev.Method != api.MethodPAgentChanged || string(ev.Params) != `{"node_id":"n1"}` {
			t.Fatalf("got %s %s", ev.Method, ev.Params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no pagent.changed")
	}
}
