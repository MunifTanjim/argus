package codex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// threadJSON is a minimal app-server Thread.
func threadJSON(id, status string) map[string]any {
	return map[string]any{
		"id": id, "cwd": "/w/" + id, "path": "/r/rollout-" + id + ".jsonl",
		"name": nil, "preview": "task " + id, "model": "gpt-6-luna",
		"ephemeral": false, "parentThreadId": nil, "status": map[string]any{"type": status},
	}
}

// daemonState scripts the fake daemon's thread-level responses.
type daemonState struct {
	mu        sync.Mutex
	loaded    []string
	threads   map[string]map[string]any
	noRollout map[string]bool   // thread/resume fails for these
	turn      map[string]string // thread id -> in-progress turn id, until interrupted
	items     map[string][]any  // thread id -> thread/items/list data
}

func (s *daemonState) handle(method string, params json.RawMessage) (any, *rpcError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := requestThreadID(params)
	switch method {
	case "thread/loaded/list":
		return map[string]any{"data": s.loaded, "nextCursor": nil}, nil
	case "thread/resume":
		if s.noRollout[id] {
			return nil, &rpcError{Code: -32600, Message: "no rollout found for thread id " + id}
		}
		return map[string]any{"thread": s.threads[id]}, nil
	case "thread/read":
		return map[string]any{"thread": s.threads[id]}, nil
	case "thread/turns/list":
		if t := s.turn[id]; t != "" {
			return map[string]any{"data": []map[string]any{{"id": t, "status": "inProgress", "items": []any{}}}}, nil
		}
		return map[string]any{"data": []any{}}, nil
	case "turn/interrupt":
		delete(s.turn, id)
	case "thread/items/list":
		return map[string]any{"data": s.items[id]}, nil
	}
	return nil, nil
}

func startDiscoverer(t *testing.T, st *daemonState) (*discoverer, *fakeDaemon, *registry.Registry) {
	t.Helper()
	f := newFakeDaemon(t, st.handle)
	reg := registry.New()
	t.Setenv("CODEX_HOME", t.TempDir())
	d := newDiscoverer(reg, nil)
	d.sockPath = func() (string, error) { return f.sock, nil }
	d.retry = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d.ctx = ctx
	if err := d.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	return d, f, reg
}

func waitSession(t *testing.T, reg *registry.Registry, id string, ok func(session.Session) bool) session.Session {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s, found := reg.Get(registry.AgentSessionKey(Agent, id)); found && ok(s) {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, found := reg.Get(registry.AgentSessionKey(Agent, id))
	t.Fatalf("session %s never matched; found=%v last=%+v", id, found, s)
	return session.Session{}
}

func waitGone(t *testing.T, reg *registry.Registry, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, found := reg.Get(registry.AgentSessionKey(Agent, id)); !found {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %s still present", id)
}

func TestLoadedThreadsTrackedAndSubscribed(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "idle")}}
	_, f, reg := startDiscoverer(t, st)

	m := f.expect(t, "thread/resume")
	if requestThreadID(m.Params) != "t1" {
		t.Fatalf("resume params = %s", m.Params)
	}
	s := waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusAwaitingInput })
	if s.Input != session.InputAPI || s.Frontend != session.FrontendExternal {
		t.Errorf("input/frontend = %v/%v", s.Input, s.Frontend)
	}
	if s.TranscriptPath != "/r/rollout-t1.jsonl" || s.Cwd != "/w/t1" || s.Name != "task t1" {
		t.Errorf("fields = %+v", s)
	}
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Errorf("idle thread should show the idle composer, got %+v", s.Interaction)
	}
}

func TestStatusChangesDriveSession(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "idle")}}
	_, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusAwaitingInput })

	f.push("thread/status/changed", map[string]any{"threadId": "t1", "status": map[string]any{"type": "active", "activeFlags": []string{}}})
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking && s.Interaction == nil })

	f.push("thread/status/changed", map[string]any{"threadId": "t1", "status": map[string]any{"type": "idle"}})
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusAwaitingInput })

	f.push("thread/name/updated", map[string]any{"threadId": "t1", "threadName": "Run touch command"})
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Name == "Run touch command" })

	f.push("thread/closed", map[string]any{"threadId": "t1"})
	waitGone(t, reg, "t1")
}

