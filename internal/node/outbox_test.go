package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

func pushNode(t *testing.T) *Node {
	t.Helper()
	d, _ := orchNode(t)
	d.SetIdentity("a", "")
	d.orch.SetNodeID("a")
	return d
}

func peerMessage(id, from string) api.Message {
	at := time.Unix(1_700_000_000, 0)
	return api.Message{
		ID: id, Kind: api.MessageReply,
		From: api.Actor{Node: from, Key: "claude:w"}, To: api.Actor{Node: "a", Key: "claude:p"},
		Body: "done", State: api.MessageQueued, CreatedAt: at, UpdatedAt: at,
	}
}

func pushTo(ctx context.Context, d *Node, method string, params any) error {
	raw, _ := json.Marshal(params)
	h := map[string]func(context.Context, json.RawMessage) (any, error){
		api.MethodOrchPeerDeliver:     d.handleOrchPeerDeliver,
		api.MethodOrchPeerWithdraw:    d.handleOrchPeerWithdraw,
		api.MethodOrchPeerLinkEnded:   d.handleOrchPeerLinkEnded,
		api.MethodOrchPeerParentEnded: d.handleOrchPeerParentEnded,
		api.MethodOrchPeerState:       d.handleOrchPeerState,
	}[method]
	_, err := h(ctx, raw)
	return err
}

func TestOutboxFlushOnPeerReturn(t *testing.T) {
	url := startPeerGateway(t)
	a, _ := startPeerNodeWith(t, url, "a", mustKeyPair(t), true, func(d *Node) { withOrchStore(t, d) })
	waitFor(t, func() bool { return a.peers.Load() != nil })
	a.outboxEvery = 50 * time.Millisecond
	at := time.Unix(1_700_000_000, 0)
	msg := api.Message{
		ID: "a:m-00000001", Kind: api.MessageText,
		From: api.Actor{Node: "a", Key: "opencode:p1"}, To: api.Actor{Node: "b", Key: "opencode:w1"},
		Body: "hi", State: api.MessageQueued, CreatedAt: at, UpdatedAt: at,
	}
	if err := a.orch.Enqueue(t.Context(), "b", api.MethodOrchPeerDeliver, api.OrchPeerDeliverParams{Message: msg}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.outboxLoop(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	b, _ := startPeerNodeWith(t, url, "b", mustKeyPair(t), true, func(d *Node) { withOrchStore(t, d) })
	waitFor(t, func() bool {
		_, ok, _ := b.orch.Get(t.Context(), msg.ID)
		return ok
	})
	got, _, _ := b.orch.Get(t.Context(), msg.ID)
	if got.State != api.MessageQueued || !got.CreatedAt.Equal(at) || got.From != msg.From {
		t.Fatalf("delivered = %+v", got)
	}
	waitFor(t, func() bool {
		nodes, err := a.orch.PendingNodes(t.Context())
		return err == nil && len(nodes) == 0
	})
}

func TestOutboxStopsAtFirstError(t *testing.T) {
	d := pushNode(t)
	fakeRosterEntry(t, d, "b")
	var sent []string
	fail := true
	d.outboxSend = func(_ context.Context, node, method string, _ json.RawMessage) error {
		sent = append(sent, node+"/"+method)
		if method == "m2" && fail {
			return errors.New("boom")
		}
		return nil
	}
	ctx := t.Context()
	for _, it := range []struct{ node, method string }{{"b", "m1"}, {"b", "m2"}, {"b", "m3"}, {"c", "m4"}} {
		if err := d.orch.Enqueue(ctx, it.node, it.method, nil); err != nil {
			t.Fatal(err)
		}
	}
	d.flushOutbox(ctx)
	if !slices.Equal(sent, []string{"b/m1", "b/m2"}) {
		t.Fatalf("sent = %v", sent)
	}
	left, _ := d.orch.Pending(ctx, "b", 10)
	if len(left) != 2 || left[0].Method != "m2" || left[1].Method != "m3" {
		t.Fatalf("left = %+v", left)
	}
	fail = false
	d.flushOutbox(ctx)
	if !slices.Equal(sent, []string{"b/m1", "b/m2", "b/m2", "b/m3"}) {
		t.Fatalf("sent = %v", sent)
	}
	if left, _ := d.orch.Pending(ctx, "b", 10); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}
	// c is offline: its item stays.
	if left, _ := d.orch.Pending(ctx, "c", 10); len(left) != 1 {
		t.Fatalf("c = %+v", left)
	}
}
func TestOutboxHungPeerDoesNotDelayOthers(t *testing.T) {
	d := pushNode(t)
	fakeRosterEntry(t, d, "b")
	fakeRosterEntry(t, d, "c")
	cSent := make(chan struct{})
	d.outboxSend = func(ctx context.Context, node, _ string, _ json.RawMessage) error {
		if node == "c" {
			close(cSent)
			return nil
		}
		select {
		case <-cSent:
			return nil
		case <-time.After(2 * time.Second):
			return errors.New("b was sent before c and held it back")
		}
	}
	ctx := t.Context()
	for _, node := range []string{"b", "c"} {
		if err := d.orch.Enqueue(ctx, node, "m", nil); err != nil {
			t.Fatal(err)
		}
	}
	d.flushOutbox(ctx)
	if nodes, err := d.orch.PendingNodes(ctx); err != nil || len(nodes) != 0 {
		t.Fatalf("pending nodes = %v err=%v", nodes, err)
	}
}

func TestOutboxDropsPermanentError(t *testing.T) {
	d := pushNode(t)
	fakeRosterEntry(t, d, "b")
	var sent []string
	d.outboxSend = func(_ context.Context, _, method string, _ json.RawMessage) error {
		sent = append(sent, method)
		if method == "m2" {
			return &api.RPCError{Code: api.CodePushRejected, Message: "remote caller not allowed on this connection"}
		}
		return nil
	}
	ctx := t.Context()
	for _, m := range []string{"m1", "m2", "m3"} {
		if err := d.orch.Enqueue(ctx, "b", m, nil); err != nil {
			t.Fatal(err)
		}
	}
	d.flushOutbox(ctx)
	if !slices.Equal(sent, []string{"m1", "m2", "m3"}) {
		t.Fatalf("sent = %v", sent)
	}
	if left, _ := d.orch.Pending(ctx, "b", 10); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}
}

