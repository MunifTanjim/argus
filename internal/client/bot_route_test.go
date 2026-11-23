package client

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestBotListFanoutComposites(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodBotList {
			return nil, nil, nil
		}
		b, _ := json.Marshal(api.BotListResult{Bots: []api.BotInfo{{ID: "a1", Name: "x", Status: "idle", Primary: true}}, Primary: "a1"})
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
	var res api.BotListResult
	if err := c.Call(api.MethodBotList, api.BotListParams{}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Bots) != 1 {
		t.Fatalf("bots = %d", len(res.Bots))
	}
	a := res.Bots[0]
	if a.ID != session.CompositeID("n1", "a1") || a.NodeID != "n1" || !a.Primary || res.Primary != "" {
		t.Errorf("bot %+v primary %q", a, res.Primary)
	}
}

func TestBotAddressedRoutesToNode(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var gotBot, gotThread string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodBotThreadSend:
			var p api.BotSendParams
			_ = json.Unmarshal(params, &p)
			gotBot, gotThread = p.BotID, p.ThreadID
			b, _ := json.Marshal(api.BotSendResult{ThreadID: "t1", SessionID: "s1"})
			return b, nil, nil
		case api.MethodBotThreadList:
			b, _ := json.Marshal(api.BotThreadsResult{Threads: []api.BotThreadInfo{{ID: "t1", BotID: "a1", SessionID: "s1"}}})
			return b, nil, nil
		case api.MethodBotThreadTimeline:
			var p api.BotTimelineParams
			_ = json.Unmarshal(params, &p)
			gotThread = p.ThreadID
			b, _ := json.Marshal(api.BotTimeline{ThreadID: "t1", Sessions: []api.BotTimelineSession{{SessionID: "s1"}}})
			return b, nil, nil
		case api.MethodBotCreate:
			b, _ := json.Marshal(api.BotInfo{ID: "a9", Name: "n"})
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
	var sr api.BotSendResult
	if err := c.Call(api.MethodBotThreadSend, api.BotSendParams{BotID: "n1:a1", ThreadID: "n1:t1", Text: "hi"}, &sr); err != nil {
		t.Fatal(err)
	}
	if gotBot != "a1" || gotThread != "t1" {
		t.Errorf("node got bot %q thread %q", gotBot, gotThread)
	}
	if sr.ThreadID != "n1:t1" || sr.SessionID != "n1:s1" {
		t.Errorf("send result %+v", sr)
	}
	var tr api.BotThreadsResult
	if err := c.Call(api.MethodBotThreadList, api.BotRef{BotID: "n1:a1"}, &tr); err != nil {
		t.Fatal(err)
	}
	if th := tr.Threads[0]; th.ID != "n1:t1" || th.BotID != "n1:a1" || th.SessionID != "n1:s1" {
		t.Errorf("thread %+v", th)
	}
	var tl api.BotTimeline
	if err := c.Call(api.MethodBotThreadTimeline, api.BotTimelineParams{ThreadID: "n1:t1"}, &tl); err != nil {
		t.Fatal(err)
	}
	if gotThread != "t1" || tl.ThreadID != "n1:t1" || tl.Sessions[0].SessionID != "n1:s1" {
		t.Errorf("timeline %+v node thread %q", tl, gotThread)
	}
	var info api.BotInfo
	if err := c.Call(api.MethodBotCreate, api.BotCreateParams{Name: "n"}, &info); err != nil {
		t.Fatal(err)
	}
	if info.ID != "n1:a9" || info.NodeID != "n1" {
		t.Errorf("create %+v", info)
	}
	if err := c.Call(api.MethodBotShow, api.BotRef{BotID: "bare"}, nil); err == nil {
		t.Error("unqualified bot id accepted")
	}
}

