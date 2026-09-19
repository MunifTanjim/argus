package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// newTestDiscoverer constructs a discoverer with the given registry and an
// injected dial function returning c (ok=true) or (nil,false) when c is nil.
// Reused by Task 4/5 tests.
func newTestDiscoverer(reg *registry.Registry, c *client) *discoverer {
	d := &discoverer{
		reg:      reg,
		ctx:      context.Background(),
		presence: map[string]*presenceEntry{},
		pendPerm: map[string]string{},
	}
	d.dial = func() (*client, bool) {
		if c == nil {
			return nil, false
		}
		return c, true
	}
	return d
}

func TestPresenceUpsertAddsAndUpdates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session/ses_1" {
			_, _ = w.Write([]byte(`{"data":{"id":"ses_1","title":"T","location":{"directory":"/repo"}}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	reg := registry.New()
	d := newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))

	d.upsert("ses_1", session.StatusWorking, nil)
	snap := reg.Snapshot()
	if len(snap) != 1 || snap[0].AgentSessionID != "ses_1" || snap[0].Status != session.StatusWorking {
		t.Fatalf("after upsert: %+v", snap)
	}
	if snap[0].Name != "T" || snap[0].Cwd != "/repo" || snap[0].TranscriptPath != "ses_1" {
		t.Fatalf("display fields: %+v", snap[0])
	}

	d.remove("ses_1")
	if len(reg.Snapshot()) != 0 {
		t.Fatalf("after remove, expected empty")
	}
}

func TestPresenceSweepIdleAgesOutExceptPermission(t *testing.T) {
	reg := registry.New()
	d := newTestDiscoverer(reg, nil)
	now := time.Now()
	d.mu.Lock()
	d.presence["old_idle"] = &presenceEntry{lastActivity: now.Add(-2 * time.Hour), status: session.StatusAwaitingInput, awaitingPermission: false}
	d.presence["old_perm"] = &presenceEntry{lastActivity: now.Add(-2 * time.Hour), status: session.StatusAwaitingInput, awaitingPermission: true}
	d.mu.Unlock()
	reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: "old_idle", Status: session.StatusAwaitingInput})
	reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: "old_perm", Status: session.StatusAwaitingInput})

	d.sweepIdle()

	ids := map[string]bool{}
	for _, s := range reg.Snapshot() {
		ids[s.AgentSessionID] = true
	}
	if ids["old_idle"] {
		t.Fatal("idle session should have aged out")
	}
	if !ids["old_perm"] {
		t.Fatal("awaiting-permission session must not age out")
	}
}

func TestScanOnceSeedsRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{"ses_1":{"type":"running"}}}`))
		case "/api/session/ses_1":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_1","title":"T","location":{"directory":"/repo"}}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	reg := registry.New()
	d := newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))
	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	snap := reg.Snapshot()
	if len(snap) != 1 || snap[0].AgentSessionID != "ses_1" || snap[0].Status != session.StatusWorking {
		t.Fatalf("seed: %+v", snap)
	}
}
