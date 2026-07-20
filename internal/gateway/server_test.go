package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestResumeRoutesByNodeID(t *testing.T) {
	a := New(time.Second)
	home := newFakeSource("home", "home-box", sess("default:%1"))
	home.callResp = json.RawMessage(`{"session_id":"default:%9"}`)
	a.AddSource(home)
	eventually(t, func() bool { return len(a.Snapshot()) == 1 })

	srv := NewServer(a, nil, nil)
	dispatch := srv.clientSrv.DispatchFunc()
	res, err := dispatch(context.Background(), api.MethodSessionResume,
		json.RawMessage(`{"node_id":"home","agent":"claude","agent_session_id":"x","cwd":"/tmp"}`))
	if err != nil {
		t.Fatalf("resume dispatch: %v", err)
	}
	raw, _ := json.Marshal(res)
	got, _ := sessionIDFromParams(raw)
	if got != "home:default:%9" {
		t.Fatalf("want composite session id, got %q (%s)", got, raw)
	}
}

func TestClientServerSpawnRoutesByNodeID(t *testing.T) {
	a := New(time.Second)
	home := newFakeSource("home", "home-box", sess("default:%1"))
	home.callResp = json.RawMessage(`{"session_id":"default:%9","pane_id":"%9"}`)
	a.AddSource(home)
	eventually(t, func() bool { return len(a.Snapshot()) == 1 })

	srv := NewServer(a, nil, nil)
	dispatch := srv.clientSrv.DispatchFunc()
	res, err := dispatch(context.Background(), api.MethodSessionSpawn,
		json.RawMessage(`{"node_id":"home","name":"x"}`))
	if err != nil {
		t.Fatalf("spawn dispatch: %v", err)
	}
	raw, _ := json.Marshal(res)
	got, _ := sessionIDFromParams(raw)
	if got != "home:default:%9" {
		t.Fatalf("want composite session id, got %q (%s)", got, raw)
	}
}

// TestRefreshFansOutToNodes verifies the gateway forwards sessions.refresh to
// every connected node (triggering each node's on-demand rescan) rather than
// only returning its own cached merged view.
func TestRefreshFansOutToNodes(t *testing.T) {
	a := New(time.Second)
	home := newFakeSource("home", "home-box", sess("s1"))
	dev := newFakeSource("dev", "dev-box", sess("s2"))
	a.AddSource(home)
	a.AddSource(dev)
	eventually(t, func() bool { return len(a.Snapshot()) == 2 })

	srv := NewServer(a, nil, nil)
	dispatch := srv.clientSrv.DispatchFunc()
	if _, err := dispatch(context.Background(), api.MethodSessionsRefresh, nil); err != nil {
		t.Fatalf("refresh dispatch: %v", err)
	}

	for _, src := range []*fakeSource{home, dev} {
		call, ok := src.lastCall()
		if !ok || call.method != api.MethodSessionsRefresh {
			t.Errorf("node %s did not receive sessions.refresh: ok=%v call=%+v", src.id, ok, call)
		}
	}
}

// server.info reports the version set via SetVersion plus every connected node,
// so a client can both show the version and pick a spawn target.
func TestServerInfoReportsVersionAndNodes(t *testing.T) {
	a := New(time.Second)
	a.AddSource(newFakeSource("home", "home-box"))
	a.AddSource(newFakeSource("dev", "dev-box"))
	srv := NewServer(a, nil, nil)
	srv.SetVersion("1.2.3")
	dispatch := srv.clientSrv.DispatchFunc()

	res, err := dispatch(context.Background(), api.MethodServerInfo, nil)
	if err != nil {
		t.Fatalf("server.info dispatch: %v", err)
	}
	raw, _ := json.Marshal(res)
	var info api.ServerInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("decode info: %v (%s)", err, raw)
	}
	if info.Version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", info.Version)
	}
	got := map[string]string{}
	for _, n := range info.Nodes {
		got[n.ID] = n.Label
	}
	if got["home"] != "home-box" || got["dev"] != "dev-box" {
		t.Fatalf("nodes = %+v, want home-box/dev-box", info.Nodes)
	}
}

