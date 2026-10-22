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

func TestMeshBindsPaneKeyToAgentKey(t *testing.T) {
	d, store := meshNode(t)
	ctx := context.Background()
	hookSession(d, "%3", "")
	snap := d.reg.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("sessions = %d", len(snap))
	}
	id := snap[0].ID
	if _, err := store.Insert(ctx, api.Message{
		Kind: api.MessageText, From: api.Party{Node: d.id, Key: api.PartyOwner},
		To: api.Party{Node: d.id, Key: id}, Body: "hi", State: api.MessageQueued,
	}); err != nil {
		t.Fatal(err)
	}
	d.meshPass(ctx)
	hookSession(d, "%3", "c1")
	d.meshPass(ctx)
	if _, ok, _ := store.NextQueued(ctx, "claude:c1"); !ok {
		t.Fatal("message not rebound to claude:c1")
	}
}

func TestMeshRekeysOnAgentSessionChange(t *testing.T) {
	d, store := meshNode(t)
	ctx := context.Background()
	hookSession(d, "%3", "c1")
	if err := store.PutLink(ctx, api.Link{
		Child:  api.Party{Node: d.id, Key: "claude:c1"},
		Parent: api.Party{Node: d.id, Key: "claude:p1"},
	}); err != nil {
		t.Fatal(err)
	}
	d.meshPass(ctx)
	hookSession(d, "%3", "c2")
	d.meshPass(ctx)
	if _, ok, _ := store.Link(ctx, "claude:c2"); !ok {
		t.Fatal("link not rekeyed to claude:c2")
	}
	if _, ok, _ := store.Link(ctx, "claude:c1"); ok {
		t.Fatal("old link key still present")
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

func TestMeshRekeysAcrossRegistryGap(t *testing.T) {
	d, store := meshNode(t)
	ctx := context.Background()
	hookSession(d, "%3", "c1")
	if err := store.PutLink(ctx, api.Link{
		Child:  api.Party{Node: d.id, Key: "claude:c1"},
		Parent: api.Party{Node: d.id, Key: "claude:p1"},
	}); err != nil {
		t.Fatal(err)
	}
	d.meshPass(ctx)
	d.reg.ReconcileSessions("claude", nil)
	if len(d.reg.Snapshot()) != 0 {
		t.Fatal("session still in registry")
	}
	d.meshPass(ctx)
	hookSession(d, "%3", "c2")
	d.meshPass(ctx)
	if _, ok, _ := store.Link(ctx, "claude:c2"); !ok {
		t.Fatal("link not rekeyed to claude:c2")
	}
	if _, ok, _ := store.Link(ctx, "claude:c1"); ok {
		t.Fatal("old link key still present")
	}
}