func TestNewThreadStartedIsTracked(t *testing.T) {
	st := &daemonState{threads: map[string]map[string]any{}}
	_, f, reg := startDiscoverer(t, st)
	f.waitConns(t, 1)
	f.expect(t, "thread/loaded/list")
	f.push("thread/started", map[string]any{"thread": threadJSON("t2", "idle")})
	waitSession(t, reg, "t2", func(s session.Session) bool { return s.Status == session.StatusAwaitingInput })
}

func TestTrackFallsBackToReadThenSubscribesOnActive(t *testing.T) {
	st := &daemonState{
		loaded:    []string{"t3"},
		threads:   map[string]map[string]any{"t3": threadJSON("t3", "idle")},
		noRollout: map[string]bool{"t3": true},
	}
	_, f, reg := startDiscoverer(t, st)
	f.expect(t, "thread/resume")
	f.expect(t, "thread/read")
	waitSession(t, reg, "t3", func(s session.Session) bool { return s.Status == session.StatusAwaitingInput })

	// The first turn writes the rollout; the next active status must re-subscribe.
	st.mu.Lock()
	st.noRollout = nil
	st.threads["t3"] = threadJSON("t3", "active")
	st.mu.Unlock()
	f.push("thread/status/changed", map[string]any{"threadId": "t3", "status": map[string]any{"type": "active", "activeFlags": []string{}}})
	if m := f.expect(t, "thread/resume"); requestThreadID(m.Params) != "t3" {
		t.Fatalf("resubscribe params = %s", m.Params)
	}
	waitSession(t, reg, "t3", func(s session.Session) bool { return s.Status == session.StatusWorking })
}

func TestEphemeralThreadIgnored(t *testing.T) {
	eph := map[string]any{"id": "e1", "cwd": "/w", "path": nil, "ephemeral": true, "status": map[string]any{"type": "idle"}}
	st := &daemonState{threads: map[string]map[string]any{"e1": eph}}
	d, f, reg := startDiscoverer(t, st)
	f.waitConns(t, 1)
	f.expect(t, "thread/loaded/list")
	f.push("thread/started", map[string]any{"thread": eph})
	f.push("thread/status/changed", map[string]any{"threadId": "e1", "status": map[string]any{"type": "active", "activeFlags": []string{}}})
	time.Sleep(100 * time.Millisecond)
	if _, found := reg.Get(registry.AgentSessionKey(Agent, "e1")); found {
		t.Fatal("ephemeral thread must not create a session")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.ignored["e1"] {
		t.Fatal("ephemeral thread should be remembered as ignored")
	}
}

func TestPumpDropsSessionsOnDisconnect(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "idle")}}
	_, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(session.Session) bool { return true })

	st.mu.Lock()
	st.loaded = nil
	st.mu.Unlock()
	f.closeConns()
	waitGone(t, reg, "t1")

	// The pump reconnects and re-tracks once the daemon holds the thread again.
	st.mu.Lock()
	st.loaded = []string{"t1"}
	st.mu.Unlock()
	f.closeConns() // force another reconnect, which picks up the new loaded list
	waitSession(t, reg, "t1", func(session.Session) bool { return true })
}

func TestSubagentThreadDoesNotCreateCard(t *testing.T) {
	child := threadJSON("c1", "active")
	child["parentThreadId"] = "t1"
	st := &daemonState{
		loaded:  []string{"t1", "c1"},
		threads: map[string]map[string]any{"t1": threadJSON("t1", "idle"), "c1": child},
	}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(session.Session) bool { return true })
	f.push("thread/status/changed", map[string]any{"threadId": "c1", "status": map[string]any{"type": "idle"}})
	time.Sleep(100 * time.Millisecond)
	if _, found := reg.Get(registry.AgentSessionKey(Agent, "c1")); found {
		t.Fatal("subagent thread must not create its own session")
	}
	assertRootIdle := func() {
		t.Helper()
		s, _ := reg.Get(registry.AgentSessionKey(Agent, "t1"))
		if s.Status != session.StatusAwaitingInput || s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
			t.Fatalf("root card changed by child status: %+v", s)
		}
	}
	assertRootIdle()
	f.push("thread/status/changed", map[string]any{"threadId": "c1", "status": map[string]any{"type": "active", "activeFlags": []string{}}})
	time.Sleep(100 * time.Millisecond)
	assertRootIdle()
	d.mu.Lock()
	got := d.rootOfLocked("c1")
	d.mu.Unlock()
	if got != "t1" {
		t.Fatalf("rootOf(c1) = %q; want t1", got)
	}
}

