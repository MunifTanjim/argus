package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// shortSocket returns a unix socket path under a short temp dir. t.TempDir()
// embeds the (long) test name, which can blow past the ~104-char sun_path limit
// on macOS, so we make our own short-named dir instead.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ad")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

// permissionHook builds a Bash PermissionRequest hook event for a fresh session.
func permissionHook() claudecode.HookEvent {
	payload, _ := json.Marshal(map[string]any{
		"session_id":      "perm-1",
		"hook_event_name": "PermissionRequest",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": "ls"},
	})
	return claudecode.HookEvent{
		Event: "PermissionRequest", TmuxPane: "%7", TmuxSocket: "argus", Payload: payload,
	}
}

// waitParkedID polls until a decision is parked and returns the session id it
// parked under.
func waitParkedID(t *testing.T, d *Node) string {
	t.Helper()
	for i := 0; i < 400; i++ {
		d.pendingMu.Lock()
		var sid string
		for id := range d.pending {
			sid = id
		}
		d.pendingMu.Unlock()
		if sid != "" {
			return sid
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("PermissionRequest was never parked")
	return ""
}

// TestPermissionRequestParksWithoutViewer verifies the node parks and resolves a
// PermissionRequest even when no TUI ever pinged it: the blocked hook returns the
// decision the TUI sends via sessions.respond. This guards the fix that removed the
// viewer-presence gate, which used to drop the prompt when no recent heartbeat
// existed — leaving the TUI's Allow/Deny a silent no-op.
func TestPermissionRequestParksWithoutViewer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := newNode(map[session.TmuxServer]*tmux.Client{})
	socket := shortSocket(t)
	go func() { _ = d.Run(ctx, socket) }()

	// The hook subprocess and the TUI are distinct connections; neither ever pings.
	hookClient := dialWithRetry(t, socket)
	defer hookClient.Close()
	tuiClient := dialWithRetry(t, socket)
	defer tuiClient.Close()

	// The hook blocks until answered; run it off the test goroutine.
	out := make(chan api.HookResult, 1)
	go func() {
		var res api.HookResult
		_ = hookClient.Call(claudecode.HookMethod, permissionHook(), &res)
		out <- res
	}()

	sid := waitParkedID(t, d)

	// The TUI answers; the parked hook should unblock with the allow decision.
	if err := tuiClient.Call(api.MethodSessionRespond, api.RespondParams{
		SessionID: sid, Kind: "permission", Behavior: "allow",
	}, nil); err != nil {
		t.Fatalf("respond: %v", err)
	}

	select {
	case res := <-out:
		if !strings.Contains(res.Output, `"behavior":"allow"`) {
			t.Errorf("hook output = %q, want an allow decision", res.Output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hook never returned after respond")
	}

	if s, _ := d.reg.Get(sid); s.Interaction != nil {
		t.Error("interaction not cleared after respond")
	}
}

// TestPermissionRequestUnblocksOnHookDisconnect verifies that closing the hook
// connection (the user answered in Claude's own pane, so Claude killed the hook)
// cancels the parked decision promptly via ctx — never hanging until
// decisionTimeout. This is the safety net that makes always-parking safe.
func TestPermissionRequestUnblocksOnHookDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := newNode(map[session.TmuxServer]*tmux.Client{})
	socket := shortSocket(t)
	go func() { _ = d.Run(ctx, socket) }()

	hookClient := dialWithRetry(t, socket)
	go func() {
		var res api.HookResult
		_ = hookClient.Call(claudecode.HookMethod, permissionHook(), &res)
	}()

	sid := waitParkedID(t, d)

	// Simulate Claude killing the hook: drop the connection.
	hookClient.Close()

	// The parked decision must be released promptly (well under decisionTimeout)
	// and the stale interaction cleared.
	for i := 0; i < 1000; i++ {
		d.pendingMu.Lock()
		_, parked := d.pending[sid]
		d.pendingMu.Unlock()
		s, _ := d.reg.Get(sid)
		if !parked && s.Interaction == nil {
			return // released and cleared
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("parked decision was not released after hook disconnect")
}
