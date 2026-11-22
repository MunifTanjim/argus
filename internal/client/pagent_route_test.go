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

func TestPAgentRoutineRoutesToNode(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var mu sync.Mutex
	got := map[string]api.PAgentRoutineRef{}
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		var p api.PAgentRoutineRef
		_ = json.Unmarshal(params, &p)
		mu.Lock()
		got[method] = p
		mu.Unlock()
		var res any
		switch method {
		case api.MethodPAgentRoutineList:
			res = api.PAgentRoutineListResult{Routines: []api.PAgentRoutineInfo{{AgentID: "a1", Slug: "inbox", State: "active"}}}
		case api.MethodPAgentRoutinePause, api.MethodPAgentRoutineResume:
			res = api.PAgentRoutineInfo{AgentID: "a1", Slug: p.Slug, State: "paused"}
		case api.MethodPAgentRoutineRuns:
			res = api.PAgentRoutineRunsResult{Runs: []api.PAgentRoutineRunInfo{{ID: "rr1", ThreadID: "t1"}}}
		case api.MethodPAgentRoutineRun:
			res = api.PAgentRoutineRunResult{RunID: "rr2"}
		case api.MethodPAgentRoutineHookSecret:
			res = api.PAgentRoutineHookSecretResult{Secret: "whs_x", Path: "/hooks/scout/inbox"}
		default:
			return nil, nil, nil
		}
		b, _ := json.Marshal(res)
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
	agent := session.CompositeID("n1", "a1")
	ref := api.PAgentRoutineRef{AgentID: agent, Slug: "inbox"}

	var list api.PAgentRoutineListResult
	if err := c.Call(api.MethodPAgentRoutineList, api.PAgentRef{AgentID: agent}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Routines) != 1 || list.Routines[0].AgentID != agent {
		t.Fatalf("list: %+v", list)
	}
	var info api.PAgentRoutineInfo
	if err := c.Call(api.MethodPAgentRoutinePause, ref, &info); err != nil || info.AgentID != agent {
		t.Fatalf("pause: %+v %v", info, err)
	}
	if err := c.Call(api.MethodPAgentRoutineResume, ref, &info); err != nil || info.AgentID != agent {
		t.Fatalf("resume: %+v %v", info, err)
	}
	var runs api.PAgentRoutineRunsResult
	if err := c.Call(api.MethodPAgentRoutineRuns, api.PAgentRoutineRunsParams{AgentID: agent, Slug: "inbox"}, &runs); err != nil || runs.Runs[0].ThreadID != session.CompositeID("n1", "t1") {
		t.Fatalf("runs: %+v %v", runs, err)
	}
	var run api.PAgentRoutineRunResult
	if err := c.Call(api.MethodPAgentRoutineRun, ref, &run); err != nil || run.RunID != "rr2" {
		t.Fatalf("run: %+v %v", run, err)
	}
	var sec api.PAgentRoutineHookSecretResult
	if err := c.Call(api.MethodPAgentRoutineHookSecret, ref, &sec); err != nil || sec.Secret != "whs_x" {
		t.Fatalf("hook secret: %+v %v", sec, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, m := range []string{api.MethodPAgentRoutineList, api.MethodPAgentRoutinePause, api.MethodPAgentRoutineRuns, api.MethodPAgentRoutineRun, api.MethodPAgentRoutineHookSecret} {
		if got[m].AgentID != "a1" {
			t.Errorf("%s reached the node with agent_id %q", m, got[m].AgentID)
		}
	}
	if got[api.MethodPAgentRoutineRun].Slug != "inbox" {
		t.Errorf("run slug: %+v", got[api.MethodPAgentRoutineRun])
	}
	if err := c.Call(api.MethodPAgentRoutineRun, api.PAgentRoutineRef{AgentID: "a1", Slug: "inbox"}, &run); err == nil {
		t.Fatal("an agent id without a node routed")
	}
}

func TestPAgentDraftRoutesByThread(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var got []string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodPAgentDraftSend && method != api.MethodPAgentDraftDiscard {
			return nil, nil, nil
		}
		var p api.PAgentDraftParams
		_ = json.Unmarshal(params, &p)
		got = append(got, method+" "+p.ThreadID)
		return json.RawMessage(`{}`), nil, nil
	}
	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{api.MethodPAgentDraftSend, api.MethodPAgentDraftDiscard} {
		if err := c.Call(m, api.PAgentDraftParams{ThreadID: "n1:t1", EventID: 3}, nil); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if len(got) != 2 || got[0] != api.MethodPAgentDraftSend+" t1" || got[1] != api.MethodPAgentDraftDiscard+" t1" {
		t.Errorf("node got %v", got)
	}
}

func TestPAgentOfferRoutesByThread(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var got api.PAgentOfferParams
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodPAgentOfferStart:
			_ = json.Unmarshal(params, &got)
			b, _ := json.Marshal(api.PAgentSendResult{ThreadID: "t1", SessionID: "s1"})
			return b, nil, nil
		case api.MethodPAgentOfferDismiss:
			_ = json.Unmarshal(params, &got)
			return []byte(`{}`), nil, nil
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
	var r api.PAgentSendResult
	if err := c.Call(api.MethodPAgentOfferStart, api.PAgentOfferParams{ThreadID: session.CompositeID("n1", "t1"), OfferID: "of_1"}, &r); err != nil {
		t.Fatal(err)
	}
	if got.ThreadID != "t1" || got.OfferID != "of_1" || r.ThreadID != session.CompositeID("n1", "t1") || r.SessionID != session.CompositeID("n1", "s1") {
		t.Fatalf("start: params %+v result %+v", got, r)
	}
	if err := c.Call(api.MethodPAgentOfferDismiss, api.PAgentOfferParams{ThreadID: session.CompositeID("n1", "t1"), OfferID: "of_2"}, nil); err != nil || got.OfferID != "of_2" {
		t.Fatalf("dismiss: %+v %v", got, err)
	}
}

func TestPAgentUsageAndSetRouteToNode(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var gotUsage api.PAgentUsageParams
	var gotSet api.PAgentSetParams
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodPAgentUsage:
			_ = json.Unmarshal(params, &gotUsage)
			b, _ := json.Marshal(api.PAgentUsageResult{AgentID: "a1", Total: api.PAgentUsageRow{Sessions: 2}})
			return b, nil, nil
		case api.MethodPAgentSet:
			_ = json.Unmarshal(params, &gotSet)
			return []byte(`{"commit":"c1"}`), nil, nil
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
	agent := session.CompositeID("n1", "a1")
	var u api.PAgentUsageResult
	if err := c.Call(api.MethodPAgentUsage, api.PAgentUsageParams{AgentID: agent}, &u); err != nil || gotUsage.AgentID != "a1" || u.AgentID != agent || u.Total.Sessions != 2 {
		t.Fatalf("usage: %+v %+v %v", gotUsage, u, err)
	}
	var s api.PAgentSetResult
	if err := c.Call(api.MethodPAgentSet, api.PAgentSetParams{AgentID: agent, Key: "notify", Value: "all"}, &s); err != nil || gotSet.AgentID != "a1" || gotSet.Key != "notify" || s.Commit != "c1" {
		t.Fatalf("set: %+v %+v %v", gotSet, s, err)
	}
}
