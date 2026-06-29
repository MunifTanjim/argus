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

// nodes.list returns every connected node so a client can pick a spawn target
// without first having a session there.
func TestNodesListReturnsConnectedNodes(t *testing.T) {
	a := New(time.Second)
	a.AddSource(newFakeSource("home", "home-box"))
	a.AddSource(newFakeSource("dev", "dev-box"))

	srv := NewServer(a, nil, nil)
	dispatch := srv.clientSrv.DispatchFunc()
	res, err := dispatch(context.Background(), api.MethodNodesList, nil)
	if err != nil {
		t.Fatalf("nodes.list dispatch: %v", err)
	}
	raw, _ := json.Marshal(res)
	var nodes []api.NodeInfo
	if err := json.Unmarshal(raw, &nodes); err != nil {
		t.Fatalf("decode nodes: %v (%s)", err, raw)
	}
	got := map[string]string{}
	for _, n := range nodes {
		got[n.NodeID] = n.NodeLabel
	}
	if got["home"] != "home-box" || got["dev"] != "dev-box" {
		t.Fatalf("nodes = %+v, want home-box/dev-box", nodes)
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

// goneSender is a push.Sender that always reports its target gone, so push.test
// can be exercised against the prune-and-report path.
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
	// gateway with one source "d1"; client subscribes with sub_id "k".
	// when the source pushes transcript.delta{sub_id:"k"}, the client receives it.
	// a second non-subscribing client must NOT receive it.

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