func TestSendPromptStartsTurn(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "idle")}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(session.Session) bool { return true })
	if err := d.sendPrompt(context.Background(), "t1", "hello"); err != nil {
		t.Fatal(err)
	}
	m := f.expect(t, "turn/start")
	if string(m.Params) != `{"input":[{"text":"hello","type":"text"}],"threadId":"t1"}` {
		t.Fatalf("turn/start params = %s", m.Params)
	}
}

func TestSpawnSessionStartsThreadAndTurn(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	st := &daemonState{threads: map[string]map[string]any{}}
	f := newFakeDaemon(t, func(method string, params json.RawMessage) (any, *rpcError) {
		if method == "thread/start" {
			return map[string]any{"thread": threadJSON("n1", "idle")}, nil
		}
		return st.handle(method, params)
	})
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	d.sockPath = func() (string, error) { return f.sock, nil }
	d.retry = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.ctx = ctx
	_ = d.ScanOnce(ctx)
	f.waitConns(t, 1)

	id, err := d.spawnSession(ctx, "/w/n1", "do it")
	if err != nil || id != "n1" {
		t.Fatalf("spawn = %q, %v", id, err)
	}
	if m := f.expect(t, "thread/start"); string(m.Params) != `{"cwd":"/w/n1"}` {
		t.Fatalf("thread/start params = %s", m.Params)
	}
	f.expect(t, "turn/start")
	waitSession(t, reg, "n1", func(s session.Session) bool { return s.Status == session.StatusWorking })
}

func TestSpawnStartsDaemonWhenDown(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	// The pump goroutine reads the path while startDaemon swaps it in.
	var sock atomic.Value
	sock.Store(filepath.Join(t.TempDir(), "missing.sock"))
	d.sockPath = func() (string, error) { return sock.Load().(string), nil }
	d.retry = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.ctx = ctx
	_ = d.ScanOnce(ctx)

	started := false
	old := startDaemon
	t.Cleanup(func() { startDaemon = old })
	startDaemon = func(context.Context) error {
		started = true
		f := newFakeDaemon(t, func(method string, _ json.RawMessage) (any, *rpcError) {
			switch method {
			case "thread/start":
				return map[string]any{"thread": threadJSON("n2", "idle")}, nil
			case "thread/loaded/list":
				return map[string]any{"data": []string{}}, nil
			}
			return nil, nil
		})
		sock.Store(f.sock)
		return nil
	}
	if _, err := d.spawnSession(ctx, "/w/n2", ""); err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("spawn must start the daemon when no connection exists")
	}
}

func TestDismissInterruptsUnsubscribesAndRemoves(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(session.Session) bool { return true })
	f.push("turn/started", map[string]any{"threadId": "t1", "turn": map[string]any{"id": "u9"}})
	time.Sleep(50 * time.Millisecond)

	d.dismiss(context.Background(), "t1")
	if m := f.expect(t, "turn/interrupt"); string(m.Params) != `{"threadId":"t1","turnId":"u9"}` {
		t.Fatalf("interrupt params = %s", m.Params)
	}
	f.expect(t, "thread/unsubscribe")
	waitGone(t, reg, "t1")
}

func TestSweepIdleRemovesStaleSessions(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "idle")}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(session.Session) bool { return true })
	d.mu.Lock()
	d.threads["t1"].lastActivity = time.Now().Add(-2 * sessionIdleTTL)
	d.mu.Unlock()
	d.sweepIdle(context.Background())
	f.expect(t, "thread/unsubscribe")
	waitGone(t, reg, "t1")
}

