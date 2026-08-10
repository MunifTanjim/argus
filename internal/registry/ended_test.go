package registry

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

func endedPane(paneID string) []DiscoveredSession {
	return []DiscoveredSession{{
		HasPane: true, Server: session.TmuxServerArgus, PaneID: paneID, AgentSessionID: "agent-1",
	}}
}

func deadHook(paneID string) HookUpdate {
	return HookUpdate{
		Agent: "claude", Server: session.TmuxServerArgus, PaneID: paneID,
		AgentSessionID: "agent-1", Status: session.StatusDead,
	}
}

// An agent's exit hook fires while its process is still alive, so the rescan it
// triggers still sees the pane. Without a guard that scan re-creates the session
// the hook just ended, and nothing removes it again until the next manual refresh.
func TestReconcileDoesNotResurrectSessionEndedByHook(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", endedPane("%1"))
	if len(r.Snapshot()) != 1 {
		t.Fatalf("setup: %d sessions, want 1", len(r.Snapshot()))
	}

	r.ApplyHook(deadHook("%1"))
	if n := len(r.Snapshot()); n != 0 {
		t.Fatalf("after the end hook: %d sessions, want 0", n)
	}

	r.ReconcileSessions("claude", endedPane("%1")) // the agent is still shutting down
	if n := len(r.Snapshot()); n != 0 {
		t.Fatalf("the scan resurrected the ended session: %d sessions, want 0", n)
	}
}

// The guard is a short window, not a permanent block: a pane reused by a new agent
// is discovered again once it passes.
func TestReconcileDiscoversPaneAgainAfterEndedGrace(t *testing.T) {
	r := New()
	r.endedGrace = 10 * time.Millisecond
	r.ReconcileSessions("claude", endedPane("%1"))
	r.ApplyHook(deadHook("%1"))

	time.Sleep(20 * time.Millisecond)
	r.ReconcileSessions("claude", endedPane("%1"))
	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("after the grace window: %d sessions, want 1", n)
	}
}

// A hook for the same pane means a live agent again — discovery must stop being
// held back immediately, without waiting out the window.
func TestHookClearsEndedGuardForItsPane(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", endedPane("%1"))
	r.ApplyHook(deadHook("%1"))

	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerArgus, PaneID: "%1",
		AgentSessionID: "agent-2", Status: session.StatusIdle,
	})
	r.ReconcileSessions("claude", []DiscoveredSession{{
		HasPane: true, Server: session.TmuxServerArgus, PaneID: "%1", AgentSessionID: "agent-2",
	}})
	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("restarted agent: %d sessions, want 1", n)
	}
}
