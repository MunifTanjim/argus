package mesh_test

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/mesh"
	"github.com/MunifTanjim/argus/internal/session"
)

func newTestStore(t *testing.T) (*mesh.Store, *sql.DB) {
	t.Helper()
	sqlDB, err := db.Open(t.TempDir() + "/argus.db")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	clock := time.Unix(1_700_000_000, 0)
	return mesh.NewStore(sqlDB, func() time.Time {
		clock = clock.Add(time.Millisecond)
		return clock
	}), sqlDB
}

func queued(to string) api.Message {
	return api.Message{
		Kind:  api.MessageText,
		From:  api.Party{Node: "n1", Key: api.PartyOwner},
		To:    api.Party{Node: "n1", Key: to},
		Body:  "hi",
		State: api.MessageQueued,
	}
}

func TestInsertGetRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	in := queued("claude:a")
	in.Kind = api.MessagePromptNotice
	in.Subject = "claude:a"
	in.Interaction = &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"}
	got, err := s.Insert(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^m-[0-9a-f]{8}$`).MatchString(got.ID) {
		t.Fatalf("ID = %q", got.ID)
	}
	back, ok, err := s.Get(ctx, got.ID)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if back.Kind != in.Kind || back.From != in.From || back.To != in.To || back.Subject != in.Subject || back.State != in.State {
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

func TestNextQueuedOldestFirst(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	m1, _ := s.Insert(ctx, queued("claude:a"))
	m2, _ := s.Insert(ctx, queued("claude:a"))
	got, ok, err := s.NextQueued(ctx, "claude:a")
	if err != nil || !ok || got.ID != m1.ID {
		t.Fatalf("first = %v ok=%v err=%v", got.ID, ok, err)
	}
	if err := s.SetState(ctx, m1.ID, api.MessageDelivered); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.NextQueued(ctx, "claude:a")
	if err != nil || !ok || got.ID != m2.ID {
		t.Fatalf("second = %v ok=%v err=%v", got.ID, ok, err)
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
	got, err := s.Queued(ctx, "claude:a")
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
	got, err := s.QueuedRecipients(ctx)
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

func TestRekeyMovesMessagesAndLinks(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	s.Insert(ctx, queued("argus:%3"))
	if err := s.PutLink(ctx, api.Link{
		Child:  api.Party{Node: "n1", Key: "argus:%3"},
		Parent: api.Party{Node: "n1", Key: "claude:p"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rekey(ctx, "argus:%3", "claude:c"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.NextQueued(ctx, "claude:c"); !ok {
		t.Fatal("message not moved")
	}
	if l, ok, _ := s.Link(ctx, "claude:c"); !ok || l.Parent.Key != "claude:p" {
		t.Fatalf("link not moved: %+v ok=%v", l, ok)
	}
	if _, ok, _ := s.Link(ctx, "argus:%3"); ok {
		t.Fatal("old link remains")
	}
}

func TestUnrepliedTaskClearsOnReply(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	task := queued("claude:c")
	task.Kind = api.MessageTask
	task.State = api.MessageDelivered
	m, _ := s.Insert(ctx, task)
	if got, ok, _ := s.UnrepliedTask(ctx, "claude:c"); !ok || got.ID != m.ID {
		t.Fatalf("task not found: %v ok=%v", got.ID, ok)
	}
	if err := s.SetState(ctx, m.ID, api.MessageReplied); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.UnrepliedTask(ctx, "claude:c"); ok {
		t.Fatal("replied task still found")
	}
}

func TestExpireDeletesOld(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	s.Insert(ctx, queued("claude:a"))
	s.Insert(ctx, queued("claude:b"))
	n, err := s.Expire(ctx, time.Now().Add(time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("Expire = %d, %v", n, err)
	}
	list, _ := s.List(ctx, 10)
	if len(list) != 0 {
		t.Fatalf("list = %d", len(list))
	}
}

func TestExpireLinksKeepsLiveAndFresh(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	parent := api.Party{Node: "n", Key: "claude:p"}
	for _, child := range []string{"claude:old", "claude:live", "claude:fresh"} {
		if err := s.PutLink(ctx, api.Link{Child: api.Party{Node: "n", Key: child}, Parent: parent}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Link(ctx, "claude:old"); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Unix(1_700_000_000, 0).Add(3 * time.Millisecond)
	n, err := s.ExpireLinks(ctx, cutoff, func(key string) bool { return key == "claude:live" })
	if err != nil || n != 1 {
		t.Fatalf("ExpireLinks = %d, %v", n, err)
	}
	if _, ok, _ := s.Link(ctx, "claude:old"); ok {
		t.Fatal("expired link still cached")
	}
	links, err := s.Links(ctx)
	if err != nil || len(links) != 2 || links[0].Child.Key != "claude:live" || links[1].Child.Key != "claude:fresh" {
		t.Fatalf("links = %+v err=%v", links, err)
	}
}

func TestFailQueuedOnlyQueued(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	s.Insert(ctx, queued("claude:c"))
	d := queued("claude:c")
	d.State = api.MessageDelivered
	dm, _ := s.Insert(ctx, d)
	n, err := s.FailQueued(ctx, "claude:c")
	if err != nil || n != 1 {
		t.Fatalf("FailQueued = %d, %v", n, err)
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

func TestClaimNoticeOnlyOpenPromptNotice(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	n := queued("claude:p")
	n.Kind = api.MessagePromptNotice
	notice, _ := s.Insert(ctx, n)
	text, _ := s.Insert(ctx, queued("claude:p"))

	if _, ok, err := s.ClaimNotice(ctx, text.ID, "claude:p"); err != nil || ok {
		t.Fatalf("claimed a text message: ok=%v err=%v", ok, err)
	}
	if _, err := s.Transition(ctx, notice.ID, api.MessageQueued, api.MessageDelivered); err != nil {
		t.Fatal(err)
	}
	if prev, ok, err := s.ClaimNotice(ctx, notice.ID, "claude:p"); err != nil || !ok || prev != api.MessageDelivered {
		t.Fatalf("claim: prev=%s ok=%v err=%v", prev, ok, err)
	}
	got, _, _ := s.Get(ctx, notice.ID)
	if got.State != api.MessageReplied || got.AnsweredBy != "claude:p" {
		t.Fatalf("got %+v", got)
	}
	if _, ok, _ := s.ClaimNotice(ctx, notice.ID, "claude:q"); ok {
		t.Fatal("claimed twice")
	}

	if ok, _ := s.ReleaseNotice(ctx, notice.ID, "claude:q", api.MessageQueued); ok {
		t.Fatal("released by another party")
	}
	if ok, err := s.ReleaseNotice(ctx, notice.ID, "claude:p", api.MessageDelivered); err != nil || !ok {
		t.Fatalf("release: ok=%v err=%v", ok, err)
	}
	got, _, _ = s.Get(ctx, notice.ID)
	if got.State != api.MessageDelivered || got.AnsweredBy != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestLinkCacheFollowsWrites(t *testing.T) {
	s, sqlDB := newTestStore(t)
	ctx := context.Background()
	if _, ok, err := s.Link(ctx, "claude:c"); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	put := func(child, parent string) {
		t.Helper()
		if err := s.PutLink(ctx, api.Link{
			Child: api.Party{Node: "n1", Key: child}, Parent: api.Party{Node: "n1", Key: parent},
		}); err != nil {
			t.Fatal(err)
		}
	}
	put("claude:c", "claude:p")
	put("claude:w", "claude:c")
	if err := s.Rekey(ctx, "claude:c", "claude:c2"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEndedBy(ctx, "claude:c2", "parent"); err != nil {
		t.Fatal(err)
	}
	put("claude:w", "claude:q")
	if err := s.Rekey(ctx, "claude:w", "claude:c2"); err == nil {
		t.Fatal("rekey onto an existing child succeeded")
	}

	fresh := mesh.NewStore(sqlDB, time.Now)
	for _, key := range []string{"claude:c", "claude:c2", "claude:w"} {
		got, gotOK, err := s.Link(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		want, wantOK, err := fresh.Link(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if got != want || gotOK != wantOK {
			t.Errorf("%s: cached %+v/%v, stored %+v/%v", key, got, gotOK, want, wantOK)
		}
	}
	if l, _, _ := s.Link(ctx, "claude:c2"); l.EndedBy != "parent" || l.Parent.Key != "claude:p" {
		t.Fatalf("c2 = %+v", l)
	}
}
