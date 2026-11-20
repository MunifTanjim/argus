package node

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func orchNode(t *testing.T) (*Node, *orch.Store) {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	sqlDB, err := db.Open(t.TempDir() + "/argus.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	store := orch.NewStore(sqlDB, time.Now)
	d.SetOrchStore(store)
	d.orchGoneGrace = 0
	return d, store
}

func orchNodeDB(t *testing.T, id string) (*Node, *sql.DB) {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.id = id
	sqlDB, err := db.Open(t.TempDir() + "/argus.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	d.SetOrchStore(orch.NewStore(sqlDB, time.Now))
	d.orchGoneGrace = 0
	return d, sqlDB
}

func mustExec(t *testing.T, sqlDB *sql.DB, q string) {
	t.Helper()
	if _, err := sqlDB.Exec(q); err != nil {
		t.Fatal(err)
	}
}

func hookSession(d *Node, pane, agentID string) {
	d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: pane,
		AgentSessionID: agentID, Status: session.StatusIdle,
	})
}

func clearSession(d *Node, pane, agentID string) {
	d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: pane,
		AgentSessionID: agentID, Status: session.StatusIdle, Cleared: true,
	})
}

func linkAndQueue(t *testing.T, d *Node, store *orch.Store, child string) {
	t.Helper()
	if err := store.PutLink(t.Context(), api.Link{
		Child:  api.Actor{Node: d.id, Key: child},
		Parent: api.Actor{Node: d.id, Key: "claude:p1"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Insert(t.Context(), api.Message{
		ID: "m-00000001", Kind: api.MessageText, From: api.Actor{Node: d.id, Key: api.ActorOwner},
		To: api.Actor{Node: d.id, Key: child}, Body: "hi", State: api.MessageQueued,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertActor(t *testing.T, d *Node, store *orch.Store, key string) {
	t.Helper()
	if _, ok, err := store.Link(t.Context(), api.Actor{Node: d.id, Key: key}); err != nil || !ok {
		t.Fatalf("link not under %s: ok=%v err=%v", key, ok, err)
	}
	m, ok, err := store.Get(t.Context(), "m-00000001")
	if err != nil || !ok || m.To.Key != key {
		t.Fatalf("message = %+v ok=%v err=%v, want it under %s", m, ok, err, key)
	}
}

func TestOrchPaneReuseKeepsOldActor(t *testing.T) {
	cases := map[string]func(d *Node){
		"same record":  func(*Node) {},
		"registry gap": func(d *Node) { d.reg.ReconcileSessions("claude", nil) },
	}
	for name, between := range cases {
		t.Run(name, func(t *testing.T) {
			d, store := orchNode(t)
			ctx := context.Background()
			hookSession(d, "%3", "c1")
			linkAndQueue(t, d, store, "claude:c1")
			d.orchPass(ctx)
			between(d)
			d.orchPass(ctx)
			hookSession(d, "%3", "c2")
			d.orchPass(ctx)
			assertActor(t, d, store, "claude:c1")
			s, _ := d.reg.FindByAgentSession("claude", "c2")
			if p := d.withCaps(s).Parent; p != nil {
				t.Fatalf("new conversation inherited parent %+v", p)
			}
		})
	}
}

func TestOrchSessionWithoutAgentIDHasNoActor(t *testing.T) {
	d, store := orchNode(t)
	ctx := context.Background()
	hookSession(d, "%3", "")
	s := d.reg.Snapshot()[0]
	linkAndQueue(t, d, store, s.ID)
	d.orchPass(ctx)
	hookSession(d, "%3", "c1")
	d.orchPass(ctx)
	assertActor(t, d, store, s.ID)
	s, _ = d.reg.FindByAgentSession("claude", "c1")
	if p := d.withCaps(s).Parent; p != nil {
		t.Fatalf("pane-keyed link bound to the session: %+v", p)
	}
}

func TestWithCapsStampsParent(t *testing.T) {
	d, store := orchNode(t)
	ctx := context.Background()
	hookSession(d, "%1", "c1")
	if err := store.PutLink(ctx, api.Link{
		Child:  api.Actor{Node: d.id, Key: "claude:c1"},
		Parent: api.Actor{Node: d.id, Key: "claude:p1"},
	}); err != nil {
		t.Fatal(err)
	}
	child, _ := d.reg.FindByAgentSession("claude", "c1")
	if p := d.withCaps(child).Parent; p == nil || !p.Gone || p.ID != actorID(api.Actor{Node: d.id, Key: "claude:p1"}) {
		t.Fatalf("absent parent: %+v", p)
	}
	hookSession(d, "%2", "p1")
	p := d.withCaps(child).Parent
	if p == nil || p.Gone || p.ID != actorID(api.Actor{Node: d.id, Key: "claude:p1"}) {
		t.Fatalf("live parent: %+v", p)
	}
}

func TestRemoteDispatchRejectsOrch(t *testing.T) {
	d, _ := orchNode(t)
	for _, m := range []string{api.MethodOrchSessionCurrent, api.MethodOrchMessageSend, api.MethodOrchInteractionRespond, api.MethodOrchPeerDeliver} {
		_, err := d.remoteDispatch()(context.Background(), m, nil)
		rpcErr, ok := err.(*api.RPCError)
		if !ok || rpcErr.Code != api.CodeMethodNotFound {
			t.Fatalf("%s: err = %v, want method not found", m, err)
		}
	}
}

func TestRemoteDispatchServesMessageList(t *testing.T) {
	d, _ := orchNode(t)
	if _, err := d.remoteDispatch()(context.Background(), api.MethodMessageList, nil); err != nil {
		t.Fatalf("message.list: err = %v", err)
	}
}

func TestOrchRepublishesChildWhenParentChanges(t *testing.T) {
	d, store := orchNode(t)
	ctx := context.Background()
	hookSession(d, "%1", "c1")
	d.orchPass(ctx)
	events, cancel := d.reg.Subscribe()
	defer cancel()
	child, _ := d.reg.FindByAgentSession("claude", "c1")
	republished := func() bool {
		for {
			select {
			case ev := <-events:
				if ev.Session.ID == child.ID && ev.Type == registry.EventUpdated {
					return true
				}
			default:
				return false
			}
		}
	}

	if err := store.PutLink(ctx, api.Link{
		Child:  api.Actor{Node: d.id, Key: "claude:c1"},
		Parent: api.Actor{Node: d.id, Key: "claude:p1"},
	}); err != nil {
		t.Fatal(err)
	}
	d.orchPass(ctx)
	if !republished() {
		t.Fatal("child not republished after the link was made")
	}
	d.orchPass(ctx)
	if republished() {
		t.Fatal("child republished with no change")
	}
	hookSession(d, "%2", "p1")
	republished()
	d.orchPass(ctx)
	if !republished() {
		t.Fatal("child not republished after the parent came back")
	}
}

func TestOrchClearKeepsTheOldLink(t *testing.T) {
	d, store := orchNode(t)
	ctx := context.Background()
	hookSession(d, "%3", "c1")
	linkAndQueue(t, d, store, "claude:c1")
	d.orchPass(ctx)
	clearSession(d, "%3", "c2")
	d.orchPass(ctx)
	if _, ok, _ := store.Link(ctx, api.Actor{Node: d.id, Key: "claude:c1"}); !ok {
		t.Fatal("the link left the old key")
	}
	if _, ok, _ := store.Link(ctx, api.Actor{Node: d.id, Key: "claude:c2"}); ok {
		t.Fatal("the link moved to the new conversation")
	}
	if m, _, _ := store.Get(ctx, "m-00000001"); m.State != api.MessageFailed || m.To.Key != "claude:c1" {
		t.Fatalf("message = %+v, want failed under claude:c1", m)
	}
	s, _ := d.reg.FindByAgentSession("claude", "c2")
	if p := d.withCaps(s).Parent; p != nil {
		t.Fatalf("new conversation inherited parent %+v", p)
	}
}

func TestOrchClearSendsNothingToPeers(t *testing.T) {
	d, store := orchNode(t)
	d.SetIdentity("a", "")
	store.SetNodeID("a")
	hookSession(d, "%3", "c1")
	if _, err := store.Receive(t.Context(), api.Message{
		ID: "b:m-0000000000000001", Kind: api.MessageText, From: api.Actor{Node: "b", Key: "claude:w"},
		To: api.Actor{Node: "a", Key: "claude:c1"}, Body: "hi", State: api.MessageDelivered,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	d.orchPass(t.Context())
	clearSession(d, "%3", "c2")
	d.orchPass(t.Context())
	if items, err := store.Pending(t.Context(), "b", 10); err != nil || len(items) != 0 {
		t.Fatalf("b's outbox = %+v err=%v, want nothing", items, err)
	}
}

func TestOrchGoneGraceKeepsAShortDropOut(t *testing.T) {
	d, store := orchNode(t)
	d.orchGoneGrace = time.Hour
	ctx := context.Background()
	hookSession(d, "%3", "c1")
	linkAndQueue(t, d, store, "claude:c1")
	d.orchPass(ctx)
	d.reg.ReconcileSessions("claude", nil)
	d.orchPass(ctx)
	hookSession(d, "%3", "c1")
	d.orchPass(ctx)
	assertActor(t, d, store, "claude:c1")
	if m, _, _ := store.Get(ctx, "m-00000001"); m.State != api.MessageQueued {
		t.Fatalf("state = %s, want queued", m.State)
	}
}

func TestOrchOldAgentIDNeverReachesNewConversation(t *testing.T) {
	d, _ := orchNode(t)
	ctx := context.Background()
	d.SetIdentity("home", "")
	d.markOrchSettled()
	hookSession(d, "%3", "c1")
	d.orchPass(ctx)
	clearSession(d, "%3", "c2")
	d.orchPass(ctx)
	owner := callerRef{Actor: api.Actor{Node: "home", Key: api.ActorOwner}}
	const old = "home:claude:c1"
	if _, err := d.send(ctx, owner, old, "hi", ""); !isCode(err, api.CodeUnknownSession) {
		t.Errorf("send: err = %v", err)
	}
	if _, err := d.getSession(ctx, old); !isCode(err, api.CodeUnknownSession) {
		t.Errorf("get: err = %v", err)
	}
	if _, err := d.output(ctx, old); !isCode(err, api.CodeUnknownSession) {
		t.Errorf("output: err = %v", err)
	}
	if _, err := d.transcript(ctx, old, 0); !isCode(err, api.CodeUnknownSession) {
		t.Errorf("transcript: err = %v", err)
	}
	if _, err := d.rename(ctx, old, "x"); !isCode(err, api.CodeUnknownSession) {
		t.Errorf("rename: err = %v", err)
	}
	if _, err := d.kill(ctx, owner, old); !isCode(err, api.CodeUnknownSession) {
		t.Errorf("kill: err = %v", err)
	}
	if got := messagesTo(t, d, "claude:c2"); len(got) != 0 {
		t.Fatalf("new conversation got %+v", got)
	}
}