func TestBotMemoryRoutesByBotAndProposal(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var gotBot, gotProposal, gotImport string
	var gotUndo api.BotMemoryUndoParams
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodBotMemoryProposals:
			var p api.BotRef
			_ = json.Unmarshal(params, &p)
			gotBot = p.BotID
			b, _ := json.Marshal(api.BotMemoryProposalsResult{Proposals: []api.BotMemoryProposal{{ID: "mp1", BotID: "a1", ThreadID: "t1"}}})
			return b, nil, nil
		case api.MethodBotMemoryLog:
			b, _ := json.Marshal(api.BotMemoryLogResult{Commits: []api.BotMemoryCommit{{Hash: "abc", ThreadID: "t1"}, {Hash: "def"}}})
			return b, nil, nil
		case api.MethodBotMemoryImport:
			var p api.BotMemoryImportParams
			_ = json.Unmarshal(params, &p)
			gotImport = p.BotID
			b, _ := json.Marshal(api.BotMemoryImportResult{Staged: 1})
			return b, nil, nil
		case api.MethodBotMemoryUndo:
			_ = json.Unmarshal(params, &gotUndo)
			b, _ := json.Marshal(api.BotMemoryUndoResult{Commit: "rev"})
			return b, nil, nil
		case api.MethodBotMemoryDecide:
			var p api.BotMemoryDecideParams
			_ = json.Unmarshal(params, &p)
			gotProposal = p.ProposalID
			b, _ := json.Marshal(api.BotMemoryDecideResult{Applied: 1})
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
	var props api.BotMemoryProposalsResult
	if err := c.Call(api.MethodBotMemoryProposals, api.BotRef{BotID: "n1:a1"}, &props); err != nil {
		t.Fatal(err)
	}
	if pr := props.Proposals[0]; gotBot != "a1" || pr.ID != "n1:mp1" || pr.BotID != "n1:a1" || pr.ThreadID != "n1:t1" {
		t.Errorf("proposal %+v node bot %q", pr, gotBot)
	}
	var log api.BotMemoryLogResult
	if err := c.Call(api.MethodBotMemoryLog, api.BotMemoryLogParams{BotID: "n1:a1"}, &log); err != nil {
		t.Fatal(err)
	}
	if log.Commits[0].ThreadID != "n1:t1" || log.Commits[1].ThreadID != "" {
		t.Errorf("log %+v", log)
	}
	if err := c.Call(api.MethodBotMemoryImport, api.BotMemoryImportParams{BotID: "n1:a1", Content: "x"}, nil); err != nil || gotImport != "a1" {
		t.Errorf("import err %v node bot %q", err, gotImport)
	}
	var dr api.BotMemoryDecideResult
	if err := c.Call(api.MethodBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: props.Proposals[0].ID, Accept: true}, &dr); err != nil {
		t.Fatal(err)
	}
	if gotProposal != "mp1" || dr.Applied != 1 {
		t.Errorf("decide %+v node proposal %q", dr, gotProposal)
	}
	if err := c.Call(api.MethodBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: "mp1"}, nil); err == nil {
		t.Error("unqualified proposal id accepted")
	}
	if err := c.Call(api.MethodBotMemoryUndo, api.BotMemoryUndoParams{BotID: "n1:a1", Commit: "abc", ThreadID: "n1:t1"}, nil); err != nil {
		t.Fatal(err)
	}
	if gotUndo != (api.BotMemoryUndoParams{BotID: "a1", Commit: "abc", ThreadID: "t1"}) {
		t.Errorf("node undo params %+v", gotUndo)
	}
	if err := c.Call(api.MethodBotMemoryUndo, api.BotMemoryUndoParams{BotID: "n1:a1", Commit: "abc", ThreadID: "n2:t1"}, nil); err == nil {
		t.Error("undo from another node's thread accepted")
	}
}