func TestOutboxWrappedRPCErrorIsTransient(t *testing.T) {
	refusal := &api.RPCError{Code: api.CodePushRejected, Message: "rejected"}
	if rejectedByPeer(fmt.Errorf("node b: relay.open: %w", refusal)) {
		t.Fatal("a gateway error while opening the channel counted as the peer's rejection")
	}
	if !rejectedByPeer(refusal) {
		t.Fatal("a bare refusal reply was not counted as the peer's rejection")
	}
	if rejectedByPeer(&api.RPCError{Code: api.CodeInternalError, Message: "orch: receive: database is locked"}) {
		t.Fatal("an internal error on the receiver counted as rejection")
	}
	if rejectedByPeer(fmt.Errorf("node b is offline: %w", errPeerOffline)) {
		t.Fatal("offline counted as rejection")
	}
}

func TestOutboxKeepsItemsOnTransientReply(t *testing.T) {
	for _, code := range []int{api.CodeInternalError} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			d := pushNode(t)
			fakeRosterEntry(t, d, "b")
			var sent []string
			d.outboxSend = func(_ context.Context, _, method string, _ json.RawMessage) error {
				sent = append(sent, method)
				return &api.RPCError{Code: code, Message: "failed: " + method}
			}
			ctx := t.Context()
			for _, m := range []string{"m1", "m2"} {
				if err := d.orch.Enqueue(ctx, "b", m, nil); err != nil {
					t.Fatal(err)
				}
			}
			d.flushOutbox(ctx)
			if !slices.Equal(sent, []string{"m1"}) {
				t.Fatalf("sent = %v", sent)
			}
			if left, _ := d.orch.Pending(ctx, "b", 10); len(left) != 2 {
				t.Fatalf("left = %+v", left)
			}
		})
	}
}

func TestOutboxKeepsItemsForNodeWithoutCapability(t *testing.T) {
	d := pushNode(t)
	d.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{ID: "b", Online: true}})
	var sent []string
	d.outboxSend = func(_ context.Context, _, method string, _ json.RawMessage) error {
		sent = append(sent, method)
		return nil
	}
	ctx := t.Context()
	if err := d.orch.Enqueue(ctx, "b", "m1", nil); err != nil {
		t.Fatal(err)
	}
	d.flushOutbox(ctx)
	if len(sent) != 0 {
		t.Fatalf("sent = %v to a node without cross-node support", sent)
	}
	if left, _ := d.orch.Pending(ctx, "b", 10); len(left) != 1 {
		t.Fatalf("left = %+v", left)
	}
}

