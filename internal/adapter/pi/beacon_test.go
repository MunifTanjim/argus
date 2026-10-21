package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// fakeBeacon is a scripted gate-extension peer: it speaks hello and dialogs
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
	return waitCardState(t, reg, id, func(s session.Session) bool { return want == "" || s.Status == want })
}

func waitCardState(t *testing.T, reg *registry.Registry, id string, ok func(session.Session) bool) session.Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range reg.Snapshot() {
			if s.AgentSessionID == id && ok(s) {
				return s
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("card for %s never matched: %+v", id, reg.Snapshot())
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
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
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

func TestBeaconSelectDialogRoundTrip(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-2", "cwd": "/w", "idle": false})
	waitCard(t, reg, "for-2", session.StatusWorking)

	fb.send(map[string]any{
		"type": "ui_request", "id": "u1", "method": "select", "title": "Allow rm?", "options": []string{"Yes", "No"},
	})
	s := waitCard(t, reg, "for-2", session.StatusAwaitingInput)
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionQuestion || s.Interaction.RequestID != "u1" {
		t.Fatalf("interaction = %+v, want the question u1", s.Interaction)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.respond(ctx, s, api.RespondParams{RequestID: "u1", Answers: map[string]any{"Allow rm?": "Yes"}}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	resp := fb.waitFor(t, "ui_response")
	if resp["id"] != "u1" || resp["value"] != "Yes" {
		t.Fatalf("response = %v, want value Yes for u1", resp)
	}
	s = waitCard(t, reg, "for-2", session.StatusWorking)
	if s.Interaction != nil {
		t.Fatalf("interaction = %+v, want none", s.Interaction)
	}
}

func TestBeaconConfirmDialogDenied(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-c", "cwd": "/w", "idle": false})
	waitCard(t, reg, "for-c", session.StatusWorking)

	fb.send(map[string]any{"type": "ui_request", "id": "u1", "method": "confirm", "title": "Delete?", "message": "rm -rf x"})
	s := waitCard(t, reg, "for-c", session.StatusAwaitingInput)
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionPermission {
		t.Fatalf("interaction = %+v, want a permission prompt", s.Interaction)
	}
	if err := d.respond(context.Background(), s, api.RespondParams{Behavior: "deny", RequestID: "u1"}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	resp := fb.waitFor(t, "ui_response")
	if resp["id"] != "u1" || resp["confirmed"] != false {
		t.Fatalf("response = %v, want confirmed false", resp)
	}
}

func TestBeaconDialogSettledInPi(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-s", "cwd": "/w", "idle": false})
	waitCard(t, reg, "for-s", session.StatusWorking)
	fb.send(map[string]any{"type": "ui_request", "id": "u1", "method": "confirm", "title": "Run?", "message": "ls"})
	waitCard(t, reg, "for-s", session.StatusAwaitingInput)

	fb.send(map[string]any{"type": "ui_settled", "id": "u1"})
	s := waitCard(t, reg, "for-s", session.StatusWorking)
	if s.Interaction != nil {
		t.Fatalf("interaction = %+v, want none", s.Interaction)
	}
	err := d.respond(context.Background(), s, api.RespondParams{Behavior: "allow", RequestID: "u1"})
	if err == nil {
		t.Fatal("a settled dialog must not be answered again")
	}
}

func TestBeaconEditorRequestIgnored(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-e", "cwd": "/w", "idle": false})
	waitCard(t, reg, "for-e", session.StatusWorking)

	fb.send(map[string]any{"type": "ui_request", "id": "u1", "method": "editor", "title": "Edit"})
	fb.send(map[string]any{"type": "ui_request", "id": "u2", "method": "select", "title": "Pick", "options": []string{"A", "B"}})
	s := waitCardState(t, reg, "for-e", func(s session.Session) bool { return s.InteractionPending() })
	if s.Interaction.RequestID != "u2" {
		t.Fatalf("interaction = %+v, want the select", s.Interaction)
	}
	fs := d.lookupForeign("for-e")
	fs.mu.Lock()
	ids := slices.Sorted(maps.Keys(fs.pending))
	fs.mu.Unlock()
	if !slices.Equal(ids, []string{"u2"}) {
		t.Fatalf("pending = %v, want only the select", ids)
	}
}

func TestGateRegistersNoToolCallHandler(t *testing.T) {
	if strings.Contains(gateSource, `"tool_call"`) || strings.Contains(gateSource, "APPROVAL_MARKER") {
		t.Fatal("the gate must add no approvals")
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

func TestBeaconCompactIntent(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-c", "cwd": "/w", "idle": true})
	waitCard(t, reg, "for-c", session.StatusAwaitingInput)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.sendPrompt(ctx, "for-c", " /compact\nkeep the tests"); err != nil {
		t.Fatal(err)
	}
	if intent := fb.waitFor(t, "intent_compact"); intent["instructions"] != "keep the tests" {
		t.Fatalf("intent = %v, want the instructions", intent)
	}
	for _, m := range fb.received() {
		if m["type"] == "intent_prompt" {
			t.Fatalf("intent_prompt = %v: pi keeps /compact out of prompts", m)
		}
	}

	fb.send(map[string]any{"type": "event", "name": "compaction", "open": true})
	waitCard(t, reg, "for-c", session.StatusWorking)
	fb.send(map[string]any{"type": "event", "name": "compaction", "open": false})
	waitCardState(t, reg, "for-c", isIdle)
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

func TestBeaconRenameIntent(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-r", "cwd": "/w", "idle": true})
	waitCard(t, reg, "for-r", session.StatusAwaitingInput)
	if err := d.rename(context.Background(), "for-r", "fix login"); err != nil {
		t.Fatal(err)
	}
	if intent := fb.waitFor(t, "intent_rename"); intent["name"] != "fix login" {
		t.Fatalf("intent = %v", intent)
	}
}

func TestBeaconReportsTheQueue(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-q", "sessionFile": "/s/for-q.jsonl", "cwd": "/w", "idle": false})
	waitCard(t, reg, "for-q", session.StatusWorking)
	fb.send(map[string]any{"type": "event", "name": "queue", "items": []string{"one", "two"}})
	waitQueue := func(want []string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if slices.Equal(d.queueFor("/s/for-q.jsonl"), want) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("queue = %v, want %v", d.queueFor("/s/for-q.jsonl"), want)
	}
	waitQueue([]string{"one", "two"})
	fb.send(map[string]any{"type": "event", "name": "queue", "items": []string{}})
	waitQueue(nil)
}

func TestBeaconShowsADialogWaitingInPi(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-d", "cwd": "/w", "idle": true})
	waitCard(t, reg, "for-d", session.StatusAwaitingInput)
	waitMessage := func(want string) session.Session {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if s := waitCard(t, reg, "for-d", ""); s.Interaction != nil && s.Interaction.Message == want {
				return s
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("card = %+v, want message %q", waitCard(t, reg, "for-d", ""), want)
		return session.Session{}
	}

	fb.send(map[string]any{"type": "event", "name": "ui_prompt", "open": true, "title": "Pick a branch"})
	s := waitMessage("Waiting in pi: Pick a branch")
	if s.Status != session.StatusAwaitingInput || s.InteractionPending() {
		t.Fatalf("card = %+v, want it waiting on you, with nothing to answer in Argus", s)
	}
	fb.send(map[string]any{"type": "event", "name": "agent_start"})
	fb.send(map[string]any{"type": "event", "name": "model", "model": "m1"})
	waitMessage("Waiting in pi: Pick a branch")

	fb.send(map[string]any{"type": "event", "name": "ui_prompt", "open": false, "title": "Pick a branch"})
	waitCard(t, reg, "for-d", session.StatusWorking)
}

func TestBeaconStagingNeedsAPane(t *testing.T) {
	for _, tc := range []struct {
		text string
		want error
	}{
		{"!ls", api.ErrShellNeedsPane},
		{"/new", errNewNeedsPane},
	} {
		t.Run(tc.text, func(t *testing.T) {
			reg := registry.New()
			d := newDiscoverer(reg, nil)
			fb := dialBeacon(t, d)
			fb.send(map[string]any{"type": "hello", "sessionId": "for-sh", "cwd": "/w", "idle": true})
			waitCard(t, reg, "for-sh", session.StatusAwaitingInput)
			if _, err := d.runShell(context.Background(), "for-sh", tc.text); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// shellBeacon is a foreign session in pane %7.
func shellBeacon(t *testing.T) (*discoverer, *fakeBeacon) {
	t.Helper()
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	d.paneFor = func(context.Context, int) (session.TmuxServer, string) { return session.TmuxServerArgus, "%7" }
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-sh", "cwd": "/w", "idle": true, "pid": 42})
	waitCard(t, reg, "for-sh", session.StatusAwaitingInput)
	return d, fb
}

type shellResult struct {
	submit bool
	err    error
}

func runShellAsync(d *discoverer, text string) <-chan shellResult {
	done := make(chan shellResult, 1)
	go func() {
		submit, err := d.runShell(context.Background(), "for-sh", text)
		done <- shellResult{submit, err}
	}()
	return done
}

func TestBeaconShellStagesForEnter(t *testing.T) {
	d, fb := shellBeacon(t)
	done := runShellAsync(d, " !!ls -la ")
	intent := fb.waitFor(t, "intent_shell")
	if intent["text"] != "!!ls -la" {
		t.Fatalf("intent = %v, want the trimmed command", intent)
	}
	select {
	case r := <-done:
		t.Fatalf("runShell = %+v before the gate staged the command", r)
	default:
	}
	fb.send(map[string]any{"type": "shell_ready", "id": intent["id"], "ok": true})
	if r := <-done; r.err != nil || !r.submit {
		t.Fatalf("runShell = %+v, want it staged for Enter", r)
	}
}

func TestBeaconNewStagesForEnter(t *testing.T) {
	d, fb := shellBeacon(t)
	done := runShellAsync(d, " /new ")
	intent := fb.waitFor(t, "intent_shell")
	if intent["text"] != "/new" {
		t.Fatalf("intent = %v, want /new staged", intent)
	}
	fb.send(map[string]any{"type": "shell_ready", "id": intent["id"], "ok": true})
	if r := <-done; r.err != nil || !r.submit {
		t.Fatalf("runShell = %+v, want it staged for Enter", r)
	}
}

func TestBeaconShellRefused(t *testing.T) {
	d, fb := shellBeacon(t)
	done := runShellAsync(d, "!ls")
	intent := fb.waitFor(t, "intent_shell")
	fb.send(map[string]any{"type": "shell_ready", "id": intent["id"], "ok": false, "reason": "the session has no editor"})
	if r := <-done; r.err == nil || !strings.Contains(r.err.Error(), "no editor") {
		t.Fatalf("err = %v, want the gate's reason", r.err)
	}
}

func TestBeaconShellOldGate(t *testing.T) {
	old := shellReadyTimeout
	shellReadyTimeout = 50 * time.Millisecond
	t.Cleanup(func() { shellReadyTimeout = old })
	d, _ := shellBeacon(t)
	if _, err := d.runShell(context.Background(), "for-sh", "!ls"); err == nil || !strings.Contains(err.Error(), "/reload") {
		t.Fatalf("err = %v, want a hint to reload the gate", err)
	}
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

func setSwitchGrace(t *testing.T, d time.Duration) {
	t.Helper()
	old := switchGrace
	switchGrace = d
	t.Cleanup(func() { switchGrace = old })
}

func TestBeaconSessionSwitchKeepsTheCard(t *testing.T) {
	setSwitchGrace(t, 200*time.Millisecond)
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	a := dialBeacon(t, d)
	a.send(map[string]any{"type": "hello", "sessionId": "s1", "cwd": "/w", "idle": true, "pid": 10})
	first := waitCard(t, reg, "s1", session.StatusAwaitingInput)
	_ = a.conn.Close()
	b := dialBeacon(t, d)
	b.send(map[string]any{"type": "hello", "sessionId": "s2", "cwd": "/w", "idle": true, "pid": 10, "previousSessionId": "s1"})
	s := waitCard(t, reg, "s2", session.StatusAwaitingInput)
	if s.ID != first.ID {
		t.Fatalf("card id = %q, want %q", s.ID, first.ID)
	}
	fs := d.lookupForeign("s2")
	fs.mu.Lock()
	prev := fs.prevID
	fs.mu.Unlock()
	if prev != "" {
		t.Fatalf("prevID = %q after the push that carried it", prev)
	}
	time.Sleep(2 * switchGrace)
	var pi []session.Session
	for _, s := range reg.Snapshot() {
		if s.Agent == Agent {
			pi = append(pi, s)
		}
	}
	if len(pi) != 1 || pi[0].ID != first.ID || pi[0].AgentSessionID != "s2" {
		t.Fatalf("pi cards = %+v, want one %s on s2", pi, first.ID)
	}
}

func TestBeaconDisconnectEndsTheCardAfterTheGrace(t *testing.T) {
	setSwitchGrace(t, 100*time.Millisecond)
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "s1", "cwd": "/w", "idle": true})
	waitCard(t, reg, "s1", session.StatusAwaitingInput)
	_ = fb.conn.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(reg.Snapshot()) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("card survived the grace: %+v", reg.Snapshot())
}

func TestBeaconRestartInThePaneWithinTheGraceTakesThePane(t *testing.T) {
	setSwitchGrace(t, time.Minute)
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	d.paneFor = func(context.Context, int) (session.TmuxServer, string) { return session.TmuxServerArgus, "%1" }
	a := dialBeacon(t, d)
	a.send(map[string]any{"type": "hello", "sessionId": "s1", "cwd": "/w", "idle": true, "pid": 10})
	waitCard(t, reg, "s1", session.StatusAwaitingInput)
	_ = a.conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		fs := d.lookupForeign("s1")
		fs.mu.Lock()
		gone := fs.disconnected
		fs.mu.Unlock()
		if gone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnect never seen")
		}
		time.Sleep(10 * time.Millisecond)
	}
	b := dialBeacon(t, d)
	b.send(map[string]any{"type": "hello", "sessionId": "s2", "cwd": "/w", "idle": true, "pid": 20})
	s := waitCard(t, reg, "s2", session.StatusAwaitingInput)
	if s.Tmux.PaneID != "%1" {
		t.Fatalf("restarted pi got pane %q, want %%1", s.Tmux.PaneID)
	}
	snapshotGone(t, reg, "s1")
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

func TestBeaconSecondHelloKeepsTheLiveEntry(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	a := dialBeacon(t, d)
	a.send(map[string]any{"type": "hello", "sessionId": "s1", "cwd": "/w", "idle": true})
	waitCard(t, reg, "s1", session.StatusAwaitingInput)
	b := dialBeacon(t, d)
	b.send(map[string]any{"type": "hello", "sessionId": "s1", "cwd": "/w", "idle": true})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := a.conn.Write([]byte("\n")); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old connection never closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if d.lookupForeign("s1") == nil {
		t.Fatal("old connection's cleanup dropped the live entry")
	}
	if _, ok := reg.Get("pi:s1"); !ok {
		t.Fatal("card dropped")
	}
	_ = b.conn.Close()
	waitGone(t, reg, "s1")
}

func TestBeaconConcurrentHellosClaimAPaneOnce(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	d.paneFor = func(context.Context, int) (session.TmuxServer, string) { return session.TmuxServerArgus, "%1" }
	a := dialBeacon(t, d)
	b := dialBeacon(t, d)
	var wg sync.WaitGroup
	for fb, n := range map[*fakeBeacon]int{a: 1, b: 2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fb.send(map[string]any{"type": "hello", "sessionId": "s" + string(rune('0'+n)), "cwd": "/w", "idle": true, "pid": n * 10})
		}()
	}
	wg.Wait()
	waitCard(t, reg, "s1", session.StatusAwaitingInput)
	waitCard(t, reg, "s2", session.StatusAwaitingInput)
	claimed := 0
	for _, id := range []string{"s1", "s2"} {
		fs := d.lookupForeign(id)
		if fs == nil {
			t.Fatalf("%s not tracked", id)
		}
		fs.mu.Lock()
		if fs.paneID == "%1" {
			claimed++
		}
		fs.mu.Unlock()
	}
	if claimed != 1 {
		t.Fatalf("%d sessions claimed the pane, want 1", claimed)
	}
}

func TestReconcileLeavesAUserGateAlone(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(t.TempDir(), "agent"))
	path, err := beaconPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "// my own argus.ts\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := New().Reconcile(adapter.InstallOptions{Socket: "/s"})
	if err != nil || len(paths) != 0 {
		t.Fatalf("reconcile = %v, %v", paths, err)
	}
	if got, _ := os.ReadFile(path); string(got) != mine {
		t.Fatalf("file changed: %q", got)
	}
}

func writeInstalledGate(t *testing.T, src string) string {
	t.Helper()
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(t.TempDir(), "agent"))
	path, err := beaconPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReconcileUpgradesAMarkedGateWithOlderContent(t *testing.T) {
	old := gateMarker + "\nconst SOCKET_PATH = \"/old.sock\";\n// older body\n"
	path := writeInstalledGate(t, old)
	paths, err := New().Reconcile(adapter.InstallOptions{Socket: "/new.sock"})
	if err != nil || len(paths) != 1 {
		t.Fatalf("reconcile = %v, %v", paths, err)
	}
	if got := mustRead(t, path); got != stampSocket("/new.sock") {
		t.Fatalf("gate not upgraded:\n%s", got)
	}
}

func TestUnmarkedGateIsLeftAlone(t *testing.T) {
	mine := "// my gate\nconst SOCKET_PATH = \"/s\";\n"
	path := writeInstalledGate(t, mine)
	paths, err := New().Reconcile(adapter.InstallOptions{Socket: "/new.sock"})
	if err != nil || len(paths) != 0 {
		t.Fatalf("reconcile = %v, %v", paths, err)
	}
	if err := New().Uninstall(); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != mine {
		t.Fatalf("file changed: %q", got)
	}
}

func TestBeaconStagingRefusedWhileADialogWaitsInPi(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	d.paneFor = func(context.Context, int) (session.TmuxServer, string) { return session.TmuxServerArgus, "%7" }
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "s1", "cwd": "/w", "idle": true, "pid": 42})
	waitCard(t, reg, "s1", session.StatusAwaitingInput)
	fb.send(map[string]any{"type": "event", "name": "ui_prompt", "open": true, "title": "Pick"})
	deadline := time.Now().Add(5 * time.Second)
	for s := waitCard(t, reg, "s1", ""); s.Interaction == nil || s.Interaction.Message != "Waiting in pi: Pick"; s = waitCard(t, reg, "s1", "") {
		if time.Now().After(deadline) {
			t.Fatalf("card = %+v, dialog never registered", s)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := d.runShell(context.Background(), "s1", "!ls"); !errors.Is(err, api.ErrSessionBusy) {
		t.Fatalf("err = %v, want ErrSessionBusy", err)
	}
	for _, m := range fb.received() {
		if m["type"] == "intent_shell" {
			t.Fatalf("intent_shell reached the beacon: %v", m)
		}
	}
}

func TestBeaconIdleDialogSettledInPiLeavesTheCardIdle(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-i1", "cwd": "/w", "idle": true})
	waitCard(t, reg, "for-i1", session.StatusAwaitingInput)
	fb.send(map[string]any{"type": "ui_request", "id": "u1", "method": "select", "title": "Pick", "options": []string{"A", "B"}})
	waitCardState(t, reg, "for-i1", func(s session.Session) bool { return s.InteractionPending() })

	fb.send(map[string]any{"type": "ui_settled", "id": "u1"})
	s := waitCardState(t, reg, "for-i1", func(s session.Session) bool {
		return !s.InteractionPending()
	})
	if s.Status != session.StatusAwaitingInput || s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("card = %+v, want idle", s)
	}
}

func TestBeaconIdleDialogAnsweredInArgusLeavesTheCardIdle(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-i2", "cwd": "/w", "idle": true})
	waitCard(t, reg, "for-i2", session.StatusAwaitingInput)
	fb.send(map[string]any{"type": "ui_request", "id": "u1", "method": "select", "title": "Pick", "options": []string{"A", "B"}})
	s := waitCardState(t, reg, "for-i2", func(s session.Session) bool { return s.InteractionPending() })

	if err := d.respond(context.Background(), s, api.RespondParams{RequestID: "u1", Answers: map[string]any{"Pick": "A"}}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	s = waitCardState(t, reg, "for-i2", func(s session.Session) bool { return !s.InteractionPending() })
	if s.Status != session.StatusAwaitingInput || s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("card = %+v, want idle", s)
	}
}

func TestBeaconStreamingDialogSettledInPiResumesWorking(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	fb := dialBeacon(t, d)
	fb.send(map[string]any{"type": "hello", "sessionId": "for-i3", "cwd": "/w", "idle": true})
	waitCard(t, reg, "for-i3", session.StatusAwaitingInput)
	fb.send(map[string]any{"type": "event", "name": "agent_start"})
	waitCard(t, reg, "for-i3", session.StatusWorking)
	fb.send(map[string]any{"type": "ui_request", "id": "u1", "method": "select", "title": "Pick", "options": []string{"A", "B"}})
	waitCard(t, reg, "for-i3", session.StatusAwaitingInput)

	fb.send(map[string]any{"type": "ui_settled", "id": "u1"})
	s := waitCard(t, reg, "for-i3", session.StatusWorking)
	if s.Interaction != nil {
		t.Fatalf("interaction = %+v, want none", s.Interaction)
	}
}