func TestBotUnreachableWhenNodeLost(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodBotList {
			return nil, nil, nil
		}
		b, _ := json.Marshal(api.BotListResult{Bots: []api.BotInfo{{ID: "a1", Name: "x", Status: "working"}}})
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
	var res api.BotListResult
	if err := c.Call(api.MethodBotList, nil, &res); err != nil || res.Bots[0].Status != "working" {
		t.Fatalf("err %v res %+v", err, res)
	}
	c.offlineGrace = time.Millisecond
	c.loseNode("n1", false)
	time.Sleep(20 * time.Millisecond)
	res = api.BotListResult{}
	if err := c.Call(api.MethodBotList, nil, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Bots) != 1 || res.Bots[0].Status != "unreachable" || res.Bots[0].ID != "n1:a1" {
		t.Fatalf("offline list = %+v", res.Bots)
	}
	c.loseNode("n1", true)
	res = api.BotListResult{}
	if err := c.Call(api.MethodBotList, nil, &res); err != nil || len(res.Bots) != 0 {
		t.Fatalf("after gone: %v %+v", err, res.Bots)
	}
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

func TestBotListInFlightLossStaysUnreachable(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodBotList {
			return nil, nil, nil
		}
		b, _ := json.Marshal(api.BotListResult{Bots: []api.BotInfo{{ID: "a1", Status: "idle"}}})
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
	var res api.BotListResult
	if err := c.Call(api.MethodBotList, nil, &res); err != nil {
		t.Fatal(err)
	}
	c.botListHook = func(nodeID string) { c.loseNode(nodeID, false) }
	for i := 0; i < 2; i++ {
		res = api.BotListResult{}
		if err := c.Call(api.MethodBotList, nil, &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Bots) != 1 || res.Bots[0].Status != "unreachable" {
			t.Fatalf("list %d = %+v", i, res.Bots)
		}
		c.botListHook = nil
	}
}

func TestBotChangedStampedWithNodeID(t *testing.T) {
	f, conn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	f.handle = func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return json.RawMessage(`{}`), nil, &fakeNote{method: api.MethodBotChanged, params: json.RawMessage(`{}`)}
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
		if ev.Method != api.MethodBotChanged || string(ev.Params) != `{"node_id":"n1"}` {
			t.Fatalf("got %s %s", ev.Method, ev.Params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no bot.changed")
	}
}

func TestBotRoutineRoutesToNode(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var mu sync.Mutex
	got := map[string]api.BotRoutineRef{}
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		var p api.BotRoutineRef
		_ = json.Unmarshal(params, &p)
		mu.Lock()
		got[method] = p
		mu.Unlock()
		var res any
		switch method {
		case api.MethodBotRoutineList:
			res = api.BotRoutineListResult{Routines: []api.BotRoutineInfo{{BotID: "a1", Slug: "inbox", State: "active"}}}
		case api.MethodBotRoutinePause, api.MethodBotRoutineResume:
			res = api.BotRoutineInfo{BotID: "a1", Slug: p.Slug, State: "paused"}
		case api.MethodBotRoutineRuns:
			res = api.BotRoutineRunsResult{Runs: []api.BotRoutineRunInfo{{ID: "rr1", ThreadID: "t1"}}}
		case api.MethodBotRoutineRun:
			res = api.BotRoutineRunResult{RunID: "rr2"}
		case api.MethodBotRoutineHookSecret:
			res = api.BotRoutineHookSecretResult{Secret: "whs_x", Path: "/hooks/scout/inbox"}
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
	bot := session.CompositeID("n1", "a1")
	ref := api.BotRoutineRef{BotID: bot, Slug: "inbox"}

	var list api.BotRoutineListResult
	if err := c.Call(api.MethodBotRoutineList, api.BotRef{BotID: bot}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Routines) != 1 || list.Routines[0].BotID != bot {
		t.Fatalf("list: %+v", list)
	}
	var info api.BotRoutineInfo
	if err := c.Call(api.MethodBotRoutinePause, ref, &info); err != nil || info.BotID != bot {
		t.Fatalf("pause: %+v %v", info, err)
	}
	if err := c.Call(api.MethodBotRoutineResume, ref, &info); err != nil || info.BotID != bot {
		t.Fatalf("resume: %+v %v", info, err)
	}
	var runs api.BotRoutineRunsResult
	if err := c.Call(api.MethodBotRoutineRuns, api.BotRoutineRunsParams{BotID: bot, Slug: "inbox"}, &runs); err != nil || runs.Runs[0].ThreadID != session.CompositeID("n1", "t1") {
		t.Fatalf("runs: %+v %v", runs, err)
	}
	var run api.BotRoutineRunResult
	if err := c.Call(api.MethodBotRoutineRun, ref, &run); err != nil || run.RunID != "rr2" {
		t.Fatalf("run: %+v %v", run, err)
	}
	var sec api.BotRoutineHookSecretResult
	if err := c.Call(api.MethodBotRoutineHookSecret, ref, &sec); err != nil || sec.Secret != "whs_x" {
		t.Fatalf("hook secret: %+v %v", sec, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, m := range []string{api.MethodBotRoutineList, api.MethodBotRoutinePause, api.MethodBotRoutineRuns, api.MethodBotRoutineRun, api.MethodBotRoutineHookSecret} {
		if got[m].BotID != "a1" {
			t.Errorf("%s reached the node with bot_id %q", m, got[m].BotID)
		}
	}
	if got[api.MethodBotRoutineRun].Slug != "inbox" {
		t.Errorf("run slug: %+v", got[api.MethodBotRoutineRun])
	}
	if err := c.Call(api.MethodBotRoutineRun, api.BotRoutineRef{BotID: "a1", Slug: "inbox"}, &run); err == nil {
		t.Fatal("a bot id without a node routed")
	}
}

func TestBotDraftRoutesByThread(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var got []string
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method != api.MethodBotDraftSend && method != api.MethodBotDraftDiscard {
			return nil, nil, nil
		}
		var p api.BotDraftParams
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
	for _, m := range []string{api.MethodBotDraftSend, api.MethodBotDraftDiscard} {
		if err := c.Call(m, api.BotDraftParams{ThreadID: "n1:t1", EventID: 3}, nil); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if len(got) != 2 || got[0] != api.MethodBotDraftSend+" t1" || got[1] != api.MethodBotDraftDiscard+" t1" {
		t.Errorf("node got %v", got)
	}
}

func TestBotOfferRoutesByThread(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var got api.BotOfferParams
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodBotOfferStart:
			_ = json.Unmarshal(params, &got)
			b, _ := json.Marshal(api.BotSendResult{ThreadID: "t1", SessionID: "s1"})
			return b, nil, nil
		case api.MethodBotOfferDismiss:
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
	var r api.BotSendResult
	if err := c.Call(api.MethodBotOfferStart, api.BotOfferParams{ThreadID: session.CompositeID("n1", "t1"), OfferID: "of_1"}, &r); err != nil {
		t.Fatal(err)
	}
	if got.ThreadID != "t1" || got.OfferID != "of_1" || r.ThreadID != session.CompositeID("n1", "t1") || r.SessionID != session.CompositeID("n1", "s1") {
		t.Fatalf("start: params %+v result %+v", got, r)
	}
	if err := c.Call(api.MethodBotOfferDismiss, api.BotOfferParams{ThreadID: session.CompositeID("n1", "t1"), OfferID: "of_2"}, nil); err != nil || got.OfferID != "of_2" {
		t.Fatalf("dismiss: %+v %v", got, err)
	}
}

func TestBotUsageAndSetRouteToNode(t *testing.T) {
	f, clientConn := newFakeGatewayNode(t, "n1")
	defer f.peer.Close()
	var gotUsage api.BotUsageParams
	var gotSet api.BotSetParams
	f.handle = func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		switch method {
		case api.MethodBotUsage:
			_ = json.Unmarshal(params, &gotUsage)
			b, _ := json.Marshal(api.BotUsageResult{BotID: "a1", Total: api.BotUsageRow{Sessions: 2}})
			return b, nil, nil
		case api.MethodBotSet:
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
	bot := session.CompositeID("n1", "a1")
	var u api.BotUsageResult
	if err := c.Call(api.MethodBotUsage, api.BotUsageParams{BotID: bot}, &u); err != nil || gotUsage.BotID != "a1" || u.BotID != bot || u.Total.Sessions != 2 {
		t.Fatalf("usage: %+v %+v %v", gotUsage, u, err)
	}
	var s api.BotSetResult
	if err := c.Call(api.MethodBotSet, api.BotSetParams{BotID: bot, Key: "notify", Value: "all"}, &s); err != nil || gotSet.BotID != "a1" || gotSet.Key != "notify" || s.Commit != "c1" {
		t.Fatalf("set: %+v %+v %v", gotSet, s, err)
	}
}
