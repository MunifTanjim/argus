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
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// fakeBeacon is a scripted gate-extension peer: it dials the node socket,
// speaks hello/approvals, and records everything the node sends it.
type fakeBeacon struct {
	t    *testing.T
	conn net.Conn
	sc   *bufio.Scanner
	mu   sync.Mutex
	got  []map[string]any
}

func dialBeacon(t *testing.T, sock string) *fakeBeacon {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial beacon socket: %v", err)
	}
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

func newBeaconDiscoverer(t *testing.T, reg *registry.Registry) (*discoverer, string) {
	t.Helper()
	d := newDiscoverer(reg, nil)
	sock := filepath.Join(t.TempDir(), "argus-pi.sock")
	d.sockPath = func() string { return sock }
	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.blnMu.Lock()
		ln := d.bln
		d.blnMu.Unlock()
		if ln != nil {
			ln.Close()
		}
	})
	return d, sock
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
	d, sock := newBeaconDiscoverer(t, reg)
	fb := dialBeacon(t, sock)
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
	d, sock := newBeaconDiscoverer(t, reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", "")
	if err != nil {
		t.Fatal(err)
	}
	fb := dialBeacon(t, sock)
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
	d, sock := newBeaconDiscoverer(t, reg)
	fb := dialBeacon(t, sock)
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
	if err := d.respond(ctx, s, api.RespondParams{Behavior: "deny", RequestID: "a1"}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	dec := fb.waitFor(t, "approval_decision")
	if dec["id"] != "a1" || dec["decision"] != "deny" {
		t.Fatalf("decision = %v", dec)
	}
}

func TestBeaconPromptIntent(t *testing.T) {
	reg := registry.New()
	d, sock := newBeaconDiscoverer(t, reg)
	fb := dialBeacon(t, sock)
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
	_, sock := newBeaconDiscoverer(t, reg)
	fb := dialBeacon(t, sock)
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
	_, sock := newBeaconDiscoverer(t, reg)
	fb := dialBeacon(t, sock)
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
	d, sock := newBeaconDiscoverer(t, reg)
	fb := dialBeacon(t, sock)
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

func TestBeaconSocketTakeover(t *testing.T) {
	reg := registry.New()
	d, sock := newBeaconDiscoverer(t, reg)
	// A second discoverer must not steal a live socket.
	d2 := newDiscoverer(registry.New(), nil)
	d2.sockPath = func() string { return sock }
	if err := d2.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	d2.blnMu.Lock()
	bound := d2.bln != nil
	d2.blnMu.Unlock()
	if bound {
		t.Fatal("second discoverer bound an owned socket")
	}
	// ...but adopts a stale file.
	d.blnMu.Lock()
	ln := d.bln
	d.blnMu.Unlock()
	ln.Close()
	d.blnMu.Lock()
	d.bln = nil
	d.blnMu.Unlock()
	// Drop the stale file's listener without removing the file: simulate
	// staleness by closing; the path file lingers.
	if err := d2.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	d2.blnMu.Lock()
	bound = d2.bln != nil
	d2.blnMu.Unlock()
	if !bound {
		t.Fatal("second discoverer did not adopt stale socket")
	}
	d2.bln.Close()
}

func TestBeaconInstallCycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New()
	path, err := a.SettingsPath()
	if err != nil || path == "" {
		t.Fatalf("settings path = %q, %v", path, err)
	}
	if err := a.Install(""); err != nil {
		t.Fatalf("install: %v", err)
	}
	beacon := filepath.Join(home, "agent", "extensions", "argus.ts")
	data, err := os.ReadFile(beacon)
	if err != nil {
		t.Fatalf("beacon not written: %v", err)
	}
	if !strings.Contains(string(data), "ARGUS_MANAGED") {
		t.Fatal("beacon content unexpected")
	}
	added, err := a.ReconcileIfInstalled("")
	if err != nil || len(added) != 0 {
		t.Fatalf("reconcile clean = %v, %v", added, err)
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