func TestDismissedSessionStaysGone(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.push("turn/started", map[string]any{"threadId": "t1", "turn": map[string]any{"id": "u1"}})
	time.Sleep(50 * time.Millisecond)

	d.dismiss(context.Background(), "t1")
	f.expect(t, "thread/unsubscribe")
	waitGone(t, reg, "t1")

	// The interrupted turn ends after the dismiss and the daemon reports idle.
	st.mu.Lock()
	st.threads["t1"] = threadJSON("t1", "idle")
	st.mu.Unlock()
	f.push("turn/completed", map[string]any{"threadId": "t1", "turn": map[string]any{"id": "u1"}})
	f.push("thread/status/changed", map[string]any{"threadId": "t1", "status": map[string]any{"type": "idle"}})
	time.Sleep(200 * time.Millisecond)
	if s, found := reg.Get(registry.AgentSessionKey(Agent, "t1")); found {
		t.Fatalf("dismissed session came back: %+v", s)
	}
}

// Interrupting a waiting turn reports it active again before it ends; that must
// not bring the dismissed session back.
func TestDismissedWaitingSessionStaysGone(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.push("turn/started", map[string]any{"threadId": "t1", "turn": map[string]any{"id": "u1"}})
	f.request(1, "item/commandExecution/requestApproval", approvalParams("t1"))
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusAwaitingInput })

	d.dismiss(context.Background(), "t1")
	f.expect(t, "thread/unsubscribe")
	waitGone(t, reg, "t1")

	f.push("thread/status/changed", map[string]any{"threadId": "t1", "status": map[string]any{"type": "active", "activeFlags": []string{}}})
	time.Sleep(200 * time.Millisecond)
	if s, found := reg.Get(registry.AgentSessionKey(Agent, "t1")); found {
		t.Fatalf("dismissed session came back: %+v", s)
	}
}

func TestChildUnsubscribedWhenIdle(t *testing.T) {
	child := threadJSON("c1", "active")
	child["parentThreadId"] = "t1"
	st := &daemonState{
		loaded:  []string{"t1", "c1"},
		threads: map[string]map[string]any{"t1": threadJSON("t1", "active"), "c1": child},
	}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.expect(t, "thread/resume")
	f.expect(t, "thread/resume")

	f.push("thread/status/changed", map[string]any{"threadId": "c1", "status": map[string]any{"type": "idle"}})
	if m := f.expect(t, "thread/unsubscribe"); requestThreadID(m.Params) != "c1" {
		t.Fatalf("unsubscribe params = %s", m.Params)
	}
	time.Sleep(50 * time.Millisecond)
	d.mu.Lock()
	subscribed, parent := d.subs["c1"], d.parentOf["c1"]
	d.mu.Unlock()
	if subscribed || parent != "t1" {
		t.Fatalf("after idle: subs[c1]=%v parentOf[c1]=%q; want false, t1", subscribed, parent)
	}
}

func TestDismissUnsubscribesChildren(t *testing.T) {
	child := threadJSON("c1", "active")
	child["parentThreadId"] = "t1"
	st := &daemonState{
		loaded:  []string{"t1", "c1"},
		threads: map[string]map[string]any{"t1": threadJSON("t1", "idle"), "c1": child},
	}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(session.Session) bool { return true })
	f.expect(t, "thread/resume")
	f.expect(t, "thread/resume")

	d.dismiss(context.Background(), "t1")
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		seen[requestThreadID(f.expect(t, "thread/unsubscribe").Params)] = true
	}
	if !seen["t1"] || !seen["c1"] {
		t.Fatalf("unsubscribed %v; want t1 and c1", seen)
	}
	d.mu.Lock()
	_, isChild := d.parentOf["c1"]
	d.mu.Unlock()
	if isChild {
		t.Fatal("dismiss must drop the child's parentOf entry")
	}
}

func TestDismissInterruptsTurnStartedBeforeConnect(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}, turn: map[string]string{"t1": "u5"}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })

	d.dismiss(context.Background(), "t1")
	if m := f.expect(t, "turn/interrupt"); string(m.Params) != `{"threadId":"t1","turnId":"u5"}` {
		t.Fatalf("interrupt params = %s", m.Params)
	}
	waitGone(t, reg, "t1")
}

func TestSweepIdleKeepsActiveThread(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	d, _, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	d.mu.Lock()
	d.threads["t1"].lastActivity = time.Now().Add(-2 * sessionIdleTTL)
	d.mu.Unlock()
	d.sweepIdle(context.Background())
	if _, found := reg.Get(registry.AgentSessionKey(Agent, "t1")); !found {
		t.Fatal("sweep removed a thread whose turn is running")
	}
}
