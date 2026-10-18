package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// fakeBeacon is a scripted gate-extension peer: it speaks hello/approvals
// and records everything the node sends it.
type fakeBeacon struct {
	t    *testing.T
	conn net.Conn
	sc   *bufio.Scanner
	mu   sync.Mutex
	got  []map[string]any
}

func dialBeacon(t *testing.T, d *discoverer) *fakeBeacon {
	t.Helper()
	conn, peer := net.Pipe()
	go d.serveBeacon(peer)
	fb := &fakeBeacon{t: t, conn: conn, sc: bufio.NewScanner(conn)}
	fb.sc.Buffer(make([]byte, 64*1024), 1024*1024)
	t.Cleanup(func() { conn.Close() })
	go fb.readLoop()
	return fb
}

func (f *fakeBeacon) readLoop() {
	for f.sc.Scan() {
		var m map[string]any
		if json.Unmarshal(f.sc.Bytes(), &m) != nil {
			continue
		}
		f.mu.Lock()
		f.got = append(f.got, m)
		f.mu.Unlock()
	}
}

func (f *fakeBeacon) send(v any) {
	f.t.Helper()
	line, err := json.Marshal(v)
	if err != nil {
		f.t.Fatal(err)
	}
	line = append(line, '\n')
	if _, err := f.conn.Write(line); err != nil {
		f.t.Fatalf("beacon write: %v", err)
	}
}

func (f *fakeBeacon) received() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.got...)
}

