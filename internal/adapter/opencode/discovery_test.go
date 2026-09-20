package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// newTestDiscoverer constructs a discoverer with the given registry and an
// injected dial function returning c (ok=true) or (nil,false) when c is nil.
func newTestDiscoverer(reg *registry.Registry, c *client) *discoverer {
	d := &discoverer{
		reg:      reg,
		ctx:      context.Background(),
		presence: map[string]*presenceEntry{},
		panes:    map[string]string{},
		pendPerm: map[string]string{},
		pendForm: map[string]*pendingForm{},
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

func TestPresenceUpsertRetriesHydrationUntilSuccess(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session/ses_1" {
			if fail.Load() {
				w.WriteHeader(500)
				return
			}
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
	if len(snap) != 1 || snap[0].AgentSessionID != "ses_1" {
		t.Fatalf("after failed hydrate: %+v", snap)
	}
	if snap[0].Name != "" {
		t.Fatalf("expected empty Name after failed getSession: %+v", snap[0])
	}
	d.mu.Lock()
	if d.presence["ses_1"].hydrated {
		t.Fatal("entry must not be hydrated after getSession failure")
	}
	d.mu.Unlock()

	fail.Store(false)
	d.upsert("ses_1", session.StatusWorking, nil)
	snap = reg.Snapshot()
	if len(snap) != 1 || snap[0].Name != "T" || snap[0].Cwd != "/repo" {
		t.Fatalf("after successful retry: %+v", snap[0])
	}
	d.mu.Lock()
	if !d.presence["ses_1"].hydrated {
		t.Fatal("entry must be hydrated after getSession success")
	}
	d.mu.Unlock()
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

func TestDismissRemovesAndReappearsOnActivity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/session/") {
			_, _ = w.Write([]byte(`{"data":{"id":"ses_1","title":"T","location":{"directory":"/repo"}}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	reg := registry.New()
	a := &ocAdapter{disc: newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))}

	a.disc.upsert("ses_1", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
	if err := a.Dismiss(context.Background(), session.Session{Agent: Agent, AgentSessionID: "ses_1"}); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if len(reg.Snapshot()) != 0 {
		t.Fatal("dismiss should remove from list")
	}
	a.disc.applyEvent(sseFrame{Type: "session.execution.started", Data: json.RawMessage(`{"sessionID":"ses_1"}`)})
	if len(reg.Snapshot()) != 1 {
		t.Fatal("new activity should re-add")
	}
}

func TestScanOnceSeedsRecentIdleSessions(t *testing.T) {
	now := time.Now().UnixMilli()
	recent := now - (10 * time.Minute).Milliseconds()
	old := now - (2 * time.Hour).Milliseconds()
	list := `{"data":[` +
		`{"id":"ses_run","title":"Run","location":{"directory":"/repo"},"time":{"updated":` + itoa(now) + `}},` +
		`{"id":"ses_idle","title":"Idle","location":{"directory":"/repo"},"time":{"updated":` + itoa(recent) + `}},` +
		`{"id":"ses_old","title":"Old","location":{"directory":"/repo"},"time":{"updated":` + itoa(old) + `}}` +
		`]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{"ses_run":{"type":"running"}}}`))
		case "/api/session":
			_, _ = w.Write([]byte(list))
		case "/api/session/ses_run":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_run","title":"Run","location":{"directory":"/repo"}}}`))
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

	got := map[string]session.Status{}
	for _, s := range reg.Snapshot() {
		got[s.AgentSessionID] = s.Status
	}
	if got["ses_run"] != session.StatusWorking {
		t.Fatalf("running session should stay Working: %v", got)
	}
	if got["ses_idle"] != session.StatusAwaitingInput {
		t.Fatalf("recent idle session should seed as AwaitingInput: %v", got)
	}
	if _, ok := got["ses_old"]; ok {
		t.Fatalf("session idle beyond the TTL must not seed: %v", got)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestAdapterSendPromptPostsViaClient(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			gotPath = r.URL.Path
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			_, _ = w.Write([]byte(`{"data":{}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	reg := registry.New()
	a := &ocAdapter{disc: newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))}

	if err := a.SendPrompt(context.Background(), session.Session{Agent: Agent, AgentSessionID: "ses_1"}, "hello"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	if gotPath != "/api/session/ses_1/prompt" || gotBody != `{"text":"hello"}` {
		t.Fatalf("path=%s body=%s", gotPath, gotBody)
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
