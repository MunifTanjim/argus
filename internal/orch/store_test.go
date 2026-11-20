package orch_test

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/session"
)

func newTestStore(t *testing.T) (*orch.Store, *sql.DB) {
	t.Helper()
	sqlDB, err := db.Open(t.TempDir() + "/argus.db")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	clock := time.Unix(1_700_000_000, 0)
	return orch.NewStore(sqlDB, func() time.Time {
		clock = clock.Add(time.Millisecond)
		return clock
	}), sqlDB
}

func queued(to string) api.Message {
	return api.Message{
		Kind:  api.MessageText,
		From:  api.Actor{Node: "n1", Key: api.ActorOwner},
		To:    api.Actor{Node: "n1", Key: to},
		Body:  "hi",
		State: api.MessageQueued,
	}
}

func TestInsertGetRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	s.SetNodeID("home")
	ctx := context.Background()
	in := queued("claude:a")
	in.Kind = api.MessageInteraction
	in.Child = &api.Actor{Node: "home", Key: "claude:a"}
	in.Interaction = &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"}
	in.FromLabel = "worker"
	got, err := s.Insert(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^home:m-[0-9a-f]{16}$`).MatchString(got.ID) {
		t.Fatalf("ID = %q", got.ID)
	}
	back, ok, err := s.Get(ctx, got.ID)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if back.Kind != in.Kind || back.From != in.From || back.To != in.To || back.Child == nil || *back.Child != *in.Child || back.State != in.State ||
		back.FromLabel != in.FromLabel {
		t.Fatalf("round trip mismatch: %+v", back)
	}
	if back.Interaction == nil || back.Interaction.Kind != session.InteractionPermission || back.Interaction.ToolName != "Bash" {
		t.Fatalf("interaction = %+v", back.Interaction)
	}
	if !back.CreatedAt.Equal(got.CreatedAt) || !back.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatalf("timestamps: %v/%v vs %v/%v", back.CreatedAt, back.UpdatedAt, got.CreatedAt, got.UpdatedAt)
	}
	if _, ok, _ := s.Get(ctx, "m-00000000"); ok {
		t.Fatal("Get of missing ID found a message")
	}
}

func TestQueuedListsAllOldestFirst(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	m1, _ := s.Insert(ctx, queued("claude:a"))
	m2, _ := s.Insert(ctx, queued("claude:a"))
	m3, _ := s.Insert(ctx, queued("claude:a"))
	s.Insert(ctx, queued("claude:b"))
	if err := s.SetState(ctx, m2.ID, api.MessageDelivered); err != nil {
		t.Fatal(err)
	}
	got, err := s.Queued(ctx, api.Actor{Node: "n1", Key: "claude:a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != m1.ID || got[1].ID != m3.ID {
		t.Fatalf("Queued = %+v", got)
	}
}

func TestQueuedRecipientsDistinct(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	s.Insert(ctx, queued("claude:a"))
	s.Insert(ctx, queued("claude:a"))
	s.Insert(ctx, queued("codex:b"))
	d := queued("x")
	d.State = api.MessageDelivered
	s.Insert(ctx, d)
	got, err := s.QueuedRecipients(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, k := range got {
		seen[k] = true
	}
	if len(got) != 2 || !seen["claude:a"] || !seen["codex:b"] {
		t.Fatalf("recipients = %v", got)
	}
}

func TestUnrepliedTaskClearsOnReply(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	task := queued("claude:c")
	task.Kind = api.MessageTask
	task.State = api.MessageDelivered
	m, _ := s.Insert(ctx, task)
	if got, ok, _ := s.UnrepliedTask(ctx, api.Actor{Node: "n1", Key: "claude:c"}); !ok || got.ID != m.ID {
		t.Fatalf("task not found: %v ok=%v", got.ID, ok)
	}
	if err := s.SetState(ctx, m.ID, api.MessageReplied); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.UnrepliedTask(ctx, api.Actor{Node: "n1", Key: "claude:c"}); ok {
		t.Fatal("replied task still found")
	}
}

func TestExpireDeletesOld(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for _, to := range []string{"claude:a", "claude:b"} {
		if _, err := s.Insert(ctx, queued(to)); err != nil {
			t.Fatal(err)
		}
	}
	fresh, err := s.Insert(ctx, queued("claude:c"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.Expire(ctx, fresh.CreatedAt)
	if err != nil || n != 2 {
		t.Fatalf("Expire = %d, %v", n, err)
	}
	list, err := s.List(ctx, 10)
	if err != nil || len(list) != 1 || list[0].ID != fresh.ID {
		t.Fatalf("list = %+v err=%v", list, err)
	}
}

func TestExpireLinksKeepsLiveAndFresh(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	parent := api.Actor{Node: "n", Key: "claude:p"}
	for _, child := range []string{"claude:old", "claude:live", "claude:fresh"} {
		if err := s.PutLink(ctx, api.Link{Child: api.Actor{Node: "n", Key: child}, Parent: parent}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Link(ctx, api.Actor{Node: "n", Key: "claude:old"}); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Unix(1_700_000_000, 0).Add(3 * time.Millisecond)
	n, err := s.ExpireLinks(ctx, cutoff, func(c api.Actor) bool { return c.Key == "claude:live" }, func(string) bool { return true })
	if err != nil || n != 1 {
		t.Fatalf("ExpireLinks = %d, %v", n, err)
	}
	if _, ok, _ := s.Link(ctx, api.Actor{Node: "n", Key: "claude:old"}); ok {
		t.Fatal("expired link still cached")
	}
	links, err := s.Links(ctx)
	if err != nil || len(links) != 2 || links[0].Child.Key != "claude:fresh" || links[1].Child.Key != "claude:live" {
		t.Fatalf("links = %+v err=%v", links, err)
	}
}

func TestExpireLinksKeepsLiveRemoteMirror(t *testing.T) {
	s, _ := newTestStore(t)
	s.SetNodeID("n")
	ctx := context.Background()
	parent := api.Actor{Node: "n", Key: "claude:p"}
	for _, l := range []api.Link{
		{Child: api.Actor{Node: "n", Key: "claude:local"}, Parent: parent},
		{Child: api.Actor{Node: "m", Key: "claude:running"}, Parent: parent},
		{Child: api.Actor{Node: "m", Key: "claude:ended"}, Parent: parent, EndedBy: "self"},
	} {
		if err := s.PutLink(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.ExpireLinks(ctx, time.Unix(1_800_000_000, 0), func(api.Actor) bool { return false }, func(string) bool { return true })
	if err != nil || n != 2 {
		t.Fatalf("ExpireLinks = %d, %v", n, err)
	}
	links, err := s.Links(ctx)
	if err != nil || len(links) != 1 || links[0].Child.Key != "claude:running" {
		t.Fatalf("links = %+v err=%v", links, err)
	}

	// m was not seen for the whole retention: its mirror goes too.
	n, err = s.ExpireLinks(ctx, time.Unix(1_800_000_000, 0), func(api.Actor) bool { return false }, func(string) bool { return false })
	if err != nil || n != 1 {
		t.Fatalf("ExpireLinks for an unseen node = %d, %v", n, err)
	}
}

func TestFailQueuedOnlyQueued(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	s.Insert(ctx, queued("claude:c"))
	d := queued("claude:c")
	d.State = api.MessageDelivered
	dm, _ := s.Insert(ctx, d)
	failed, err := s.FailQueued(ctx, api.Actor{Node: "n1", Key: "claude:c"})
	if err != nil || len(failed) != 1 || failed[0].State != api.MessageFailed {
		t.Fatalf("FailQueued = %+v, %v", failed, err)
	}
	got, _, _ := s.Get(ctx, dm.ID)
	if got.State != api.MessageDelivered {
		t.Fatalf("delivered state = %q", got.State)
	}
}

func TestBodyKeepsNewlines(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	in := queued("claude:a")
	in.Body = "a\n\nb"
	m, _ := s.Insert(ctx, in)
	got, _, _ := s.Get(ctx, m.ID)
	if got.Body != "a\n\nb" {
		t.Fatalf("body = %q", got.Body)
	}
}

func TestTransitionOnlyFromExpectedState(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	m, err := st.Insert(ctx, queued("a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(ctx, m.ID, api.MessageReplied); err != nil {
		t.Fatal(err)
	}
	ok, err := st.Transition(ctx, m.ID, api.MessageQueued, api.MessageDelivered)
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	got, _, _ := st.Get(ctx, m.ID)
	if got.State != api.MessageReplied {
		t.Fatalf("state = %s", got.State)
	}
	m2, _ := st.Insert(ctx, queued("a"))
	if ok, err := st.Transition(ctx, m2.ID, api.MessageQueued, api.MessageDelivered); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestClaimInteractionOnlyOpenInteraction(t *testing.T) {
	s, _ := newTestStore(t)
	actorP, actorQ := api.Actor{Node: "a", Key: "claude:p"}, api.Actor{Node: "a", Key: "claude:q"}
	ctx := context.Background()
	n := queued("claude:p")
	n.Kind = api.MessageInteraction
	notice, _ := s.Insert(ctx, n)
	text, _ := s.Insert(ctx, queued("claude:p"))

	if _, ok, err := s.ClaimInteraction(ctx, text.ID, actorP); err != nil || ok {
		t.Fatalf("claimed a text message: ok=%v err=%v", ok, err)
	}
	if _, err := s.Transition(ctx, notice.ID, api.MessageQueued, api.MessageDelivered); err != nil {
		t.Fatal(err)
	}
	if prev, ok, err := s.ClaimInteraction(ctx, notice.ID, actorP); err != nil || !ok || prev != api.MessageDelivered {
		t.Fatalf("claim: prev=%s ok=%v err=%v", prev, ok, err)
	}
	got, _, _ := s.Get(ctx, notice.ID)
	if got.State != api.MessageReplied || got.AnsweredBy == nil || *got.AnsweredBy != actorP {
		t.Fatalf("got %+v", got)
	}
	if _, ok, _ := s.ClaimInteraction(ctx, notice.ID, actorQ); ok {
		t.Fatal("claimed twice")
	}

	if ok, _ := s.ReleaseInteraction(ctx, notice.ID, actorQ, api.MessageQueued); ok {
		t.Fatal("released by another actor")
	}
	if ok, err := s.ReleaseInteraction(ctx, notice.ID, actorP, api.MessageDelivered); err != nil || !ok {
		t.Fatalf("release: ok=%v err=%v", ok, err)
	}
	got, _, _ = s.Get(ctx, notice.ID)
	if got.State != api.MessageDelivered || got.AnsweredBy != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestLinkCacheFollowsWrites(t *testing.T) {
	s, sqlDB := newTestStore(t)
	s.SetNodeID("n1")
	ctx := context.Background()
	if _, ok, err := s.Link(ctx, api.Actor{Node: "n1", Key: "claude:c"}); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	put := func(child, parent string) {
		t.Helper()
		if err := s.PutLink(ctx, api.Link{
			Child: api.Actor{Node: "n1", Key: child}, Parent: api.Actor{Node: "n1", Key: parent},
		}); err != nil {
			t.Fatal(err)
		}
	}
	put("claude:c", "claude:p")
	put("claude:w", "claude:c")
	if err := s.SetEndedBy(ctx, api.Actor{Node: "n1", Key: "claude:c"}, "parent"); err != nil {
		t.Fatal(err)
	}
	put("claude:w", "claude:q")

	fresh := orch.NewStore(sqlDB, time.Now)
	for _, key := range []string{"claude:c", "claude:w"} {
		got, gotOK, err := s.Link(ctx, api.Actor{Node: "n1", Key: key})
		if err != nil {
			t.Fatal(err)
		}
		want, wantOK, err := fresh.Link(ctx, api.Actor{Node: "n1", Key: key})
		if err != nil {
			t.Fatal(err)
		}
		if got != want || gotOK != wantOK {
			t.Errorf("%s: cached %+v/%v, stored %+v/%v", key, got, gotOK, want, wantOK)
		}
	}
	if l, _, _ := s.Link(ctx, api.Actor{Node: "n1", Key: "claude:c"}); l.EndedBy != "parent" || l.Parent.Key != "claude:p" {
		t.Fatalf("c = %+v", l)
	}
}

func TestInsertRequestIsUniquePerSender(t *testing.T) {
	s, _ := newTestStore(t)
	req := orch.Request{ID: "r1", Hash: "h1"}
	first, err := s.InsertRequest(t.Context(), queued("claude:b"), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertRequest(t.Context(), queued("claude:b"), req); err == nil {
		t.Fatal("a second message with the same sender and request_id was stored")
	}
	other := queued("claude:b")
	other.From.Key = "claude:a"
	if _, err := s.InsertRequest(t.Context(), other, req); err != nil {
		t.Fatalf("another sender with the same request_id: %v", err)
	}
	m, hash, ok, err := s.MessageByRequest(t.Context(), api.Actor{Node: "n1", Key: api.ActorOwner}, "r1")
	if err != nil || !ok || m.ID != first.ID || hash != "h1" {
		t.Fatalf("got %+v hash=%q ok=%v err=%v", m, hash, ok, err)
	}
	if _, _, ok, err := s.MessageByRequest(t.Context(), api.Actor{Node: "n1", Key: api.ActorOwner}, "r2"); err != nil || ok {
		t.Fatalf("unknown request: ok=%v err=%v", ok, err)
	}
}

func TestLinkSpawnedStoresLinkTaskAndRequest(t *testing.T) {
	s, _ := newTestStore(t)
	parent := api.Actor{Node: "n1", Key: "claude:p"}
	child := api.Actor{Node: "n1", Key: "codex:c"}
	task := api.Message{ID: "m-00000001", Kind: api.MessageTask, From: parent, To: child, Body: "x", State: api.MessageDelivered}
	l := api.Link{Child: child, Parent: parent, TaskID: task.ID}
	if err := s.LinkSpawned(t.Context(), l, task, orch.Request{ID: "r1", Hash: "h1"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.LinkByRequest(t.Context(), api.Actor{Node: "n1", Key: "claude:p"}, "r1")
	if err != nil || !ok || got.Link != l || got.Hash != "h1" {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := s.Get(t.Context(), task.ID); err != nil || !ok {
		t.Fatalf("task: ok=%v err=%v", ok, err)
	}
}

func TestLinkSpawnedRollsBackWhenTaskFails(t *testing.T) {
	s, _ := newTestStore(t)
	parent := api.Actor{Node: "n1", Key: "claude:p"}
	child := api.Actor{Node: "n1", Key: "codex:c"}
	task := api.Message{ID: "m-00000001", Kind: api.MessageTask, From: parent, To: child, Body: "x", State: api.MessageDelivered}
	if _, err := s.Insert(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Link(t.Context(), child); err != nil {
		t.Fatal(err)
	}
	l := api.Link{Child: child, Parent: parent, TaskID: task.ID}
	if err := s.LinkSpawned(t.Context(), l, task, orch.Request{ID: "r1", Hash: "h1"}); err == nil {
		t.Fatal("expected the duplicate task id to fail")
	}
	if _, ok, err := s.Link(t.Context(), child); err != nil || ok {
		t.Fatalf("link left behind: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.LinkByRequest(t.Context(), api.Actor{Node: "n1", Key: "claude:p"}, "r1"); err != nil || ok {
		t.Fatalf("request left behind: ok=%v err=%v", ok, err)
	}
}

func TestChildrenListsDirectChildrenByKey(t *testing.T) {
	s, _ := newTestStore(t)
	for _, l := range [][2]string{{"claude:c2", "claude:p"}, {"claude:c1", "claude:p"}, {"claude:g", "claude:c1"}} {
		if err := s.PutLink(t.Context(), api.Link{
			Child: api.Actor{Node: "n1", Key: l[0]}, Parent: api.Actor{Node: "n1", Key: l[1]},
		}); err != nil {
			t.Fatal(err)
		}
	}
	links, err := s.Children(t.Context(), api.Actor{Node: "n1", Key: "claude:p"})
	if err != nil || len(links) != 2 || links[0].Child.Key != "claude:c1" || links[1].Child.Key != "claude:c2" {
		t.Fatalf("links = %+v err=%v", links, err)
	}
}

func TestNewIDIsNodeQualified(t *testing.T) {
	id := orch.NewID("home")
	if !regexp.MustCompile(`^home:m-[0-9a-f]{16}$`).MatchString(id) {
		t.Fatalf("got %q", id)
	}
}

func TestInsertAssignsNodeQualifiedID(t *testing.T) {
	s, _ := newTestStore(t)
	s.SetNodeID("home")
	got, err := s.Insert(context.Background(), queued("claude:a"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.ID, "home:m-") {
		t.Fatalf("ID = %q", got.ID)
	}
}

func TestReceiveKeepsSender(t *testing.T) {
	s, _ := newTestStore(t)
	s.SetNodeID("home")
	ctx := context.Background()
	at := time.Unix(1_700_000_000, 0)
	in := api.Message{
		ID: "b:m-00000001", Kind: api.MessageText,
		From: api.Actor{Node: "b", Key: "opencode:x"}, FromLabel: "planner",
		To: api.Actor{Node: "home", Key: "claude:a"}, Body: "hi", State: api.MessageQueued, CreatedAt: at, UpdatedAt: at,
	}
	if added, err := s.Receive(ctx, in); err != nil || !added {
		t.Fatalf("Receive: added=%v err=%v", added, err)
	}
	back, ok, err := s.Get(ctx, in.ID)
	if err != nil || !ok || back.FromLabel != in.FromLabel {
		t.Fatalf("back = %+v ok=%v err=%v", back, ok, err)
	}
}

func TestReleaseInteractionNeedsSameActor(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	n := queued("claude:c")
	n.Kind = api.MessageInteraction
	msg, _ := s.Insert(ctx, n)
	onA, onB := api.Actor{Node: "a", Key: "codex:p"}, api.Actor{Node: "b", Key: "codex:p"}
	if _, ok, err := s.ClaimInteraction(ctx, msg.ID, onA); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if ok, err := s.ReleaseInteraction(ctx, msg.ID, onB, api.MessageQueued); err != nil || ok {
		t.Fatalf("released by the same key on another node: ok=%v err=%v", ok, err)
	}
	if got, _, _ := s.Get(ctx, msg.ID); got.State != api.MessageReplied {
		t.Fatalf("state %s, want replied", got.State)
	}
	if ok, err := s.ReleaseInteraction(ctx, msg.ID, onA, api.MessageQueued); err != nil || !ok {
		t.Fatalf("release: ok=%v err=%v", ok, err)
	}
	if got, _, _ := s.Get(ctx, msg.ID); got.State != api.MessageQueued || got.AnsweredBy != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestOpenInteractionsMatchesTheChildNode(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for _, node := range []string{"n1", "n2"} {
		m := queued("claude:p")
		m.Kind, m.Child = api.MessageInteraction, &api.Actor{Node: node, Key: "claude:c"}
		if _, err := s.Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.OpenInteractions(ctx, api.Actor{Node: "n1", Key: "claude:c"})
	if err != nil || len(got) != 1 || got[0].Child.Node != "n1" {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestUnrepliedTaskMatchesTheChildNode(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	m := queued("claude:c")
	m.Kind, m.State, m.To.Node = api.MessageTask, api.MessageDelivered, "n2"
	if _, err := s.Insert(ctx, m); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.UnrepliedTask(ctx, api.Actor{Node: "n1", Key: "claude:c"}); err != nil || ok {
		t.Fatalf("matched a task to another node's child: %+v err=%v", got, err)
	}
}

func TestLinksWithTheSameKeyOnTwoNodesCoexist(t *testing.T) {
	s, sqlDB := newTestStore(t)
	s.SetNodeID("n1")
	ctx := context.Background()
	local := api.Link{Child: api.Actor{Node: "n1", Key: "claude:c"}, Parent: api.Actor{Node: "n1", Key: "claude:p1"}}
	remote := api.Link{Child: api.Actor{Node: "n2", Key: "claude:c"}, Parent: api.Actor{Node: "n1", Key: "claude:p2"}}
	for _, l := range []api.Link{local, remote} {
		if err := s.PutLink(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	links, err := s.Links(ctx)
	if err != nil || len(links) != 2 {
		t.Fatalf("links = %+v err=%v, want both", links, err)
	}
	if l, ok, _ := s.Link(ctx, local.Child); !ok || l.Parent != local.Parent {
		t.Fatalf("local link = %+v ok=%v", l, ok)
	}
	if l, ok, _ := s.Link(ctx, remote.Child); !ok || l.Parent != remote.Parent {
		t.Fatalf("remote link = %+v ok=%v", l, ok)
	}
	if err := s.SetEndedBy(ctx, remote.Child, "self"); err != nil {
		t.Fatal(err)
	}
	if l, _, _ := s.Link(ctx, local.Child); l.EndedBy != "" {
		t.Fatalf("ending the remote child ended the local one: %+v", l)
	}
	if kids, _ := s.Children(ctx, local.Parent); len(kids) != 1 || kids[0].Child != local.Child {
		t.Fatalf("children of p1 = %+v", kids)
	}
	fresh := orch.NewStore(sqlDB, time.Now)
	fresh.SetNodeID("n1")
	if l, ok, _ := fresh.Link(ctx, remote.Child); !ok || l.EndedBy != "self" {
		t.Fatalf("reloaded remote link = %+v ok=%v", l, ok)
	}
}

func TestQueuedKeepsArrivalOrder(t *testing.T) {
	s, _ := newTestStore(t)
	s.SetNodeID("n1")
	ctx := context.Background()
	to := api.Actor{Node: "n1", Key: "claude:a"}
	base := time.Unix(1_700_000_000, 0)
	// The second node's clock runs behind: its later message has an earlier time.
	for i, m := range []api.Message{
		{ID: "n2:m-00000001", CreatedAt: base.Add(10 * time.Second)},
		{ID: "n3:m-00000002", CreatedAt: base},
	} {
		m.Kind, m.From, m.To, m.Body, m.State, m.UpdatedAt = api.MessageText, api.Actor{Node: "n2", Key: "claude:w"}, to, fmt.Sprint(i), api.MessageQueued, m.CreatedAt
		if _, err := s.Receive(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Queued(ctx, to)
	if err != nil || len(got) != 2 || got[0].ID != "n2:m-00000001" || got[1].ID != "n3:m-00000002" {
		t.Fatalf("queued = %+v err=%v, want arrival order", got, err)
	}
}

func TestQueuedRecipientsUsesTheToIndex(t *testing.T) {
	_, sqlDB := newTestStore(t)
	rows, err := sqlDB.Query(`EXPLAIN QUERY PLAN SELECT DISTINCT to_key FROM orch_message WHERE to_node = ? AND state = 'queued'`, "n1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "; ")
	if !strings.Contains(joined, "orch_message_idx_to (to_node=?") {
		t.Fatalf("plan = %s, want a search of orch_message_idx_to by to_node", joined)
	}
}
