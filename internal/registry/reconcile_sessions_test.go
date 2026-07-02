package registry

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func tmuxDisc(agentSessionID, paneID, name string, server session.TmuxServer) DiscoveredSession {
	return DiscoveredSession{
		AgentSessionID: agentSessionID,
		HasPane:        true,
		Server:         server,
		PaneID:         paneID,
		SessionName:    name,
		Frontend:       session.FrontendTmux,
	}
}

func TestReconcileSessionsAddsTmuxAndPrunes(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", []DiscoveredSession{
		tmuxDisc("c0", "%0", "a", session.TmuxServerDefault),
		tmuxDisc("c1", "%1", "b", session.TmuxServerDefault),
	})
	if n := len(r.Snapshot()); n != 2 {
		t.Fatalf("want 2, got %d", n)
	}
	s, ok := r.Get("default:%0")
	if !ok || s.Frontend != session.FrontendTmux || s.Tmux.PaneID != "%0" || s.AgentSessionID != "c0" {
		t.Fatalf("default:%%0 wrong: ok=%v %+v", ok, s)
	}

	// %1 gone → pruned.
	r.ReconcileSessions("claude", []DiscoveredSession{
		tmuxDisc("c0", "%0", "a", session.TmuxServerDefault),
	})
	if _, ok := r.Get("default:%1"); ok {
		t.Error("default:%%1 should be pruned")
	}
}

func TestReconcileSessionsAddsVSCodeAndPrunes(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", []DiscoveredSession{
		{AgentSessionID: "vs-1", Frontend: session.FrontendVSCode, Name: "n"},
	})
	s, ok := r.Get("claude:vs-1")
	if !ok || s.Frontend != session.FrontendVSCode || s.Tmux.PaneID != "" || s.Source != session.SourceDiscovered {
		t.Fatalf("vs-1 wrong: ok=%v %+v", ok, s)
	}
	r.ReconcileSessions("claude", nil)
	if _, ok := r.Get("claude:vs-1"); ok {
		t.Error("vs-1 should be pruned when gone")
	}
}

func TestReconcileSessionsAttachesPaneOntoClaudeRecord(t *testing.T) {
	r := New()
	// Hook created a paneless record keyed by claude id.
	r.ApplyHook(HookUpdate{Agent: "claude", AgentSessionID: "c1", Status: session.StatusWorking})
	// Discovery later correlates a pane for the same claude id.
	r.ReconcileSessions("claude", []DiscoveredSession{
		tmuxDisc("c1", "%5", "w", session.TmuxServerDefault),
	})
	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("want 1 (no duplicate), got %d", n)
	}
	s, ok := r.Get("claude:c1") // ID stays stable
	if !ok || s.Tmux.PaneID != "%5" || s.Frontend != session.FrontendTmux {
		t.Fatalf("pane not attached to claude record: ok=%v %+v", ok, s)
	}
	if s.Status != session.StatusWorking {
		t.Errorf("hook status downgraded: %q", s.Status)
	}
}

func TestReconcileSessionsLearnsClaudeOntoPaneRecord(t *testing.T) {
	r := New()
	// A pane-bearing record with no claude id yet.
	r.ReconcileSessions("claude", []DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", Frontend: session.FrontendTmux},
	})
	// Next scan learns the claude id for the same pane.
	r.ReconcileSessions("claude", []DiscoveredSession{
		tmuxDisc("c0", "%0", "a", session.TmuxServerDefault),
	})
	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("want 1, got %d", n)
	}
	id, ok := r.index.findByAgentSession("c0")
	if !ok || id != "default:%0" {
		t.Fatalf("claude id not indexed onto pane record: ok=%v id=%q", ok, id)
	}
}

func TestReconcileSessionsNeverDowngradesFrontend(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", []DiscoveredSession{
		tmuxDisc("c0", "%0", "a", session.TmuxServerDefault),
	})
	// Transient scan where the pane wasn't correlated but the claude id was:
	// must keep the existing pane + tmux frontend (alive via claude id).
	r.ReconcileSessions("claude", []DiscoveredSession{
		{AgentSessionID: "c0", Frontend: session.FrontendExternal},
	})
	s, _ := r.Get("default:%0")
	if s.Frontend != session.FrontendTmux || s.Tmux.PaneID != "%0" {
		t.Fatalf("frontend/pane downgraded: %+v", s)
	}
}

func TestReconcileSessionsCrossServerLiveness(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", []DiscoveredSession{
		tmuxDisc("c0", "%0", "a", session.TmuxServerDefault),
		tmuxDisc("c1", "%0", "b", session.TmuxServerArgus),
	})
	if n := len(r.Snapshot()); n != 2 {
		t.Fatalf("same pane id on two servers = 2 sessions, got %d", n)
	}
	// Scan that finds only the default server's session must not prune argus's,
	// because the global found-set still contains argus this call... so instead
	// model a scan that genuinely no longer sees argus:
	r.ReconcileSessions("claude", []DiscoveredSession{
		tmuxDisc("c0", "%0", "a", session.TmuxServerDefault),
	})
	if _, ok := r.Get("argus:%0"); ok {
		t.Error("argus:%%0 should be pruned when absent from the scan")
	}
	if _, ok := r.Get("default:%0"); !ok {
		t.Error("default:%%0 should survive")
	}
}

// A /clear keeps the pane but swaps AgentSessionID + transcript path: discovery
// must reset the stale summary and drop the superseded claude id from the index.
func TestReconcileSessionsTranscriptSwapResetsSummaryAndClaudeIndex(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", []DiscoveredSession{{
		AgentSessionID: "c0", HasPane: true, Server: session.TmuxServerDefault,
		PaneID: "%0", Frontend: session.FrontendTmux,
		TranscriptPath: "/tmp/c0.jsonl", Summary: &session.Summary{Task: "pre-clear"},
	}})
	// /clear on the same pane: new claude id + transcript, no fresh summary yet.
	r.ReconcileSessions("claude", []DiscoveredSession{{
		AgentSessionID: "c1", HasPane: true, Server: session.TmuxServerDefault,
		PaneID: "%0", Frontend: session.FrontendTmux,
		TranscriptPath: "/tmp/c1.jsonl",
	}})
	s, ok := r.Get("default:%0")
	if !ok {
		t.Fatal("record missing after swap")
	}
	if s.Summary != nil {
		t.Fatalf("transcript swap must reset the stale summary, got %+v", s.Summary)
	}
	if s.TranscriptPath != "/tmp/c1.jsonl" || s.AgentSessionID != "c1" {
		t.Fatalf("swap not applied: %+v", s)
	}
	if _, ok := r.index.findByAgentSession("c0"); ok {
		t.Error("superseded claude id should be cleared from the index")
	}
	if id, ok := r.index.findByAgentSession("c1"); !ok || id != s.ID {
		t.Errorf("new claude id should resolve to the record: %q %v", id, ok)
	}
}
