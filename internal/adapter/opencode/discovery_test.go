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

func TestSpawnSessionCreatesPromptsAndRegisters(t *testing.T) {
	var createBody, promptBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/session":
			b, _ := io.ReadAll(r.Body)
			createBody = string(b)
			_, _ = w.Write([]byte(`{"data":{"id":"ses_new","title":"New","location":{"directory":"/repo"}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/session/ses_new/prompt":
			b, _ := io.ReadAll(r.Body)
			promptBody = string(b)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/session/ses_new":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_new","title":"New","location":{"directory":"/repo"}}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	reg := registry.New()
	d := newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))

	id, err := d.spawnSession(context.Background(), "/repo", "do the thing")
	if err != nil {
		t.Fatalf("spawnSession: %v", err)
	}
	if id != "ses_new" {
		t.Fatalf("id = %q, want ses_new", id)
	}
	if !strings.Contains(createBody, `"directory":"/repo"`) {
		t.Fatalf("create body missing cwd: %q", createBody)
	}
	if !strings.Contains(promptBody, "do the thing") {
		t.Fatalf("prompt body missing text: %q", promptBody)
	}
	snap := reg.Snapshot()
	if len(snap) != 1 || snap[0].AgentSessionID != "ses_new" || snap[0].Status != session.StatusWorking {
		t.Fatalf("after spawn: %+v", snap)
	}
	if snap[0].ID != Agent+":ses_new" || snap[0].Cwd != "/repo" || snap[0].Input != session.InputAPI {
		t.Fatalf("registered record: %+v", snap[0])
	}
}

func TestSpawnSessionServiceUnavailable(t *testing.T) {
	d := newTestDiscoverer(registry.New(), nil)
	if _, err := d.spawnSession(context.Background(), "/repo", "hi"); err == nil {
		t.Fatal("expected error when service is unavailable")
	}
}

func TestSpawnSessionWithoutPromptSkipsPrompt(t *testing.T) {
	prompted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/session":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_np"}}`))
		case r.URL.Path == "/api/session/ses_np/prompt":
			prompted = true
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/api/session/ses_np":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_np"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	reg := registry.New()
	d := newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))

	if _, err := d.spawnSession(context.Background(), "", ""); err != nil {
		t.Fatalf("spawnSession: %v", err)
	}
	if prompted {
		t.Fatal("prompt endpoint called for an empty prompt")
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

// A session that waits for a form answer stays in /api/session/active, so the
// scan's active loop must not downgrade it to Working and clear the SSE-set
// interaction.
func TestScanOnceKeepsPendingInteraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{"ses_q":{"type":"running"},"ses_p":{"type":"running"}}}`))
		case "/api/session":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/api/session/ses_q/form":
			_, _ = w.Write([]byte(`{"data":[{"id":"frm_1"}]}`))
		case "/api/session/ses_p/permission":
			_, _ = w.Write([]byte(`{"data":[{"id":"perm_1"}]}`))
		case "/api/session/ses_q":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_q","title":"Q","location":{"directory":"/repo"}}}`))
		case "/api/session/ses_p":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_p","title":"P","location":{"directory":"/repo"}}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	reg := registry.New()
	d := newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))

	// The event pump established a pending question and permission, both still live
	// on the server (returned by the list endpoints above).
	d.mu.Lock()
	d.pendForm["ses_q"] = &pendingForm{formID: "frm_1"}
	d.pendPerm["ses_p"] = "perm_1"
	d.mu.Unlock()
	d.upsert("ses_q", session.StatusAwaitingInput, &session.Interaction{
		Kind:      session.InteractionQuestion,
		Questions: []session.QuestionSpec{{Header: "Friendly greeting", Question: "Which greeting?", Options: []string{"Hello", "Hi"}}},
	})
	d.upsert("ses_p", session.StatusAwaitingInput, &session.Interaction{
		Kind:     session.InteractionPermission,
		ToolName: "bash",
	})

	// Backdate past the idle TTL so ScanOnce's sweepIdle would age these out unless
	// the pending prompt protects them.
	d.mu.Lock()
	stale := time.Now().Add(-2 * sessionIdleTTL)
	d.presence["ses_q"].lastActivity = stale
	d.presence["ses_p"].lastActivity = stale
	d.mu.Unlock()

	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	byID := map[string]session.Session{}
	for _, s := range reg.Snapshot() {
		byID[s.AgentSessionID] = s
	}
	q := byID["ses_q"]
	if q.Status != session.StatusAwaitingInput {
		t.Fatalf("question session status = %q, want awaiting_input (scan must not downgrade it)", q.Status)
	}
	if q.Interaction == nil || len(q.Interaction.Questions) == 0 {
		t.Fatalf("pending question was cleared by scan: %+v", q.Interaction)
	}
	p := byID["ses_p"]
	if p.Status != session.StatusAwaitingInput {
		t.Fatalf("permission session status = %q, want awaiting_input (scan must not downgrade it)", p.Status)
	}
	if p.Interaction == nil || p.Interaction.Kind != session.InteractionPermission {
		t.Fatalf("pending permission was cleared by scan: %+v", p.Interaction)
	}
}