// With node_id omitted and exactly one node connected, spawn defaults to it so a
// fresh single-node setup can create its first session.
func TestSpawnDefaultsToSoleNode(t *testing.T) {
	a := New(time.Second)
	home := newFakeSource("home", "home-box")
	home.callResp = json.RawMessage(`{"session_id":"default:%9","pane_id":"%9"}`)
	a.AddSource(home)

	srv := NewServer(a, nil, nil)
	dispatch := srv.clientSrv.DispatchFunc()
	res, err := dispatch(context.Background(), api.MethodSessionSpawn,
		json.RawMessage(`{"name":"x"}`))
	if err != nil {
		t.Fatalf("spawn dispatch: %v", err)
	}
	raw, _ := json.Marshal(res)
	got, _ := sessionIDFromParams(raw)
	if got != "home:default:%9" {
		t.Fatalf("want composite session id, got %q (%s)", got, raw)
	}
}

// With node_id omitted and more than one node connected, spawn is ambiguous and
// must be rejected rather than guessing.
func TestSpawnWithoutNodeIDAmbiguousFails(t *testing.T) {
	a := New(time.Second)
	a.AddSource(newFakeSource("home", "home-box"))
	a.AddSource(newFakeSource("dev", "dev-box"))

	srv := NewServer(a, nil, nil)
	dispatch := srv.clientSrv.DispatchFunc()
	if _, err := dispatch(context.Background(), api.MethodSessionSpawn,
		json.RawMessage(`{"name":"x"}`)); err == nil {
		t.Fatal("want error when node_id omitted with multiple nodes, got nil")
	}
}

func TestAgentsListRouting(t *testing.T) {
	a := New(time.Second)
	home := newFakeSource("home", "home-box")
	home.callResp = json.RawMessage(`{"agents":[{"id":"claude","name":"Claude","color":"#fe8019","spawnable":true},{"id":"codex","name":"Codex","color":"#b8bb26","spawnable":false}]}`)
	a.AddSource(home)

	srv := NewServer(a, nil, nil)
	dispatch := srv.clientSrv.DispatchFunc()

	// Omitted node_id with a single node → routed to it.
	res, err := dispatch(context.Background(), api.MethodAgentsList, nil)
	if err != nil {
		t.Fatalf("list dispatch: %v", err)
	}
	raw, _ := json.Marshal(res)
	var out api.AgentsListResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if len(out.Agents) != 2 || out.Agents[0].ID != "claude" || !out.Agents[0].Spawnable ||
		out.Agents[1].ID != "codex" || out.Agents[1].Spawnable {
		t.Fatalf("agents = %+v, want claude(spawnable) + codex(not)", out.Agents)
	}

	// A second node makes an omitted node_id ambiguous.
	a.AddSource(newFakeSource("dev", "dev-box"))
	if _, err := dispatch(context.Background(), api.MethodAgentsList, nil); err == nil {
		t.Fatal("want error when node_id omitted with multiple nodes, got nil")
	}
}

type goneSender struct{}

func (goneSender) Send(context.Context, push.Target, push.Notification) error {
	return fmt.Errorf("%w: 410 gone https://example/ep", push.ErrGone)
}

