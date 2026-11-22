package pagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/db"
)

func newStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	sqlDB, err := db.Open(t.TempDir() + "/argus.db")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	clock := time.Unix(1_700_000_000, 0)
	return NewStore(sqlDB, func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}), sqlDB
}

func TestAgentNameUniqueAmongActive(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	if err := s.InsertAgent(ctx, Agent{ID: "a1", Name: "scout", Dir: "/d/1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertAgent(ctx, Agent{ID: "a2", Name: "scout", Dir: "/d/2"}); err == nil {
		t.Fatal("duplicate active name accepted")
	}
	if err := s.ArchiveAgent(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.AgentByName(ctx, "scout"); ok {
		t.Fatal("archived agent found by name")
	}
	if err := s.InsertAgent(ctx, Agent{ID: "a2", Name: "scout", Dir: "/d/2"}); err != nil {
		t.Fatalf("archived name not reusable: %v", err)
	}
	if err := s.RestoreAgent(ctx, "a1"); err == nil {
		t.Fatal("restore over an active name accepted")
	}
	if l, _ := s.ListAgents(ctx, false); len(l) != 1 || l[0].ID != "a2" {
		t.Fatalf("active list = %+v", l)
	}
	l, _ := s.ListAgents(ctx, true)
	if len(l) != 2 {
		t.Fatalf("full list = %+v", l)
	}
	a1, ok, err := s.AgentByID(ctx, "a1")
	if err != nil || !ok || a1.ArchivedAt.IsZero() {
		t.Fatalf("a1 = %+v %v %v", a1, ok, err)
	}
	if _, ok, _ := s.AgentByDir(ctx, "/d/1"); !ok {
		t.Fatal("AgentByDir missed archived agent")
	}
	if err := s.RenameAgent(ctx, "a2", "scout2"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.AgentByName(ctx, "scout2"); !ok {
		t.Fatal("renamed agent not found")
	}
}

func seedAgent(t *testing.T, s *Store) {
	t.Helper()
	if err := s.InsertAgent(context.Background(), Agent{ID: "a1", Name: "scout", Dir: "/d/1"}); err != nil {
		t.Fatal(err)
	}
}

func TestThreadLatestAndTouch(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	seedAgent(t, s)
	for _, id := range []string{"t1", "t2"} {
		if err := s.InsertThread(ctx, Thread{ID: id, AgentID: "a1", Kind: "chat", StartedBy: "you"}); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := s.LatestThread(ctx, "a1", "chat")
	if err != nil || !ok || got.ID != "t2" || got.Status != "active" {
		t.Fatalf("latest = %+v %v %v", got, ok, err)
	}
	if err := s.TouchThread(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = s.LatestThread(ctx, "a1", "chat"); got.ID != "t1" {
		t.Fatalf("latest after touch = %s", got.ID)
	}
	if err := s.SetReadPosition(ctx, "t1", 7); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseThread(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = s.LatestThread(ctx, "a1", "chat"); got.ID != "t2" {
		t.Fatalf("closed thread still latest: %s", got.ID)
	}
	if _, ok, _ := s.LatestThread(ctx, "a1", "run"); ok {
		t.Fatal("run thread found")
	}
	l, _ := s.ListThreads(ctx, "a1")
	if len(l) != 2 {
		t.Fatalf("threads = %+v", l)
	}
	for _, th := range l {
		if th.ID == "t1" && (th.ReadPosition != 7 || th.Status != "closed") {
			t.Fatalf("t1 = %+v", th)
		}
	}
}

func TestThreadByAgentSession(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	seedAgent(t, s)
	if err := s.InsertThread(ctx, Thread{ID: "t1", AgentID: "a1", Kind: "chat", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertThreadSession(ctx, ThreadSession{ThreadID: "t1", SessionID: "s1", CLI: "claude", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.ThreadByAgentSession(ctx, "claude", "x"); ok {
		t.Fatal("found before set")
	}
	if err := s.SetAgentSessionID(ctx, "s1", "claude", "x", "/tmp/x.jsonl"); err != nil {
		t.Fatal(err)
	}
	th, ok, err := s.ThreadByAgentSession(ctx, "claude", "x")
	if err != nil || !ok || th.ID != "t1" {
		t.Fatalf("got %+v %v %v", th, ok, err)
	}
	if th, ok, _ := s.ThreadBySession(ctx, "s1"); !ok || th.ID != "t1" {
		t.Fatalf("ThreadBySession = %+v %v", th, ok)
	}
	if err := s.EndThreadSession(ctx, "s1", "exit"); err != nil {
		t.Fatal(err)
	}
	ss, _ := s.ThreadSessions(ctx, "t1")
	if len(ss) != 1 || ss[0].EndReason != "exit" || ss[0].EndedAt.IsZero() || ss[0].TranscriptPath != "/tmp/x.jsonl" || ss[0].AgentSessionID != "x" {
		t.Fatalf("sessions = %+v", ss)
	}
	// Unset ids do not collide.
	for _, id := range []string{"s2", "s3"} {
		if err := s.InsertThreadSession(ctx, ThreadSession{ThreadID: "t1", SessionID: id, CLI: "claude", StartedBy: "you"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgentSessionMovesToNewThreadSession(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	seedAgent(t, s)
	if err := s.InsertThread(ctx, Thread{ID: "t1", AgentID: "a1", Kind: "chat", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertThreadSession(ctx, ThreadSession{ThreadID: "t1", SessionID: "s1", CLI: "claude", AgentSessionID: "x", TranscriptPath: "/x", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	if err := s.EndThreadSession(ctx, "s1", "ended"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgentSessionID(ctx, "s1", "claude", "y", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set on ended session err = %v", err)
	}
	if err := s.InsertThreadSession(ctx, ThreadSession{ThreadID: "t1", SessionID: "s2", CLI: "claude", AgentSessionID: "x", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	ss, _ := s.ThreadSessions(ctx, "t1")
	if len(ss) != 2 || ss[0].AgentSessionID != "" || ss[0].TranscriptPath != "/x" || ss[1].AgentSessionID != "x" {
		t.Fatalf("sessions = %+v", ss)
	}
	if err := s.SetEndReason(ctx, "t1", "s1", "resume-failed"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEndReason(ctx, "t1", "s2", "idle-closed"); err != nil {
		t.Fatal(err)
	}
	ss, _ = s.ThreadSessions(ctx, "t1")
	if ss[0].EndReason != "resume-failed" || ss[1].EndReason != "idle-closed" || ss[1].EndedAt.IsZero() {
		t.Fatalf("sessions = %+v", ss)
	}
	if err := s.SetEndReason(ctx, "t1", "nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown err = %v", err)
	}
	if th, ok, err := s.Thread(ctx, "t1"); err != nil || !ok || th.AgentID != "a1" {
		t.Fatalf("thread = %+v %v %v", th, ok, err)
	}
}

func TestEventsAfter(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	seedAgent(t, s)
	var ids []int64
	for _, k := range []string{"user", "assistant", "user"} {
		id, err := s.InsertEvent(ctx, Event{ThreadID: "t1", Kind: k, StartedBy: "you", Payload: json.RawMessage(`{"a":1}`), AnchorSession: "s1", AnchorIndex: 2})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if !(ids[0] < ids[1] && ids[1] < ids[2]) {
		t.Fatalf("ids = %v", ids)
	}
	got, err := s.Events(ctx, "t1", ids[0])
	if err != nil || len(got) != 2 || got[0].ID != ids[1] || got[1].Kind != "user" {
		t.Fatalf("events = %+v %v", got, err)
	}
	if string(got[0].Payload) != `{"a":1}` || got[0].AnchorSession != "s1" || got[0].AnchorIndex != 2 || got[0].CreatedAt.IsZero() {
		t.Fatalf("event = %+v", got[0])
	}
	if got, _ := s.Events(ctx, "t2", 0); len(got) != 0 {
		t.Fatalf("other thread events = %+v", got)
	}
}

func TestPurgeAgentRemovesEverything(t *testing.T) {
	ctx := context.Background()
	s, sqlDB := newStore(t)
	seedAgent(t, s)
	if err := s.InsertAgent(ctx, Agent{ID: "a2", Name: "other", Dir: "/d/2"}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"a1", "a2"} {
		tid := "t-" + a
		if err := s.InsertThread(ctx, Thread{ID: tid, AgentID: a, Kind: "chat", StartedBy: "you"}); err != nil {
			t.Fatal(err)
		}
		if err := s.InsertThreadSession(ctx, ThreadSession{ThreadID: tid, SessionID: "s-" + a, CLI: "claude", StartedBy: "you"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.InsertEvent(ctx, Event{ThreadID: tid, Kind: "user", StartedBy: "you", Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.Exec(`INSERT INTO pagent_capture_cursor (thread_id, session_id, entry_count, last_entry_id, updated_at) VALUES (?, ?, 1, 'e', 1)`, tid, "s-"+a); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.Exec(`INSERT INTO pagent_memory_proposal (id, agent_id, thread_id, ops, created_at) VALUES (?, ?, ?, '[]', 1)`, "p-"+a, a, tid); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PurgeAgent(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	count := func(q string, args ...any) int {
		var n int
		if err := sqlDB.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, tbl := range []string{"pagent", "pagent_thread", "pagent_thread_session", "pagent_thread_event", "pagent_capture_cursor", "pagent_memory_proposal"} {
		if n := count("SELECT COUNT(*) FROM " + tbl); n != 1 {
			t.Errorf("%s rows = %d, want 1 (a2 only)", tbl, n)
		}
	}
	if _, ok, _ := s.AgentByID(ctx, "a1"); ok {
		t.Fatal("a1 still present")
	}
}

func TestProposalsPendingAndDecide(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	seedAgent(t, s)
	for _, id := range []string{"p1", "p2"} {
		if err := s.InsertProposal(ctx, Proposal{ID: id, AgentID: "a1", ThreadID: "t1", Ops: json.RawMessage(`{"ops":[]}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertProposal(ctx, Proposal{ID: "p3", AgentID: "a2", Ops: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingProposals(ctx, "a1")
	if err != nil || len(pending) != 2 || pending[0].ID != "p1" || pending[1].ThreadID != "t1" || string(pending[0].Ops) != `{"ops":[]}` {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if err := s.DecideProposal(ctx, "p1", "accepted"); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideProposal(ctx, "p1", "rejected"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second decide err = %v", err)
	}
	p, ok, err := s.Proposal(ctx, "p1")
	if err != nil || !ok || p.Decision != "accepted" || p.DecidedAt.IsZero() || p.CreatedAt.IsZero() {
		t.Fatalf("proposal = %+v %v %v", p, ok, err)
	}
	if pending, _ := s.PendingProposals(ctx, "a1"); len(pending) != 1 || pending[0].ID != "p2" {
		t.Fatalf("pending after decide = %+v", pending)
	}
	if _, ok, _ := s.Proposal(ctx, "nope"); ok {
		t.Fatal("missing proposal found")
	}
}

func TestCaptureCursorRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	if _, ok, err := s.CaptureCursor(ctx, "th", "s"); err != nil || ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	for _, n := range []int{2, 5} {
		if err := s.SetCaptureCursor(ctx, CaptureCursor{ThreadID: "th", SessionID: "s", EntryCount: n, LastEntryID: "e"}); err != nil {
			t.Fatal(err)
		}
	}
	c, ok, err := s.CaptureCursor(ctx, "th", "s")
	if err != nil || !ok || c.EntryCount != 5 || c.LastEntryID != "e" || c.UpdatedAt.IsZero() {
		t.Fatalf("cursor = %+v, ok = %v, err = %v", c, ok, err)
	}
}