// TestScanOnceReconcilesSettledPending covers the case where the SSE reply event
// was missed (pump reconnect). The server no longer lists the form/permission, so
// a scan must drop the stale pend entry and return the session to idle instead of
// stranding it at AwaitingInput.
func TestScanOnceReconcilesSettledPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{}}`))
		case "/api/session":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/api/session/ses_q/form", "/api/session/ses_p/permission":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/api/session/ses_q":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_q","title":"Q","location":{"directory":"/repo"}}}`))
		case "/api/session/ses_p":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_p","title":"P","location":{"directory":"/repo"}}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	reg := registry.New()
	d := newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))

	d.mu.Lock()
	d.pendForm["ses_q"] = &pendingForm{formID: "frm_gone"}
	d.pendPerm["ses_p"] = "perm_gone"
	d.mu.Unlock()
	d.upsert("ses_q", session.StatusAwaitingInput, &session.Interaction{
		Kind:      session.InteractionQuestion,
		Questions: []session.QuestionSpec{{Header: "H", Question: "Q?"}},
	})
	d.upsert("ses_p", session.StatusAwaitingInput, &session.Interaction{
		Kind:     session.InteractionPermission,
		ToolName: "bash",
	})

	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	d.mu.Lock()
	_, formStuck := d.pendForm["ses_q"]
	_, permStuck := d.pendPerm["ses_p"]
	d.mu.Unlock()
	if formStuck {
		t.Fatal("settled form must be reconciled out of pendForm")
	}
	if permStuck {
		t.Fatal("settled permission must be reconciled out of pendPerm")
	}
	byID := map[string]session.Session{}
	for _, s := range reg.Snapshot() {
		byID[s.AgentSessionID] = s
	}
	if ix := byID["ses_q"].Interaction; ix == nil || ix.Kind != session.InteractionIdle {
		t.Fatalf("ses_q interaction = %+v, want idle after reconcile", ix)
	}
	if ix := byID["ses_p"].Interaction; ix == nil || ix.Kind != session.InteractionIdle {
		t.Fatalf("ses_p interaction = %+v, want idle after reconcile", ix)
	}
}