// TestPushTestReturnsGoneCode verifies push.test surfaces CodePushGone (not the
// generic internal error) when the target is gone, so the client knows to mint a
// fresh endpoint rather than re-register the dead one.
func TestPushTestReturnsGoneCode(t *testing.T) {
	a := New(time.Second)
	srv := NewServer(a, nil, nil)
	store := push.NewStore(t.TempDir())
	srv.SetPush(store, push.NewDispatcher(store, goneSender{}, nil))

	dispatch := srv.clientSrv.DispatchFunc()
	if _, err := dispatch(context.Background(), api.MethodPushRegister,
		json.RawMessage(`{"device_id":"d1","endpoint":"https://example/ep"}`)); err != nil {
		t.Fatalf("push.register: %v", err)
	}

	_, err := dispatch(context.Background(), api.MethodPushTest,
		json.RawMessage(`{"device_id":"d1"}`))
	rpcErr, ok := err.(*api.RPCError)
	if !ok {
		t.Fatalf("want *api.RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != api.CodePushGone {
		t.Fatalf("want CodePushGone (%d), got %d", api.CodePushGone, rpcErr.Code)
	}

	// The gone target must have been pruned, so a follow-up test finds no record.
	if _, _, gerr := store.Get("d1"); gerr != nil {
		t.Fatalf("store.Get: %v", gerr)
	}
	if recs, _ := store.List(); len(recs) != 0 {
		t.Fatalf("want target pruned, got %d records", len(recs))
	}
}

// fakeClientNotifier is a test double for api.Notifier that records Notify calls.
type fakeClientNotifier struct {
	ch chan api.Notification
}

func (f *fakeClientNotifier) Notify(method string, params any) error {
	b, _ := json.Marshal(params)
	select {
	case f.ch <- api.Notification{Method: method, Params: b}:
	default:
	}
	return nil
}

// TestSubTableHelpers verifies addSub/dropSub/clientForSub/subsForClient.
func TestSubTableHelpers(t *testing.T) {
	a := New(time.Second)
	srv := NewServer(a, nil, nil)

	c1 := &fakeClientNotifier{ch: make(chan api.Notification, 4)}
	c2 := &fakeClientNotifier{ch: make(chan api.Notification, 4)}

	srv.addSub("sub1", "d1", c1)
	srv.addSub("sub2", "d1", c1)
	srv.addSub("sub3", "d2", c2)

	if client, ok := srv.clientForSub("sub1"); !ok || client != c1 {
		t.Error("clientForSub(sub1) should return c1")
	}
	if client, ok := srv.clientForSub("sub3"); !ok || client != c2 {
		t.Error("clientForSub(sub3) should return c2")
	}
	if _, ok := srv.clientForSub("missing"); ok {
		t.Error("clientForSub(missing) should return false")
	}

	subs := srv.subsForClient(c1)
	if len(subs) != 2 {
		t.Fatalf("subsForClient(c1) = %d, want 2", len(subs))
	}

	srv.dropSub("sub1")
	if _, ok := srv.clientForSub("sub1"); ok {
		t.Error("dropSub(sub1): should not be found after drop")
	}
	subs = srv.subsForClient(c1)
	if len(subs) != 1 {
		t.Fatalf("subsForClient(c1) after drop = %d, want 1", len(subs))
	}
}

// TestTranscriptSubscribeHandlerRecordsAndRoutes verifies that the gateway's
// transcript.subscribe handler records the sub in the table and routes via agg.Route.
func TestTranscriptSubscribeHandlerRecordsAndRoutes(t *testing.T) {
	a := New(time.Second)
	src := newFakeSource("d1", "d1-box", sess("s1"))
	src.callResp = json.RawMessage(`{"sub_id":"k","from_index":0,"chunks":[]}`)
	a.AddSource(src)
	eventually(t, func() bool { return len(a.Snapshot()) == 1 })

	srv := NewServer(a, nil, nil)

	client := &fakeClientNotifier{ch: make(chan api.Notification, 4)}
	ctx := api.WithNotifier(context.Background(), client)

	compositeSessionID := session.CompositeID("d1", "s1")
	params, _ := json.Marshal(api.TranscriptSubscribeParams{
		SubID:     "k",
		SessionID: compositeSessionID,
	})
	dispatch := srv.clientSrv.DispatchFunc()
	_, err := dispatch(ctx, api.MethodTranscriptSubscribe, params)
	if err != nil {
		t.Fatalf("transcript.subscribe dispatch: %v", err)
	}

	// Verify the sub was recorded in the table.
	c, ok := srv.clientForSub("k")
	if !ok {
		t.Fatal("sub 'k' not in table after subscribe")
	}
	if c != client {
		t.Error("table entry has wrong client notifier")
	}

	// Verify the source was called with local session id.
	call, ok := src.lastCall()
	if !ok || call.method != api.MethodTranscriptSubscribe {
		t.Fatalf("source not called with transcript.subscribe: %+v", call)
	}
	localID, _ := sessionIDFromParams(call.params)
	if localID != "s1" {
		t.Errorf("node should receive local session id, got %q", localID)
	}
}

// TestTranscriptUnsubscribeHandlerRemovesAndRoutes verifies that the gateway's
// transcript.unsubscribe handler removes the sub from the table and routes to the node.
func TestTranscriptUnsubscribeHandlerRemovesAndRoutes(t *testing.T) {
	a := New(time.Second)
	src := newFakeSource("d1", "d1-box", sess("s1"))
	src.callResp = json.RawMessage(`null`)
	a.AddSource(src)
	eventually(t, func() bool { return len(a.Snapshot()) == 1 })

	srv := NewServer(a, nil, nil)

	client := &fakeClientNotifier{ch: make(chan api.Notification, 4)}
	srv.addSub("k", "d1", client)

	ctx := api.WithNotifier(context.Background(), client)
	params, _ := json.Marshal(api.TranscriptUnsubscribeParams{SubID: "k"})
	dispatch := srv.clientSrv.DispatchFunc()
	_, err := dispatch(ctx, api.MethodTranscriptUnsubscribe, params)
	if err != nil {
		t.Fatalf("transcript.unsubscribe dispatch: %v", err)
	}

	// Verify removed from table.
	if _, ok := srv.clientForSub("k"); ok {
		t.Error("sub 'k' should be removed after unsubscribe")
	}

	// Verify routed to node.
	call, ok := src.lastCall()
	if !ok || call.method != api.MethodTranscriptUnsubscribe {
		t.Fatalf("source not called with transcript.unsubscribe: %+v", call)
	}
}

// TestGatewayForwardsTranscriptDeltaToSubscriber verifies that the gateway's sub table routes
// a transcript.delta to the correct subscribed client and not to a bystander.
// The full end-to-end path through serveNode's OnNotify (including the orphaned
// unsubscribe branch) is covered by TestServeNodeOnNotifyDeltaBranch.
func TestGatewayForwardsTranscriptDeltaToSubscriber(t *testing.T) {
	a := New(time.Second)
	srv := NewServer(a, nil, nil)

	// c1 is the subscribing client, c2 is a bystander.
	c1 := &fakeClientNotifier{ch: make(chan api.Notification, 8)}
	c2 := &fakeClientNotifier{ch: make(chan api.Notification, 8)}

	srv.addSub("k", "d1", c1)

	// Simulate the serveNode OnNotify logic for transcript.delta (table hit path).
	delta := api.TranscriptDelta{SubID: "k", FromIndex: 0}
	if client, ok := srv.clientForSub(delta.SubID); ok {
		_ = client.Notify(api.MethodTranscriptDelta, delta)
	} else {
		t.Fatal("sub k not found in table")
	}

	select {
	case n := <-c1.ch:
		if n.Method != api.MethodTranscriptDelta {
			t.Errorf("c1 method = %q, want %q", n.Method, api.MethodTranscriptDelta)
		}
		var got api.TranscriptDelta
		if json.Unmarshal(n.Params, &got) != nil || got.SubID != "k" {
			t.Errorf("c1 delta sub_id = %q, want k", got.SubID)
		}
	case <-time.After(time.Second):
		t.Fatal("c1 did not receive delta")
	}

	// c2 must not have received anything.
	select {
	case n := <-c2.ch:
		t.Errorf("c2 should not receive delta, got method=%q", n.Method)
	default:
		// correct: bystander gets nothing
	}
}

// TestServeNodeOnNotifyDeltaBranch exercises the full serveNode OnNotify path
// for transcript.delta using a net.Pipe-backed node uplink.
func TestServeNodeOnNotifyDeltaBranch(t *testing.T) {
	a := New(time.Second)
	srv := NewServer(a, nil, nil)

	// c1 is the subscribing client.
	c1 := &fakeClientNotifier{ch: make(chan api.Notification, 8)}
	srv.addSub("sub1", "d1", c1)

	// Set up a net.Pipe: gatewayConn is passed to serveNode; nodeConn is what
	// "the node" sends on.
	gatewayConn, nodeConn := net.Pipe()
	defer gatewayConn.Close()

	// Track what flows back UP to the node from the gateway (orphaned unsubscribe).
	upstreamNotifications := make(chan api.Notification, 8)
	upstreamRequests := make(chan api.Notification, 8)

	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{
		OnNotify: func(n api.Notification) {
			upstreamNotifications <- n
		},
		Dispatch: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			upstreamRequests <- api.Notification{Method: method, Params: params}
			if method == api.MethodNodeIdentify {
				type identResult struct {
					ID    string `json:"id"`
					Label string `json:"label"`
				}
				return identResult{ID: "d1", Label: "d1-box"}, nil
			}
			return nil, nil
		},
	})
	defer nodePeer.Close()

	// serveNode runs in background; it will call node.identify and then block.
	go srv.serveNode(gatewayConn)

	// Wait for the identify call to be answered and source to be registered.
	eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.sources["d1"] != nil
	})

	// Now push a transcript.delta for "sub1" (table hit) from the node side.
	delta := api.TranscriptDelta{SubID: "sub1", FromIndex: 0}
	if err := nodePeer.Notify(api.MethodTranscriptDelta, delta); err != nil {
		t.Fatalf("node notify: %v", err)
	}

	// c1 must receive the delta.
	select {
	case n := <-c1.ch:
		if n.Method != api.MethodTranscriptDelta {
			t.Errorf("c1 method = %q, want %q", n.Method, api.MethodTranscriptDelta)
		}
		var got api.TranscriptDelta
		if err := json.Unmarshal(n.Params, &got); err != nil || got.SubID != "sub1" {
			t.Errorf("c1 delta sub_id = %q (err=%v), want sub1", got.SubID, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("c1 did not receive delta (table hit path)")
	}

	// Now push a transcript.delta for an orphaned sub (table miss).
	// The gateway should send transcript.unsubscribe back to the node as a Call.
	if err := nodePeer.Notify(api.MethodTranscriptDelta, api.TranscriptDelta{SubID: "orphan"}); err != nil {
		t.Fatalf("node notify (orphan): %v", err)
	}

	// Expect the gateway to call transcript.unsubscribe on the node peer.
	// Drain any earlier calls (e.g. node.identify) until we get the one we want.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case req := <-upstreamRequests:
			if req.Method == api.MethodTranscriptUnsubscribe {
				var p api.TranscriptUnsubscribeParams
				if err := json.Unmarshal(req.Params, &p); err != nil || p.SubID != "orphan" {
					t.Errorf("orphan unsubscribe sub_id = %q (err=%v), want orphan", p.SubID, err)
				}
				return // test passed
			}
			// Skip other methods (e.g. node.identify was already answered).
		case <-deadline:
			t.Fatal("gateway did not send transcript.unsubscribe for orphaned sub")
		}
	}
}

