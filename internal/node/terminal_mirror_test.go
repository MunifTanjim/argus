package node

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// newTestClient returns a Client on a throwaway tmux socket, killed at test end.
// Skips if tmux is not installed.
func newTestClient(t *testing.T) *tmux.Client {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := "argus-test-" + t.Name()
	c := tmux.New(socket)
	t.Cleanup(func() {
		_ = c.KillServer(context.Background())
	})
	return c
}

func TestSetupAndRestoreMirror(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	pane, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: "origin", Command: "sh"})
	if err != nil {
		t.Fatal(err)
	}
	d := newNode(map[session.TmuxServer]*tmux.Client{session.TmuxServerDefault: c})
	d.mirrorPrefix, d.mirrorSuffix = "_", "_"
	s := session.Session{Tmux: session.TmuxLocation{Server: session.TmuxServerDefault, PaneID: pane, SessionName: "origin", WindowIndex: 0}}

	m, err := d.setupMirror(ctx, c, s, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WindowInfo(ctx, m.window); err != nil {
		t.Fatalf("mirror window missing: %v", err)
	}
	d.restoreMirror(c, m)
	// mirror session gone
	if _, err := c.WindowInfo(ctx, m.name); err == nil {
		t.Fatal("mirror session should be killed")
	}
}

// TestSetupMirror_MultiWindow verifies that setupMirror targets the agent pane's
// window (not the origin session's currently active window) in a multi-window session.
func TestSetupMirror_MultiWindow(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	socket := fmt.Sprintf("argus-test-%s", t.Name())

	// Create origin session; first pane lands in the server's base-index window.
	firstPane, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: "origin", Command: "sh"})
	if err != nil {
		t.Fatal(err)
	}

	// Record the first window's index (respects server base-index, e.g. 0 or 1).
	firstWindowIdx, err := c.WindowIndexForPane(ctx, firstPane)
	if err != nil {
		t.Fatalf("WindowIndexForPane first: %v", err)
	}

	// Add a second window; the agent pane will live here.
	if out, err := exec.Command("tmux", "-L", socket, "new-window", "-t", "origin", "sh").CombinedOutput(); err != nil {
		t.Fatalf("new-window: %v: %s", err, out)
	}

	// List panes to find the one in the second window (different index from the first).
	panes, err := c.ListPanes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var agentPane string
	var agentWindowIdx int
	for _, p := range panes {
		if p.SessionName == "origin" && p.WindowIndex != firstWindowIdx {
			agentPane = p.PaneID
			agentWindowIdx = p.WindowIndex
			break
		}
	}
	if agentPane == "" {
		t.Fatal("agent pane not found in second window")
	}

	// Select back to the first window so the agent pane is in the NON-active window.
	if err := c.SelectWindow(ctx, fmt.Sprintf("origin:%d", firstWindowIdx)); err != nil {
		t.Fatalf("select-window origin:%d: %v", firstWindowIdx, err)
	}

	d := newNode(map[session.TmuxServer]*tmux.Client{session.TmuxServerDefault: c})
	d.mirrorPrefix, d.mirrorSuffix = "_", "_"
	s := session.Session{Tmux: session.TmuxLocation{
		Server:      session.TmuxServerDefault,
		PaneID:      agentPane,
		SessionName: "origin",
		WindowIndex: agentWindowIdx,
	}}

	m, err := d.setupMirror(ctx, c, s, "t2")
	if err != nil {
		t.Fatal(err)
	}

	// The mirror window must reflect the agent pane's window (second window), not
	// origin's active window (first window).
	info, err := c.WindowInfo(ctx, m.window)
	if err != nil {
		t.Fatalf("WindowInfo(%q): %v", m.window, err)
	}
	if info.ActivePane != agentPane {
		t.Fatalf("mirror window active pane = %q, want agent pane %q", info.ActivePane, agentPane)
	}

	d.restoreMirror(c, m)
	// Mirror session must be gone after restore.
	if _, err := c.WindowInfo(ctx, m.name); err == nil {
		t.Fatal("mirror session should be killed after restoreMirror")
	}
}

// TestLockdownMirror verifies setupMirror sets prefix=None, mouse=off, and
// key-table=argus-locked, so a tmux prefix reaches the agent pane as literal input.
func TestLockdownMirror(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	pane, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: "origin", Command: "sh"})
	if err != nil {
		t.Fatal(err)
	}
	d := newNode(map[session.TmuxServer]*tmux.Client{session.TmuxServerDefault: c})
	d.mirrorPrefix, d.mirrorSuffix = "_", "_"
	s := session.Session{Tmux: session.TmuxLocation{
		Server: session.TmuxServerDefault, PaneID: pane,
		SessionName: "origin", WindowIndex: 0,
	}}

	m, err := d.setupMirror(ctx, c, s, "lock1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.restoreMirror(c, m) })

	// Query the mirror session's effective options. newTestClient uses socket
	// "argus-test-<t.Name()>" so we can invoke tmux directly.
	socket := "argus-test-" + t.Name()
	out, err := exec.Command("tmux", "-L", socket, "show-options", "-t", m.name).CombinedOutput()
	if err != nil {
		t.Fatalf("show-options: %v: %s", err, out)
	}
	opts := string(out)
	for _, want := range []string{"prefix None", "mouse off", "key-table argus-locked"} {
		if !strings.Contains(opts, want) {
			t.Errorf("mirror session missing lockdown option %q\nshow-options output:\n%s", want, opts)
		}
	}
}

// TestReapMirrorsKillsOrphans verifies that reapMirrors kills orphaned mirror sessions and leaves the origin session intact.
func TestReapMirrorsKillsOrphans(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	if _, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: "origin", Command: "sh"}); err != nil {
		t.Fatal(err)
	}
	if err := c.NewGroupedSession(ctx, "_argus-mirror-orphan_", "origin"); err != nil {
		t.Fatal(err)
	}
	d := newNode(map[session.TmuxServer]*tmux.Client{session.TmuxServerDefault: c})
	d.mirrorPrefix, d.mirrorSuffix = "_", "_"
	d.reapMirrors(ctx)
	if _, err := c.WindowInfo(ctx, "_argus-mirror-orphan_"); err == nil {
		t.Fatal("orphan mirror should be reaped")
	}
	// origin session must survive
	if _, err := c.WindowInfo(ctx, "origin"); err != nil {
		t.Fatalf("origin session should survive reap: %v", err)
	}
}
