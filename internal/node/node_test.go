package node

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// TestCaptureAndInputEndToEnd spawns a real interactive shell pane on an
// isolated tmux server, registers it via a hook, then exercises the capture and
// input API methods through a real client/node over a unix socket.
func TestCaptureAndInputEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmuxClient := tmux.New("argus-node-test")
	t.Cleanup(func() { _ = tmuxClient.KillServer(context.Background()) })

	// An interactive shell pane we can type into.
	paneID, err := tmuxClient.NewSession(ctx, tmux.NewSessionOpts{Name: "sh", Command: "/bin/sh"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	d := newNode(map[session.TmuxServer]*tmux.Client{
		session.TmuxServerArgus: tmuxClient,
	})
	// Discovery identifies Claude panes by their foreground process; this pane
	// runs /bin/sh, so make discovery treat it as Claude. Otherwise the startup
	// and hook scans reconcile it away (ReconcileDiscovered prunes panes it does
	// not recognize) and capture/input race against that prune. A real Claude
	// pane is found by discovery and kept, which is what this models.
	d.disc.SetMatch(func(tmux.Pane) bool { return true })

	socket := filepath.Join(t.TempDir(), "d.sock")
	go func() { _ = d.Run(ctx, socket) }()

	client := dialWithRetry(t, socket)
	defer client.Close()

	// Register the shell pane as a session via a hook.
	payload, _ := json.Marshal(map[string]string{"session_id": "sh-1"})
	if err := client.Call(claudecode.HookMethod, claudecode.HookEvent{
		Event: "SessionStart", TmuxPane: paneID, TmuxSocket: "argus", Payload: payload,
	}, nil); err != nil {
		t.Fatalf("hook call: %v", err)
	}
	sessionID := "argus:" + paneID

	// Force a synchronous discovery scan (Run and the hook also kick async
	// scans; making one deterministic here exercises the reconcile path).
	d.scan(ctx)

	// Capture should succeed.
	var cap api.CaptureResult
	if err := client.Call(api.MethodSessionCapture, api.SessionRef{SessionID: sessionID}, &cap); err != nil {
		t.Fatalf("capture: %v", err)
	}

	// Send a command and verify it appears on screen.
	if err := client.Call(api.MethodSessionInput, api.InputParams{
		SessionID: sessionID, Text: "echo ARGUS_E2E_OK", Submit: true,
	}, nil); err != nil {
		t.Fatalf("input: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var c api.CaptureResult
		if err := client.Call(api.MethodSessionCapture, api.SessionRef{SessionID: sessionID}, &c); err == nil {
			if strings.Contains(c.Screen, "ARGUS_E2E_OK") {
				return // success
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("input never appeared in captured screen")
}

// TestSessionInputDelaysEnterAfterText verifies that when text and submit are
// sent together, the Enter is held back by submitDelay so Claude's TUI reads it
// as a separate event (a coalesced text+CR is swallowed and never submits). A
// text-only send (no submit) must not incur the delay.
func TestSessionInputDelaysEnterAfterText(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	old := submitDelay
	submitDelay = 250 * time.Millisecond
	t.Cleanup(func() { submitDelay = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmuxClient := tmux.New("argus-delay-test")
	t.Cleanup(func() { _ = tmuxClient.KillServer(context.Background()) })

	paneID, err := tmuxClient.NewSession(ctx, tmux.NewSessionOpts{Name: "sh", Command: "/bin/sh"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	d := newNode(map[session.TmuxServer]*tmux.Client{session.TmuxServerArgus: tmuxClient})

	// Register the pane directly (no API/discovery, so no scan prunes this
	// non-Claude shell pane mid-test).
	payload, _ := json.Marshal(map[string]string{"session_id": "sh-1"})
	if _, ok := claudecode.ProcessHook(d.reg, claudecode.HookEvent{
		Event: "SessionStart", TmuxPane: paneID, TmuxSocket: "argus", Payload: payload,
	}); !ok {
		t.Fatal("ProcessHook did not register the session")
	}
	sessionID := "argus:" + paneID

	call := func(p api.InputParams) {
		t.Helper()
		raw, _ := json.Marshal(p)
		if _, err := d.handleSessionInput(ctx, raw); err != nil {
			t.Fatalf("handleSessionInput(%+v): %v", p, err)
		}
	}

	// Text + submit must wait at least submitDelay between the two sends.
	start := time.Now()
	call(api.InputParams{SessionID: sessionID, Text: "echo hi", Submit: true})
	if elapsed := time.Since(start); elapsed < submitDelay-20*time.Millisecond {
		t.Fatalf("text+submit returned in %v; expected >= ~%v (Enter not delayed)", elapsed, submitDelay)
	}

	// Text only (no submit) must not pay the delay.
	start = time.Now()
	call(api.InputParams{SessionID: sessionID, Text: "echo hi", Submit: false})
	if elapsed := time.Since(start); elapsed >= submitDelay {
		t.Fatalf("text-only took %v; should not incur the %v submit delay", elapsed, submitDelay)
	}
}

// TestSpawnAndKillEndToEnd spawns a session via the API on an isolated argus
// tmux server, then kills it, asserting the tmux pane is created and removed.
func TestSpawnAndKillEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmuxClient := tmux.New("argus-spawn-test")
	t.Cleanup(func() { _ = tmuxClient.KillServer(context.Background()) })

	d := newNode(map[session.TmuxServer]*tmux.Client{session.TmuxServerArgus: tmuxClient})
	socket := filepath.Join(t.TempDir(), "d.sock")
	go func() { _ = d.Run(ctx, socket) }()
	client := dialWithRetry(t, socket)
	defer client.Close()

	var res api.SpawnResult
	if err := client.Call(api.MethodSessionSpawn, api.SpawnParams{Name: "spawned", Command: "/bin/sh"}, &res); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.PaneID == "" {
		t.Fatal("spawn returned no pane id")
	}
	if !paneExists(t, tmuxClient, res.PaneID) {
		t.Fatalf("spawned pane %s not found on tmux server", res.PaneID)
	}

	// Register the pane as a session so kill can resolve it, then kill.
	payload, _ := json.Marshal(map[string]string{"session_id": "spawn-1"})
	if err := client.Call(claudecode.HookMethod, claudecode.HookEvent{
		Event: "SessionStart", TmuxPane: res.PaneID, TmuxSocket: "argus", Payload: payload,
	}, nil); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if err := client.Call(api.MethodSessionKill, api.SessionRef{SessionID: res.SessionID}, nil); err != nil {
		t.Fatalf("kill: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !paneExists(t, tmuxClient, res.PaneID) {
			return // killed
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane %s still alive after kill", res.PaneID)
}

func paneExists(t *testing.T, c *tmux.Client, paneID string) bool {
	t.Helper()
	panes, err := c.ListPanes(context.Background())
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	for _, p := range panes {
		if p.PaneID == paneID {
			return true
		}
	}
	return false
}

// TestRunRefusesWhenAlreadyRunning verifies Run will not steal a socket from a
// live node: a second Run on the same path errors instead of unlinking and
// rebinding (which previously orphaned the first node and let teardown of either
// delete the other's socket).
func TestRunRefusesWhenAlreadyRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socket := filepath.Join(t.TempDir(), "d.sock")
	d1 := New()
	go func() { _ = d1.Run(ctx, socket) }()
	dialWithRetry(t, socket).Close() // wait until d1 is listening

	d2 := New()
	err := d2.Run(ctx, socket)
	if err == nil {
		t.Fatal("second Run should refuse a socket with a live node")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("error = %v, want 'already running'", err)
	}
}

func dialWithRetry(t *testing.T, socket string) *api.Client {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := api.Dial(socket); err == nil {
			return c
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("node never came up")
	return nil
}