// TestServeNodeCompositesTasksChanged verifies a tasks.changed pushed by a remote
// node with a node-local session id reaches the client with a composite id, so the
// client (which only knows composite ids) can match it.
func TestServeNodeCompositesTasksChanged(t *testing.T) {
	a := New(time.Second)
	srv := NewServer(a, nil, nil)

	c1 := &fakeClientNotifier{ch: make(chan api.Notification, 8)}
	srv.addSub("sub1", "d1", c1)

	gatewayConn, nodeConn := net.Pipe()
	defer gatewayConn.Close()

	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == api.MethodNodeIdentify {
				return struct {
					ID    string `json:"id"`
					Label string `json:"label"`
				}{ID: "d1", Label: "d1-box"}, nil
			}
			return nil, nil
		},
	})
	defer nodePeer.Close()

	go srv.serveNode(gatewayConn)
	eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.sources["d1"] != nil
	})

	if err := nodePeer.Notify(api.MethodTasksChanged, api.TasksChanged{SubID: "sub1", SessionID: "abcd"}); err != nil {
		t.Fatalf("node notify: %v", err)
	}

	select {
	case n := <-c1.ch:
		if n.Method != api.MethodTasksChanged {
			t.Fatalf("c1 method = %q, want %q", n.Method, api.MethodTasksChanged)
		}
		var got api.TasksChanged
		if err := json.Unmarshal(n.Params, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.SessionID != "d1:abcd" {
			t.Fatalf("session id = %q, want d1:abcd", got.SessionID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("c1 did not receive tasks.changed")
	}
}

// TestTerminalInputRejectsNonOwner verifies terminal.input is routed only for the
// client that opened the term; a different client with the same term_id is rejected.
func TestTerminalInputRejectsNonOwner(t *testing.T) {
	a := New(time.Second)
	src := newFakeSource("n1", "n1-box", sess("s1"))
	src.callResp = json.RawMessage(`null`)
	a.AddSource(src)
	eventually(t, func() bool { return len(a.Snapshot()) == 1 })
	srv := NewServer(a, nil, nil)

	owner := &fakeClientNotifier{ch: make(chan api.Notification, 4)}
	intruder := &fakeClientNotifier{ch: make(chan api.Notification, 4)}
	srv.addTerm("t1", "n1", owner)

	dispatch := srv.clientSrv.DispatchFunc()
	params, _ := json.Marshal(api.TerminalInputParams{TermID: "t1", Data: "aGk="})

	// Intruder is rejected and the node must not be called.
	ctxIntruder := api.WithNotifier(context.Background(), intruder)
	if _, err := dispatch(ctxIntruder, api.MethodTerminalInput, params); err == nil {
		t.Fatal("want error for non-owner terminal.input, got nil")
	}
	if call, ok := src.lastCall(); ok && call.method == api.MethodTerminalInput {
		t.Fatal("node received terminal.input from non-owner")
	}

	// Owner is routed through to the node.
	ctxOwner := api.WithNotifier(context.Background(), owner)
	if _, err := dispatch(ctxOwner, api.MethodTerminalInput, params); err != nil {
		t.Fatalf("owner terminal.input: %v", err)
	}
	call, ok := src.lastCall()
	if !ok || call.method != api.MethodTerminalInput {
		t.Fatalf("node not called with terminal.input by owner: %+v", call)
	}
}

// TestTerminalCloseRejectsNonOwner verifies a non-owner cannot close another
// client's term: the request errors and the term stays in the table.
func TestTerminalCloseRejectsNonOwner(t *testing.T) {
	a := New(time.Second)
	src := newFakeSource("n1", "n1-box", sess("s1"))
	src.callResp = json.RawMessage(`null`)
	a.AddSource(src)
	eventually(t, func() bool { return len(a.Snapshot()) == 1 })
	srv := NewServer(a, nil, nil)

	owner := &fakeClientNotifier{ch: make(chan api.Notification, 4)}
	intruder := &fakeClientNotifier{ch: make(chan api.Notification, 4)}
	srv.addTerm("t1", "n1", owner)

	dispatch := srv.clientSrv.DispatchFunc()
	params, _ := json.Marshal(api.TerminalCloseParams{TermID: "t1"})

	ctxIntruder := api.WithNotifier(context.Background(), intruder)
	if _, err := dispatch(ctxIntruder, api.MethodTerminalClose, params); err == nil {
		t.Fatal("want error for non-owner terminal.close, got nil")
	}
	// The term must survive a rejected close.
	if _, ok := srv.clientForTerm("t1"); !ok {
		t.Fatal("term dropped by non-owner close")
	}

	// Owner can close it: term is dropped and routed to the node.
	ctxOwner := api.WithNotifier(context.Background(), owner)
	if _, err := dispatch(ctxOwner, api.MethodTerminalClose, params); err != nil {
		t.Fatalf("owner terminal.close: %v", err)
	}
	if _, ok := srv.clientForTerm("t1"); ok {
		t.Fatal("term not dropped after owner close")
	}
}

// TestTerminalOpenRejectsDuplicateTermID verifies terminal.open refuses an already-
// open term_id, leaving the incumbent intact and never calling the node.
func TestTerminalOpenRejectsDuplicateTermID(t *testing.T) {
	a := New(time.Second)
	src := newFakeSource("d1", "d1-box", sess("s1"))
	src.callResp = json.RawMessage(`null`)
	a.AddSource(src)
	eventually(t, func() bool { return len(a.Snapshot()) == 1 })
	srv := NewServer(a, nil, nil)

	owner := &fakeClientNotifier{ch: make(chan api.Notification, 4)}
	intruder := &fakeClientNotifier{ch: make(chan api.Notification, 4)}
	srv.addTerm("t1", "d1", owner) // owner already holds t1

	params, _ := json.Marshal(api.TerminalOpenParams{TermID: "t1", SessionID: session.CompositeID("d1", "s1")})
	dispatch := srv.clientSrv.DispatchFunc()

	ctxIntruder := api.WithNotifier(context.Background(), intruder)
	if _, err := dispatch(ctxIntruder, api.MethodTerminalOpen, params); err == nil {
		t.Fatal("want error opening an in-use term_id, got nil")
	}
	if c, ok := srv.clientForTerm("t1"); !ok || c != owner {
		t.Fatal("existing term owner was overwritten by a duplicate open")
	}
	if call, ok := src.lastCall(); ok && call.method == api.MethodTerminalOpen {
		t.Fatal("node received terminal.open for a duplicate term_id")
	}
}

// TestOrphanTerminalOutputClosesOnce verifies the gateway asks the node to close an
// orphaned term only once, even under a burst of output frames for it.
func TestOrphanTerminalOutputClosesOnce(t *testing.T) {
	a := New(time.Second)
	srv := NewServer(a, nil, nil)

	gatewayConn, nodeConn := net.Pipe()
	defer gatewayConn.Close()

	closes := make(chan string, 16)
	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			switch method {
			case api.MethodNodeIdentify:
				return map[string]string{"id": "n1", "label": "n1-box"}, nil
			case api.MethodTerminalClose:
				var p api.TerminalCloseParams
				_ = json.Unmarshal(params, &p)
				closes <- p.TermID
			}
			return nil, nil
		},
	})
	defer nodePeer.Close()

	go srv.serveNode(gatewayConn)
	eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.sources["n1"] != nil
	})

	for i := 0; i < 3; i++ {
		if err := nodePeer.Notify(api.MethodTerminalOutput, api.TerminalOutput{TermID: "orphan", Data: ""}); err != nil {
			t.Fatalf("notify #%d: %v", i, err)
		}
	}

	got := 0
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case id := <-closes:
			if id == "orphan" {
				got++
			}
		case <-deadline:
			if got != 1 {
				t.Fatalf("orphan close count = %d, want exactly 1 (debounced)", got)
			}
			return
		}
	}
}