// TestScanOnceBackfillsMissedForm covers the reported failure: a question was
// asked while argus was not listening (started or reconnected after form.created),
// so argus never got the event. A scan must discover the pending form from the
// server and render it, not leave the session at Working with no prompt.
func TestScanOnceBackfillsMissedForm(t *testing.T) {
	form := `{"id":"frm_m","sessionID":"ses_m","title":"Questions","fields":[` +
		`{"key":"q0","title":"Friendly greeting","description":"Which greeting?","type":"string",` +
		`"options":[{"value":"Hello","label":"Hello"},{"value":"Hi","label":"Hi"}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{"ses_m":{"type":"running"}}}`))
		case "/api/session":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/api/session/ses_m/form":
			_, _ = w.Write([]byte(`{"data":[` + form + `]}`))
		case "/api/session/ses_m/permission":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/api/session/ses_m":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_m","title":"M","location":{"directory":"/repo"}}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	reg := registry.New()
	d := newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))

	// argus does not know about the form (pendForm empty), as after a restart.
	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	s := map[string]session.Session{}
	for _, x := range reg.Snapshot() {
		s[x.AgentSessionID] = x
	}
	m := s["ses_m"]
	if m.Status != session.StatusAwaitingInput {
		t.Fatalf("status = %q, want awaiting_input after backfill", m.Status)
	}
	if m.Interaction == nil || m.Interaction.Kind != session.InteractionQuestion || len(m.Interaction.Questions) == 0 {
		t.Fatalf("missed form not backfilled: %+v", m.Interaction)
	}
	if got := m.Interaction.Questions[0].Header; got != "Friendly greeting" {
		t.Fatalf("question header = %q, want %q", got, "Friendly greeting")
	}
	d.mu.Lock()
	pf := d.pendForm["ses_m"]
	d.mu.Unlock()
	if pf == nil || pf.formID != "frm_m" {
		t.Fatalf("pendForm not recorded on backfill: %+v", pf)
	}
}

// TestScanOnceBackfillsMissedPermission is the permission counterpart of the form
// backfill: a scan discovers a pending permission argus never saw over SSE.
func TestScanOnceBackfillsMissedPermission(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{"ses_perm":{"type":"running"}}}`))
		case "/api/session":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/api/session/ses_perm/form":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/api/session/ses_perm/permission":
			_, _ = w.Write([]byte(`{"data":[{"id":"per_m","action":"bash","source":{"type":"tool"}}]}`))
		case "/api/session/ses_perm":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_perm","title":"P","location":{"directory":"/repo"}}}`))
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

	s := map[string]session.Session{}
	for _, x := range reg.Snapshot() {
		s[x.AgentSessionID] = x
	}
	p := s["ses_perm"]
	if p.Interaction == nil || p.Interaction.Kind != session.InteractionPermission {
		t.Fatalf("missed permission not backfilled: %+v", p.Interaction)
	}
	if p.Interaction.ToolName != "bash" {
		t.Fatalf("permission toolName = %q, want bash", p.Interaction.ToolName)
	}
	d.mu.Lock()
	reqID := d.pendPerm["ses_perm"]
	d.mu.Unlock()
	if reqID != "per_m" {
		t.Fatalf("pendPerm not recorded on backfill: %q", reqID)
	}
}

// TestScanOnceKeepsPendingOnFetchError checks that a transient list error does
// not clear a live prompt. Only a definitive "gone" from the server may clear it.
func TestScanOnceKeepsPendingOnFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{}}`))
		case "/api/session":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/api/session/ses_q/form":
			w.WriteHeader(500)
		case "/api/session/ses_q":
			_, _ = w.Write([]byte(`{"data":{"id":"ses_q","title":"Q","location":{"directory":"/repo"}}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	reg := registry.New()
	d := newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))

	d.mu.Lock()
	d.pendForm["ses_q"] = &pendingForm{formID: "frm_live"}
	d.mu.Unlock()
	d.upsert("ses_q", session.StatusAwaitingInput, &session.Interaction{
		Kind:      session.InteractionQuestion,
		Questions: []session.QuestionSpec{{Header: "H", Question: "Q?"}},
	})

	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	d.mu.Lock()
	_, stuck := d.pendForm["ses_q"]
	d.mu.Unlock()
	if !stuck {
		t.Fatal("a transient form-list error must not clear a pending form")
	}
}

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

// TestScanOnceStartsPumpWhenServiceDown verifies the live event pump starts on the
// first scan even when the service is unreachable then. The pump retries its own
// connection, so gating its start on a live dial would leave live updates dead
// until a later scan, which is why new prompts did not appear live.
func TestScanOnceStartsPumpWhenServiceDown(t *testing.T) {
	d := newTestDiscoverer(registry.New(), nil) // dial returns (nil, false)
	d.ctx = t.Context()                         // so the started pump goroutine exits with the test

	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if !d.pumpStarted.Load() {
		t.Fatal("pump must start on the first scan even when the service is unavailable")
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
