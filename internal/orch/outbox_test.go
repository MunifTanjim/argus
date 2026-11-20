package orch_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/orch"
)

func TestOutboxOrderPerNode(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	for i, node := range []string{"b", "c", "b", "b"} {
		if err := s.Enqueue(ctx, node, "m", map[string]int{"n": i}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.Pending(ctx, "b", 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, it := range items {
		if it.Node != "b" || it.Method != "m" {
			t.Fatalf("item = %+v", it)
		}
		var p struct{ N int }
		if err := json.Unmarshal(it.Params, &p); err != nil {
			t.Fatal(err)
		}
		got = append(got, p.N)
	}
	if !slices.Equal(got, []int{0, 2, 3}) {
		t.Fatalf("order = %v", got)
	}
	if !(items[0].Seq < items[1].Seq && items[1].Seq < items[2].Seq) {
		t.Fatalf("seqs = %+v", items)
	}
	if two, err := s.Pending(ctx, "b", 2); err != nil || len(two) != 2 || two[0].Seq != items[0].Seq {
		t.Fatalf("limit: %+v err=%v", two, err)
	}
	nodes, err := s.PendingNodes(ctx)
	if err != nil || !slices.Equal(nodes, []string{"b", "c"}) {
		t.Fatalf("nodes = %v err=%v", nodes, err)
	}
}

func TestOutboxAckRemoves(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	for i := range 2 {
		if err := s.Enqueue(ctx, "b", "m", i); err != nil {
			t.Fatal(err)
		}
	}
	items, _ := s.Pending(ctx, "b", 10)
	if err := s.Ack(ctx, items[0].Seq); err != nil {
		t.Fatal(err)
	}
	left, err := s.Pending(ctx, "b", 10)
	if err != nil || len(left) != 1 || left[0].Seq != items[1].Seq {
		t.Fatalf("left = %+v err=%v", left, err)
	}
	if err := s.Ack(ctx, items[1].Seq); err != nil {
		t.Fatal(err)
	}
	if nodes, err := s.PendingNodes(ctx); err != nil || len(nodes) != 0 {
		t.Fatalf("nodes = %v err=%v", nodes, err)
	}
}

func TestOutboxEnqueueWakes(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Enqueue(t.Context(), "b", "m", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Enqueued():
	default:
		t.Fatal("Enqueue did not signal")
	}
}

func TestOutboxExpire(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	if err := s.Enqueue(ctx, "b", "old", nil); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Unix(1_700_000_000, 0).Add(time.Second)
	n, err := s.ExpireOutbox(ctx, cutoff)
	if err != nil || n != 1 {
		t.Fatalf("expired %d err=%v", n, err)
	}
}

func TestReceiveKeepsIDAndTimestamps(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	at := time.Unix(1_600_000_000, 0)
	m := api.Message{
		ID: "b:m-00000001", Kind: api.MessageReply,
		From: api.Actor{Node: "b", Key: "claude:w"}, To: api.Actor{Node: "a", Key: "claude:p"},
		Body: "done", State: api.MessageQueued, CreatedAt: at, UpdatedAt: at.Add(time.Second),
	}
	ok, err := s.Receive(ctx, m)
	if err != nil || !ok {
		t.Fatalf("receive: ok=%v err=%v", ok, err)
	}
	if ok, err := s.Receive(ctx, m); err != nil || ok {
		t.Fatalf("second receive: ok=%v err=%v", ok, err)
	}
	got, _, err := s.Get(ctx, m.ID)
	if err != nil || !got.CreatedAt.Equal(at) || !got.UpdatedAt.Equal(m.UpdatedAt) || got.From != m.From {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestRequestIndexesAreScopedToNode(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	req := orch.Request{ID: "r1", Hash: "h1"}
	m := queued("claude:b")
	m.From = api.Actor{Node: "n1", Key: "claude:a"}
	first, err := s.InsertRequest(ctx, m, req)
	if err != nil {
		t.Fatal(err)
	}
	m.From.Node = "n2"
	second, err := s.InsertRequest(ctx, m, req)
	if err != nil {
		t.Fatalf("another node's actor with the same key and request_id: %v", err)
	}
	got, _, ok, err := s.MessageByRequest(ctx, api.Actor{Node: "n2", Key: "claude:a"}, "r1")
	if err != nil || !ok || got.ID != second.ID || got.ID == first.ID {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}

	child := api.Actor{Node: "n1", Key: "codex:c"}
	for _, node := range []string{"n1", "n2"} {
		child.Key = "codex:" + node
		l := api.Link{Child: child, Parent: api.Actor{Node: node, Key: "claude:p"}}
		if err := s.LinkSpawned(ctx, l, api.Message{}, req); err != nil {
			t.Fatalf("%s: %v", node, err)
		}
	}
	sl, ok, err := s.LinkByRequest(ctx, api.Actor{Node: "n2", Key: "claude:p"}, "r1")
	if err != nil || !ok || sl.Link.Child.Key != "codex:n2" {
		t.Fatalf("link %+v ok=%v err=%v", sl, ok, err)
	}
}

func pendingDelivers(t *testing.T, s *orch.Store, node string) []api.Message {
	t.Helper()
	items, err := s.Pending(t.Context(), node, 10)
	if err != nil {
		t.Fatal(err)
	}
	var out []api.Message
	for _, it := range items {
		var p api.OrchPeerDeliverParams
		if err := json.Unmarshal(it.Params, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p.Message)
	}
	return out
}

func TestDropDeliverMatchesPeerDeliver(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	m := api.Message{ID: "n1:m-00000001", Kind: api.MessageReply, From: api.Actor{Node: "n1", Key: "codex:c"}, To: api.Actor{Node: "n2", Key: "claude:p"}}
	if err := s.Enqueue(ctx, "n2", api.MethodOrchPeerDeliver, api.OrchPeerDeliverParams{Message: m}); err != nil {
		t.Fatal(err)
	}
	dropped, err := s.DropDeliver(ctx, "n2", m.ID)
	if err != nil || !dropped {
		t.Fatalf("dropped=%v err=%v", dropped, err)
	}
	if items, err := s.Pending(ctx, "n2", 10); err != nil || len(items) != 0 {
		t.Fatalf("pending = %+v err=%v", items, err)
	}
}

func TestFailCountsAndParks(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	if err := s.Enqueue(ctx, "b", "m1", nil); err != nil {
		t.Fatal(err)
	}
	items, _ := s.Pending(ctx, "b", 10)
	for i, want := range []bool{false, false, true} {
		parked, err := s.Fail(ctx, items[0].Seq, 3, 0)
		if err != nil || parked != want {
			t.Fatalf("fail %d: parked=%v err=%v, want %v", i+1, parked, err, want)
		}
	}
	if left, _ := s.Pending(ctx, "b", 10); len(left) != 0 {
		t.Fatalf("pending = %+v, want the parked item skipped", left)
	}
}

func TestFailCountsOncePerGap(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	if err := s.Enqueue(ctx, "b", "m1", nil); err != nil {
		t.Fatal(err)
	}
	items, _ := s.Pending(ctx, "b", 10)
	for range 12 {
		if parked, err := s.Fail(ctx, items[0].Seq, 3, time.Hour); err != nil || parked {
			t.Fatalf("parked=%v err=%v", parked, err)
		}
	}
	if left, _ := s.Pending(ctx, "b", 10); len(left) != 1 || left[0].Attempts != 1 {
		t.Fatalf("pending = %+v, want one counted attempt", left)
	}
}