// nodeGotTermClose reports whether the source recorded a terminal.close for termID.
func nodeGotTermClose(src *fakeSource, termID string) bool {
	src.mu.Lock()
	defer src.mu.Unlock()
	for _, c := range src.calls {
		if c.method == api.MethodTerminalClose {
			var p api.TerminalCloseParams
			if json.Unmarshal(c.params, &p) == nil && p.TermID == termID {
				return true
			}
		}
	}
	return false
}

// TestClientDisconnectClosesTerminals verifies per-connection cleanup drops a
// client's open terminals and routes terminal.close to the node on disconnect.
func TestClientDisconnectClosesTerminals(t *testing.T) {
	a := New(time.Second)
	src := newFakeSource("n1", "n1-box", sess("s1"))
	src.callResp = json.RawMessage(`null`)
	a.AddSource(src)
	eventually(t, func() bool { return len(a.Snapshot()) == 1 })
	srv := NewServer(a, nil, nil)

	// Real client connection served by the gateway; the app drives the other end.
	gwConn, appConn := net.Pipe()
	go srv.clientSrv.ServeConnContext(context.Background(), gwConn)
	app := api.NewPeer(appConn, api.PeerOptions{})

	if err := app.Call(api.MethodTerminalOpen, api.TerminalOpenParams{
		TermID: "t1", SessionID: session.CompositeID("n1", "s1"), Cols: 80, Rows: 24,
	}, nil); err != nil {
		t.Fatalf("terminal.open: %v", err)
	}
	if _, ok := srv.clientForTerm("t1"); !ok {
		t.Fatal("term t1 not recorded after open")
	}

	// Client drops its connection.
	app.Close()

	// Cleanup must drop the term and tell the node to close it.
	eventually(t, func() bool {
		_, tracked := srv.clientForTerm("t1")
		return !tracked && nodeGotTermClose(src, "t1")
	})
	if _, ok := srv.clientForTerm("t1"); ok {
		t.Fatal("term t1 still tracked after client disconnect")
	}
	if !nodeGotTermClose(src, "t1") {
		t.Fatal("node did not receive terminal.close for the disconnected client's term")
	}
}