func (f *fakeBeacon) waitFor(t *testing.T, typ string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range f.received() {
			if m["type"] == typ {
				return m
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never received %q; got %v", typ, f.received())
	return nil
}

func waitCard(t *testing.T, reg *registry.Registry, id string, want session.Status) session.Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range reg.Snapshot() {
			if s.AgentSessionID == id && (want == "" || s.Status == want) {
				return s
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("card %s status %v never appeared", id, want)
	return session.Session{}
}

func TestBeaconHelloTracksForeign(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{
		"type": "hello", "sessionId": "for-1", "cwd": "/work/f",
		"sessionFile": "/work/f/s.jsonl", "model": "m", "idle": true, "pid": 42,
	})
	ack := fb.waitFor(t, "hello_ack")
	if ack["passive"] == true {
		t.Fatalf("ack = %v, want passive false", ack)
	}
	s := waitCard(t, reg, "for-1", session.StatusAwaitingInput)
	if s.Agent != Agent || s.Cwd != "/work/f" || s.TranscriptPath != "/work/f/s.jsonl" {
		t.Fatalf("card = %+v", s)
	}
	if s.Input != session.InputAPI {
		t.Fatalf("input = %v, want API (prompt intents)", s.Input)
	}
	_ = d
}

func TestBeaconHelloOwnedIsPassive(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", "")
	if err != nil {
		t.Fatal(err)
	}
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": id, "cwd": "/w"})
	ack := fb.waitFor(t, "hello_ack")
	if ack["passive"] != true {
		t.Fatalf("ack = %v, want passive true", ack)
	}
	// No duplicate card: exactly one session with this id, owned by the proc.
	n := 0
	for _, s := range reg.Snapshot() {
		if s.AgentSessionID == id {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("cards for %s = %d", id, n)
	}
	_ = f
}

func TestBeaconApprovalRoundTrip(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-2", "cwd": "/w", "idle": false})
	waitCard(t, reg, "for-2", session.StatusWorking)

	fb.send(map[string]any{
		"type": "approval_request", "id": "a1", "toolName": "bash", "detail": "rm -rf /",
	})
	s := waitCard(t, reg, "for-2", session.StatusAwaitingInput)
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionPermission {
		t.Fatalf("interaction = %+v", s.Interaction)
	}
	if s.Interaction.RequestID != "a1" {
		t.Fatalf("request id = %q", s.Interaction.RequestID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.respond(ctx, s, api.RespondParams{Behavior: "deny", Reason: "use git clean", RequestID: "a1"}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	dec := fb.waitFor(t, "approval_decision")
	if dec["id"] != "a1" || dec["decision"] != "deny" || dec["reason"] != "use git clean" {
		t.Fatalf("decision = %v, want a deny carrying the reason", dec)
	}
}

func TestBeaconApprovalSettledLocally(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-s", "cwd": "/w", "idle": false})
	waitCard(t, reg, "for-s", session.StatusWorking)
	fb.send(map[string]any{"type": "approval_request", "id": "a1", "toolName": "bash", "detail": "ls"})
	waitCard(t, reg, "for-s", session.StatusAwaitingInput)

	fb.send(map[string]any{"type": "approval_settled", "id": "a1"})
	s := waitCard(t, reg, "for-s", session.StatusWorking)
	if s.Interaction != nil {
		t.Fatalf("interaction = %+v, want none", s.Interaction)
	}
	err := d.respond(context.Background(), s, api.RespondParams{Behavior: "allow", RequestID: "a1"})
	if err == nil {
		t.Fatal("a settled approval must not be answered again")
	}
}

func TestBeaconPromptIntent(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-3", "cwd": "/w", "idle": true})
	waitCard(t, reg, "for-3", session.StatusAwaitingInput)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess := waitCard(t, reg, "for-3", "")
	if err := d.sendPrompt(ctx, sess.AgentSessionID, "hello there"); err != nil {
		t.Fatalf("sendPrompt: %v", err)
	}
	intent := fb.waitFor(t, "intent_prompt")
	if intent["text"] != "hello there" {
		t.Fatalf("intent = %v", intent)
	}
}

func TestBeaconLifecycleEvents(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-4", "cwd": "/w", "idle": true})
	waitCard(t, reg, "for-4", session.StatusAwaitingInput)

	fb.send(map[string]any{"type": "event", "name": "agent_start"})
	waitCard(t, reg, "for-4", session.StatusWorking)
	fb.send(map[string]any{"type": "event", "name": "agent_settled"})
	s := waitCard(t, reg, "for-4", session.StatusAwaitingInput)
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("interaction = %+v", s.Interaction)
	}
	fb.send(map[string]any{"type": "event", "name": "session_info_changed", "displayName": "Renamed"})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range reg.Snapshot() {
			if s.AgentSessionID == "for-4" && s.Name == "Renamed" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("rename never applied")
}

func TestBeaconDisconnectDropsCard(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-5", "cwd": "/w", "idle": true})
	waitCard(t, reg, "for-5", session.StatusAwaitingInput)
	_ = fb.conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		gone := true
		for _, s := range reg.Snapshot() {
			if s.AgentSessionID == "for-5" {
				gone = false
			}
		}
		if gone {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("card survived disconnect")
}

func TestBeaconDismissUntracksOnly(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-6", "cwd": "/w", "idle": true})
	waitCard(t, reg, "for-6", session.StatusAwaitingInput)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.dismiss(ctx, "for-6"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	for _, s := range reg.Snapshot() {
		if s.AgentSessionID == "for-6" {
			t.Fatalf("card still listed: %+v", s)
		}
	}
	// The beacon connection itself is untouched: the user's process lives on.
	fb.send(map[string]any{"type": "heartbeat"})
	select {
	case <-time.After(100 * time.Millisecond):
	default:
	}
	if d.lookupForeign("for-6") != nil {
		t.Fatal("foreign still tracked")
	}
}

func TestBeaconInstallCycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	if err := os.MkdirAll(filepath.Join(home, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New()
	path, err := a.SettingsPath()
	if err != nil || path == "" {
		t.Fatalf("settings path = %q, %v", path, err)
	}
	beacon := filepath.Join(home, "agent", "extensions", "argus.ts")
	// Start reconciles every agent, so a never-installed beacon gets installed.
	sock := "/run/argus/argus.mod.sock"
	added, err := a.Reconcile(adapter.InstallOptions{Socket: sock})
	if err != nil || len(added) != 1 || added[0] != beacon {
		t.Fatalf("reconcile fresh = %v, %v", added, err)
	}
	if err := a.Install(adapter.InstallOptions{Socket: sock}); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, err := os.ReadFile(beacon)
	if err != nil {
		t.Fatalf("beacon not written: %v", err)
	}
	if !strings.Contains(string(data), `const SOCKET_PATH = "/run/argus/argus.mod.sock";`) {
		t.Fatal("beacon carries no socket path")
	}
	// An empty socket keeps the installed one.
	added, err = a.Reconcile(adapter.InstallOptions{})
	if err != nil || len(added) != 0 {
		t.Fatalf("reconcile clean = %v, %v", added, err)
	}
	added, err = a.Reconcile(adapter.InstallOptions{Socket: "/other/argus.mod.sock"})
	if err != nil || len(added) != 1 {
		t.Fatalf("reconcile moved socket = %v, %v", added, err)
	}
	if got, ours := stampedSocket(mustRead(t, beacon)); !ours || got != "/other/argus.mod.sock" {
		t.Fatalf("stamped socket = %q, %v", got, ours)
	}
	if err := a.Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(beacon); !os.IsNotExist(err) {
		t.Fatal("beacon not removed")
	}
	// Uninstall never touches a user-crafted file.
	if err := os.WriteFile(beacon, []byte("// mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(beacon); err != nil {
		t.Fatal("user file removed")
	}
}

func TestBeaconReportsModel(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{
		"type": "hello", "sessionId": "for-m", "cwd": "/w", "idle": true,
		"model": "m1", "reasoning": true, "thinkingLevel": "low",
	})
	s := waitCard(t, reg, "for-m", session.StatusAwaitingInput)
	if s.Summary == nil || s.Summary.ModelName != "m1" || s.Summary.Effort != "low" {
		t.Fatalf("summary = %+v", s.Summary)
	}
	fb.send(map[string]any{"type": "event", "name": "model", "model": "m2", "reasoning": false, "thinkingLevel": "off"})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := waitCard(t, reg, "for-m", ""); s.Summary.ModelName == "m2" && s.Summary.Effort == "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("model event not applied")
}

func TestForeignSessionGetsNoResumePane(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-p", "cwd": "/w", "idle": true})
	waitCard(t, reg, "for-p", session.StatusAwaitingInput)
	if _, _, ok := (&piAdapter{disc: d}).ResumeCommand("for-p"); ok {
		t.Fatal("a foreign live session must not get a second pi process")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGateSourceSocketUnset(t *testing.T) {
	if strings.Count(gateSource, socketLine) != 1 {
		t.Fatalf("gate source must hold %q once", socketLine)
	}
	if got, ours := stampedSocket(gateSource); !ours || got != "" {
		t.Fatalf("stamped socket = %q, %v", got, ours)
	}
}

func waitGone(t *testing.T, reg *registry.Registry, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		gone := true
		for _, s := range reg.Snapshot() {
			if s.AgentSessionID == id {
				gone = false
			}
		}
		if gone {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("card %s never went away", id)
}

func TestBeaconHelloBindsPane(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	var gotPID int
	d.paneFor = func(_ context.Context, pid int) (session.TmuxServer, string) {
		gotPID = pid
		return session.TmuxServerArgus, "%7"
	}
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-t", "cwd": "/w", "idle": true, "pid": 42})
	s := waitCard(t, reg, "for-t", session.StatusAwaitingInput)
	if gotPID != 42 {
		t.Fatalf("pane lookup pid = %d, want 42", gotPID)
	}
	if s.ID != "pi:for-t" || s.Tmux.Server != session.TmuxServerArgus || s.Tmux.PaneID != "%7" {
		t.Fatalf("session = id %q server %q pane %q", s.ID, s.Tmux.Server, s.Tmux.PaneID)
	}
	if s.Input != session.InputAPI || !s.Controllable() {
		t.Fatalf("input = %q, controllable = %v", s.Input, s.Controllable())
	}
}

func TestBeaconHelloOutsideTmuxHasNoPane(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	d.paneFor = func(context.Context, int) (session.TmuxServer, string) { return "", "" }
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-o", "cwd": "/w", "idle": true, "pid": 42})
	s := waitCard(t, reg, "for-o", session.StatusAwaitingInput)
	if s.Tmux.PaneID != "" || s.Frontend != session.FrontendExternal {
		t.Fatalf("pane = %q, frontend = %q", s.Tmux.PaneID, s.Frontend)
	}
}

func TestBeaconDisconnectClearsPane(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	d.paneFor = func(context.Context, int) (session.TmuxServer, string) { return session.TmuxServerArgus, "%7" }
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-u", "cwd": "/w", "idle": true, "pid": 42})
	waitCard(t, reg, "for-u", session.StatusAwaitingInput)
	_ = fb.conn.Close()
	waitGone(t, reg, "for-u")

	fb2 := dialBeacon(t, d)
	fb2.send(map[string]any{"type": "hello", "sessionId": "for-v", "cwd": "/w", "idle": true, "pid": 43})
	s := waitCard(t, reg, "for-v", session.StatusAwaitingInput)
	if s.ID != "pi:for-v" || s.Tmux.PaneID != "%7" {
		t.Fatalf("session = id %q pane %q", s.ID, s.Tmux.PaneID)
	}
	for _, other := range reg.Snapshot() {
		if other.AgentSessionID == "for-u" {
			t.Fatal("old pi record still present")
		}
	}
}

func TestBeaconPaneHeldByClaudeGetsNoPane(t *testing.T) {
	reg := registry.New()
	reg.ApplyHook(registry.HookUpdate{Agent: "claude", AgentSessionID: "c1", Server: session.TmuxServerArgus, PaneID: "%7", Status: session.StatusWorking})
	d := newDiscoverer(reg, nil)
	d.paneFor = func(context.Context, int) (session.TmuxServer, string) { return session.TmuxServerArgus, "%7" }
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-c", "cwd": "/w", "idle": true, "pid": 50})
	s := waitCard(t, reg, "for-c", session.StatusAwaitingInput)
	if s.Tmux.PaneID != "" {
		t.Fatalf("pi child took pane %q", s.Tmux.PaneID)
	}
	c, ok := reg.Get("argus:%7")
	if !ok || c.AgentSessionID != "c1" || c.Input == session.InputAPI {
		t.Fatalf("claude record = %+v, %v", c, ok)
	}
}

func TestBeaconPiChildKeepsParentCard(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	d.paneFor = func(context.Context, int) (session.TmuxServer, string) { return session.TmuxServerArgus, "%7" }
	parent := dialBeacon(t, d)
	parent.send(map[string]any{"type": "hello", "sessionId": "for-p", "cwd": "/w", "idle": true, "pid": 42})
	waitCard(t, reg, "for-p", session.StatusAwaitingInput)
	child := dialBeacon(t, d)
	child.send(map[string]any{"type": "hello", "sessionId": "for-k", "cwd": "/w", "idle": false, "pid": 43})
	k := waitCard(t, reg, "for-k", session.StatusWorking)
	if k.Tmux.PaneID != "" {
		t.Fatalf("child took pane %q", k.Tmux.PaneID)
	}
	_ = child.conn.Close()
	waitGone(t, reg, "for-k")
	p, ok := reg.Get("pi:for-p")
	if !ok || p.Tmux.PaneID != "%7" {
		t.Fatalf("parent card = %+v, %v", p, ok)
	}
}

func TestBeaconSessionSwitchKeepsPane(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	d.paneFor = func(context.Context, int) (session.TmuxServer, string) { return session.TmuxServerArgus, "%7" }
	old := dialBeacon(t, d)
	old.send(map[string]any{"type": "hello", "sessionId": "for-a", "cwd": "/w", "idle": true, "pid": 42})
	waitCard(t, reg, "for-a", session.StatusAwaitingInput)
	next := dialBeacon(t, d)
	next.send(map[string]any{"type": "hello", "sessionId": "for-b", "cwd": "/w", "idle": true, "pid": 42})
	s := waitCard(t, reg, "for-b", session.StatusAwaitingInput)
	if s.ID != "pi:for-b" || s.Tmux.PaneID != "%7" {
		t.Fatalf("session = id %q pane %q", s.ID, s.Tmux.PaneID)
	}
	if _, ok := reg.Get("pi:for-a"); ok {
		t.Fatal("old session still listed")
	}
}

func TestBeaconPaneLookupHasDeadline(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	hasDeadline := make(chan bool, 1)
	d.paneFor = func(ctx context.Context, _ int) (session.TmuxServer, string) {
		_, ok := ctx.Deadline()
		hasDeadline <- ok
		return "", ""
	}
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-d", "cwd": "/w", "idle": true, "pid": 42})
	if !<-hasDeadline {
		t.Fatal("pane lookup runs without a deadline")
	}
}