func TestDeliverIsIdempotent(t *testing.T) {
	d := pushNode(t)
	ctx := withPeerNode(t.Context(), "b")
	m := peerMessage("b:m-00000001", "b")
	m.State = api.MessageDelivered
	for range 2 {
		if err := pushTo(ctx, d, api.MethodOrchPeerDeliver, api.OrchPeerDeliverParams{Message: m}); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := d.orch.List(t.Context(), 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("msgs = %+v err=%v", msgs, err)
	}
	if msgs[0].State != api.MessageQueued || !msgs[0].CreatedAt.Equal(m.CreatedAt) {
		t.Fatalf("stored = %+v", msgs[0])
	}
}

func TestPeerOnlyMethodsRejectLocal(t *testing.T) {
	d := pushNode(t)
	for method, params := range map[string]any{
		api.MethodOrchPeerDeliver:     api.OrchPeerDeliverParams{Message: peerMessage("b:m-00000001", "b")},
		api.MethodOrchPeerWithdraw:    api.OrchPeerWithdrawParams{MessageID: "b:m-00000001"},
		api.MethodOrchPeerLinkEnded:   api.OrchPeerLinkEndedParams{ChildKey: "claude:c", EndedBy: "self"},
		api.MethodOrchPeerParentEnded: api.OrchPeerParentEndedParams{ParentKey: "claude:p"},
	} {
		if err := pushTo(t.Context(), d, method, params); !rejectedByPeer(err) || !strings.Contains(err.Error(), errRemoteCallerDenied.Error()) {
			t.Errorf("%s: err = %v", method, err)
		}
	}
}

func TestPeerPushRejectsOtherNodesData(t *testing.T) {
	d := pushNode(t)
	ctx := withPeerNode(t.Context(), "b")
	if err := pushTo(ctx, d, api.MethodOrchPeerDeliver, api.OrchPeerDeliverParams{Message: peerMessage("c:m-00000001", "c")}); !rejectedByPeer(err) {
		t.Error("deliver of c's message from b was accepted")
	}
	if _, err := d.orch.Receive(t.Context(), peerMessage("c:m-00000002", "c")); err != nil {
		t.Fatal(err)
	}
	if err := pushTo(ctx, d, api.MethodOrchPeerWithdraw, api.OrchPeerWithdrawParams{MessageID: "c:m-00000002"}); !rejectedByPeer(err) {
		t.Error("withdraw of c's message from b was accepted")
	}
	toC := peerMessage("b:m-00000003", "b")
	toC.To.Node = "c"
	if _, err := d.orch.Receive(t.Context(), toC); err != nil {
		t.Fatal(err)
	}
	if err := pushTo(ctx, d, api.MethodOrchPeerWithdraw, api.OrchPeerWithdrawParams{MessageID: toC.ID}); !rejectedByPeer(err) {
		t.Error("withdraw of a message for node c was accepted")
	}
	if err := d.orch.PutLink(t.Context(), api.Link{
		Child: api.Actor{Node: "c", Key: "claude:c"}, Parent: api.Actor{Node: "a", Key: "claude:p"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := pushTo(ctx, d, api.MethodOrchPeerLinkEnded, api.OrchPeerLinkEndedParams{ChildKey: "claude:c", EndedBy: "self"}); err != nil {
		t.Errorf("link_ended from b naming a key of c: %v", err)
	}
	if l, _, _ := d.orch.Link(t.Context(), api.Actor{Node: "c", Key: "claude:c"}); l.EndedBy != "" {
		t.Errorf("link_ended from b ended c's child: %+v", l)
	}
}

func TestPeerPushesApply(t *testing.T) {
	d := pushNode(t)
	ctx := withPeerNode(t.Context(), "b")
	notice := peerMessage("b:m-00000001", "b")
	notice.Kind, notice.From.Key = api.MessageInteraction, api.ActorArgus
	if _, err := d.orch.Receive(t.Context(), notice); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := pushTo(ctx, d, api.MethodOrchPeerWithdraw, api.OrchPeerWithdrawParams{MessageID: notice.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if st := msgState(t, d, notice.ID); st != api.MessageWithdrawn {
		t.Fatalf("notice state = %s", st)
	}
	if err := pushTo(ctx, d, api.MethodOrchPeerWithdraw, api.OrchPeerWithdrawParams{MessageID: "b:m-ffffffff"}); err != nil {
		t.Fatalf("withdraw unknown: %v", err)
	}

	if err := d.orch.PutLink(t.Context(), api.Link{
		Child: api.Actor{Node: "b", Key: "claude:w"}, Parent: api.Actor{Node: "a", Key: "claude:p"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := pushTo(ctx, d, api.MethodOrchPeerLinkEnded, api.OrchPeerLinkEndedParams{ChildKey: "claude:w", EndedBy: "self"}); err != nil {
		t.Fatal(err)
	}
	if l, _, _ := d.orch.Link(t.Context(), api.Actor{Node: "b", Key: "claude:w"}); l.EndedBy != "self" {
		t.Fatalf("link = %+v", l)
	}
	if err := pushTo(ctx, d, api.MethodOrchPeerLinkEnded, api.OrchPeerLinkEndedParams{ChildKey: "claude:zz", EndedBy: "self"}); err != nil {
		t.Fatalf("link_ended unknown: %v", err)
	}
}

func TestPeerDeliverRejectsChildOfOtherNode(t *testing.T) {
	d := pushNode(t)
	ctx := withPeerNode(t.Context(), "b")
	m := peerMessage("b:m-00000001", "b")
	m.Kind, m.From.Key = api.MessageInteraction, api.ActorArgus
	m.Child = &api.Actor{Node: "c", Key: "claude:c"}
	if err := pushTo(ctx, d, api.MethodOrchPeerDeliver, api.OrchPeerDeliverParams{Message: m}); !rejectedByPeer(err) {
		t.Fatalf("deliver about c's child from b: err = %v", err)
	}
	m.Child.Node = "b"
	if err := pushTo(ctx, d, api.MethodOrchPeerDeliver, api.OrchPeerDeliverParams{Message: m}); err != nil {
		t.Fatalf("deliver about b's child: %v", err)
	}
}

func TestMethodNotFoundIsRejected(t *testing.T) {
	d := pushNode(t)
	fakeRosterEntry(t, d, "b")
	var sent []string
	d.outboxSend = func(_ context.Context, _, method string, _ json.RawMessage) error {
		sent = append(sent, method)
		if method == "m1" {
			return &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found"}
		}
		return nil
	}
	ctx := t.Context()
	for _, m := range []string{"m1", "m2"} {
		if err := d.orch.Enqueue(ctx, "b", m, nil); err != nil {
			t.Fatal(err)
		}
	}
	d.flushOutbox(ctx)
	if !slices.Equal(sent, []string{"m1", "m2"}) {
		t.Fatalf("sent = %v", sent)
	}
	if left, _ := d.orch.Pending(ctx, "b", 10); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}
}

func TestRepeatedPeerErrorParksItem(t *testing.T) {
	d := pushNode(t)
	d.outboxEvery = 0
	fakeRosterEntry(t, d, "b")
	var sent []string
	d.outboxSend = func(_ context.Context, _, method string, _ json.RawMessage) error {
		sent = append(sent, method)
		if method == "m1" {
			return &api.RPCError{Code: api.CodeInternalError, Message: "orch: receive: constraint failed"}
		}
		return nil
	}
	ctx := t.Context()
	for _, m := range []string{"m1", "m2"} {
		if err := d.orch.Enqueue(ctx, "b", m, nil); err != nil {
			t.Fatal(err)
		}
	}
	for range 9 {
		d.flushOutbox(ctx)
	}
	if slices.Contains(sent, "m2") {
		t.Fatalf("m2 sent before m1 was parked: %v", sent)
	}
	d.flushOutbox(ctx)
	if n := strings.Count(strings.Join(sent, " "), "m1"); n != 10 || !slices.Contains(sent, "m2") {
		t.Fatalf("sent = %v, want m1 ten times, then m2", sent)
	}
	if left, _ := d.orch.Pending(ctx, "b", 10); len(left) != 0 {
		t.Fatalf("left = %+v, want m1 parked and m2 sent", left)
	}
}

func TestOfflineDoesNotCountAttempts(t *testing.T) {
	d := pushNode(t)
	fakeRosterEntry(t, d, "b")
	d.outboxSend = func(context.Context, string, string, json.RawMessage) error {
		return fmt.Errorf("node b: %w", errPeerOffline)
	}
	ctx := t.Context()
	if err := d.orch.Enqueue(ctx, "b", "m1", nil); err != nil {
		t.Fatal(err)
	}
	for range 12 {
		d.flushOutbox(ctx)
	}
	left, _ := d.orch.Pending(ctx, "b", 10)
	if len(left) != 1 || left[0].Attempts != 0 {
		t.Fatalf("left = %+v, want m1 with no attempts", left)
	}
}

func TestPushNeedsPeerVersion(t *testing.T) {
	d := pushNode(t)
	d.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{
		ID: "b", Online: true, Capabilities: api.NodeCapabilities{CrossNodeOrch: 1},
	}})
	ctx := t.Context()
	if err := d.orch.Enqueue(ctx, "b", api.MethodOrchPeerState, nil); err != nil {
		t.Fatal(err)
	}
	d.flushOutbox(ctx)
	if left, _ := d.orch.Pending(ctx, "b", 10); len(left) != 0 {
		t.Fatalf("left = %+v, want the push dropped for a version 1 peer", left)
	}
}

// ownMessage stores a message node a created for an actor on node to.
func ownMessage(t *testing.T, d *Node, to string, st api.MessageState) api.Message {
	t.Helper()
	m, err := d.orch.Insert(t.Context(), api.Message{
		Kind: api.MessageText, From: api.Actor{Node: "a", Key: "claude:p"}, To: api.Actor{Node: to, Key: "claude:c"},
		Body: "hi", State: st,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPeerStateForUnknownMessageIsIgnored(t *testing.T) {
	d := pushNode(t)
	ctx := withPeerNode(t.Context(), "b")
	if err := pushTo(ctx, d, api.MethodOrchPeerState, api.OrchPeerStateParams{MessageID: "a:m-0000beef", State: api.MessageDelivered}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if all, _ := d.orch.List(t.Context(), 10); len(all) != 0 {
		t.Fatalf("stored %+v", all)
	}
}

func TestPeerStateMovesOnlyForward(t *testing.T) {
	d := pushNode(t)
	ctx := withPeerNode(t.Context(), "b")
	m := ownMessage(t, d, "b", api.MessageQueued)
	for _, st := range []api.MessageState{api.MessageReplied, api.MessageDelivered, api.MessageFailed} {
		if err := pushTo(ctx, d, api.MethodOrchPeerState, api.OrchPeerStateParams{MessageID: m.ID, State: st}); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
	}
	if got := msgState(t, d, m.ID); got != api.MessageReplied {
		t.Fatalf("state = %s, want replied", got)
	}
	m2 := ownMessage(t, d, "b", api.MessageQueued)
	for _, st := range []api.MessageState{api.MessageDelivered, api.MessageFailed} {
		if err := pushTo(ctx, d, api.MethodOrchPeerState, api.OrchPeerStateParams{MessageID: m2.ID, State: st}); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
	}
	if got := msgState(t, d, m2.ID); got != api.MessageDelivered {
		t.Fatalf("state = %s, want delivered (failed applies only to a queued message)", got)
	}
	m3 := ownMessage(t, d, "b", api.MessageQueued)
	if err := pushTo(ctx, d, api.MethodOrchPeerState, api.OrchPeerStateParams{MessageID: m3.ID, State: api.MessageFailed}); err != nil {
		t.Fatal(err)
	}
	if got := msgState(t, d, m3.ID); got != api.MessageFailed {
		t.Fatalf("state = %s, want failed", got)
	}
}

func TestPeerStateOnlyFromRecipientNode(t *testing.T) {
	d := pushNode(t)
	m := ownMessage(t, d, "c", api.MessageQueued)
	err := pushTo(withPeerNode(t.Context(), "b"), d, api.MethodOrchPeerState, api.OrchPeerStateParams{MessageID: m.ID, State: api.MessageDelivered})
	if !rejectedByPeer(err) {
		t.Fatalf("err = %v, want a rejection", err)
	}
	if got := msgState(t, d, m.ID); got != api.MessageQueued {
		t.Fatalf("state = %s", got)
	}
}

func TestBurstOfPeerErrorsDoesNotPark(t *testing.T) {
	d := pushNode(t)
	d.outboxEvery = time.Hour
	fakeRosterEntry(t, d, "b")
	d.outboxSend = func(context.Context, string, string, json.RawMessage) error {
		return &api.RPCError{Code: api.CodeInternalError, Message: "database is locked"}
	}
	ctx := t.Context()
	if err := d.orch.Enqueue(ctx, "b", "m1", nil); err != nil {
		t.Fatal(err)
	}
	for range 15 {
		d.flushOutbox(ctx)
	}
	if left, _ := d.orch.Pending(ctx, "b", 10); len(left) != 1 {
		t.Fatalf("left = %+v, want m1 still pending", left)
	}
}

func TestOutboxDropsRetiredRekeyPushes(t *testing.T) {
	d := pushNode(t)
	fakeRosterEntry(t, d, "b")
	var sent []string
	d.outboxSend = func(_ context.Context, _, method string, _ json.RawMessage) error {
		sent = append(sent, method)
		return nil
	}
	ctx := t.Context()
	for _, m := range []string{"orch.peer.rekey", api.MethodOrchPeerLinkEnded} {
		if err := d.orch.Enqueue(ctx, "b", m, nil); err != nil {
			t.Fatal(err)
		}
	}
	d.flushOutbox(ctx)
	if !slices.Equal(sent, []string{api.MethodOrchPeerLinkEnded}) {
		t.Fatalf("sent = %v, want the rekey dropped", sent)
	}
	if left, _ := d.orch.Pending(ctx, "b", 10); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}
}