// TestTerminalOutputRoutedToClient exercises serveNode's terminal.output path: a
// table hit forwards to the client, an unknown term_id calls terminal.close back.
func TestTerminalOutputRoutedToClient(t *testing.T) {
	a := New(time.Second)
	srv := NewServer(a, nil, nil)

	// c1 is the client that opened terminal t1.
	c1 := &fakeClientNotifier{ch: make(chan api.Notification, 8)}
	srv.addTerm("t1", "n1", c1)

	// Set up a net.Pipe: gatewayConn is passed to serveNode; nodeConn is what
	// "the node" sends on.
	gatewayConn, nodeConn := net.Pipe()
	defer gatewayConn.Close()

	upstreamRequests := make(chan api.Notification, 8)

	nodePeer := api.NewPeer(nodeConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			upstreamRequests <- api.Notification{Method: method, Params: params}
			if method == api.MethodNodeIdentify {
				type identResult struct {
					ID    string `json:"id"`
					Label string `json:"label"`
				}
				return identResult{ID: "n1", Label: "n1-box"}, nil
			}
			return nil, nil
		},
	})
	defer nodePeer.Close()

	// serveNode runs in background; it calls node.identify then blocks.
	go srv.serveNode(gatewayConn)

	// Wait for the identify call to be answered and source to be registered.
	eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.sources["n1"] != nil
	})

	// Node emits terminal.output for t1 (table hit): gateway must forward to c1.
	if err := nodePeer.Notify(api.MethodTerminalOutput, api.TerminalOutput{TermID: "t1", Data: "aGk="}); err != nil {
		t.Fatalf("node notify: %v", err)
	}

	select {
	case n := <-c1.ch:
		if n.Method != api.MethodTerminalOutput {
			t.Errorf("c1 method = %q, want %q", n.Method, api.MethodTerminalOutput)
		}
		var got api.TerminalOutput
		if err := json.Unmarshal(n.Params, &got); err != nil || got.TermID != "t1" {
			t.Errorf("c1 terminal output term_id = %q (err=%v), want t1", got.TermID, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("c1 did not receive terminal.output (table hit path)")
	}

	// Node emits terminal.output for an orphaned term (table miss).
	// The gateway should call terminal.close back to the node.
	if err := nodePeer.Notify(api.MethodTerminalOutput, api.TerminalOutput{TermID: "orphan", Data: ""}); err != nil {
		t.Fatalf("node notify (orphan): %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case req := <-upstreamRequests:
			if req.Method == api.MethodTerminalClose {
				var p api.TerminalCloseParams
				if err := json.Unmarshal(req.Params, &p); err != nil || p.TermID != "orphan" {
					t.Errorf("orphan close term_id = %q (err=%v), want orphan", p.TermID, err)
				}
				return
			}
			// Skip other calls (e.g. node.identify).
		case <-deadline:
			t.Fatal("gateway did not send terminal.close for orphaned term")
		}
	}
}
