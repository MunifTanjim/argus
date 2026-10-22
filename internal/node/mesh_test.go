package node

import (
	"context"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/mesh"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func meshNode(t *testing.T) (*Node, *mesh.Store) {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	sqlDB, err := db.Open(t.TempDir() + "/argus.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	store := mesh.NewStore(sqlDB, time.Now)
	d.SetMeshStore(store)
	return d, store
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

func linkAndQueue(t *testing.T, d *Node, store *mesh.Store, child string) {
	t.Helper()
	if err := store.PutLink(t.Context(), api.Link{
		Child:  api.Party{Node: d.id, Key: child},
		Parent: api.Party{Node: d.id, Key: "claude:p1"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Insert(t.Context(), api.Message{
		ID: "m-00000001", Kind: api.MessageText, From: api.Party{Node: d.id, Key: api.PartyOwner},
		To: api.Party{Node: d.id, Key: child}, Body: "hi", State: api.MessageQueued,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertParty(t *testing.T, store *mesh.Store, key string) {
	t.Helper()
	if _, ok, err := store.Link(t.Context(), key); err != nil || !ok {
		t.Fatalf("link not under %s: ok=%v err=%v", key, ok, err)
	}
	m, ok, err := store.Get(t.Context(), "m-00000001")
	if err != nil || !ok || m.To.Key != key {
		t.Fatalf("message = %+v ok=%v err=%v, want it under %s", m, ok, err, key)
	}
}

func TestMeshRekeysOnClear(t *testing.T) {
	d, store := meshNode(t)
	ctx := context.Background()
	hookSession(d, "%3", "c1")
	linkAndQueue(t, d, store, "claude:c1")
	d.meshPass(ctx)
	clearSession(d, "%3", "c2")
	d.meshPass(ctx)
	assertParty(t, store, "claude:c2")
	if _, ok, _ := store.Link(ctx, "claude:c1"); ok {
		t.Fatal("old link key still present")
	}
}

func TestMeshPaneReuseKeepsOldParty(t *testing.T) {
	cases := map[string]func(d *Node){
		"same record":  func(*Node) {},
		"registry gap": func(d *Node) { d.reg.ReconcileSessions("claude", nil) },
	}
	for name, between := range cases {
		t.Run(name, func(t *testing.T) {
			d, store := meshNode(t)
			ctx := context.Background()
			hookSession(d, "%3", "c1")
			linkAndQueue(t, d, store, "claude:c1")
			d.meshPass(ctx)
			between(d)
			d.meshPass(ctx)
			hookSession(d, "%3", "c2")
			d.meshPass(ctx)
			assertParty(t, store, "claude:c1")
			s, _ := d.reg.FindByAgentSession("claude", "c2")
			if p := d.withCaps(s).Parent; p != nil {
				t.Fatalf("new conversation inherited parent %+v", p)
			}
		})
	}
}

func TestMeshSessionWithoutAgentIDHasNoParty(t *testing.T) {
	d, store := meshNode(t)
	ctx := context.Background()
	hookSession(d, "%3", "")
	s := d.reg.Snapshot()[0]
	linkAndQueue(t, d, store, s.ID)
	d.meshPass(ctx)
	hookSession(d, "%3", "c1")
	d.meshPass(ctx)
	assertParty(t, store, s.ID)
	s, _ = d.reg.FindByAgentSession("claude", "c1")
	if p := d.withCaps(s).Parent; p != nil {
		t.Fatalf("pane-keyed link bound to the session: %+v", p)
	}
}

func TestWithCapsStampsParent(t *testing.T) {
	d, store := meshNode(t)
	ctx := context.Background()
	hookSession(d, "%1", "c1")
	if err := store.PutLink(ctx, api.Link{
		Child:  api.Party{Node: d.id, Key: "claude:c1"},
		Parent: api.Party{Node: d.id, Key: "claude:p1"},
	}); err != nil {
		t.Fatal(err)
	}
	child, _ := d.reg.FindByAgentSession("claude", "c1")
	if p := d.withCaps(child).Parent; p == nil || !p.Gone || p.Key != "claude:p1" {
		t.Fatalf("absent parent: %+v", p)
	}
	hookSession(d, "%2", "p1")
	parent, _ := d.reg.FindByAgentSession("claude", "p1")
	p := d.withCaps(child).Parent
	if p == nil || p.Gone || p.SessionID != parent.ID {
		t.Fatalf("live parent: %+v", p)
	}
}

func TestRemoteDispatchRejectsAgentctl(t *testing.T) {
	d, _ := meshNode(t)
	_, err := d.remoteDispatch()(context.Background(), "agentctl.whoami", nil)
	rpcErr, ok := err.(*api.RPCError)
	if !ok || rpcErr.Code != api.CodeMethodNotFound {
		t.Fatalf("err = %v", err)
	}
}
