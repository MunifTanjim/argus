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

// A freshly opened pane-bearing session has no transcript yet, so discovery
// carries no StatusHint. It must still land as idle with an idle Interaction so
// clients show the compose to send the first prompt; a paneless one (can't be
// typed into) stays discovered.
func TestReconcileSessionsFreshPaneDefaultsIdle(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", []DiscoveredSession{
		tmuxDisc("c0", "%0", "a", session.TmuxServerDefault), // no StatusHint
		{AgentSessionID: "vs-1", Frontend: session.FrontendVSCode},
	})

	s, ok := r.Get("default:%0")
	if !ok {
		t.Fatal("pane session missing")
	}
	if s.Status != session.StatusIdle {
		t.Fatalf("fresh pane session status: want idle, got %q", s.Status)
	}
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("fresh pane session must synthesize idle interaction, got %+v", s.Interaction)
	}

	// Paneless: nothing to type into, stays discovered with no interaction.
	vs, ok := r.Get("claude:vs-1")
	if !ok {
		t.Fatal("vscode session missing")
	}
	if vs.Status != session.StatusDiscovered || vs.Interaction != nil {
		t.Fatalf("paneless session should stay discovered/no-interaction, got %q %+v", vs.Status, vs.Interaction)
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

func TestReconcileSessionsPaneOnlyStarting(t *testing.T) {
	r := New()
	// Pane-only discovery record: has a pane, no AgentSessionID (still at a gate).
	r.ReconcileSessions("claude", []DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%7"},
	})
	s, ok := r.Get("default:%7")
	if !ok {
		t.Fatal("pane-only session missing")
	}
	if s.Status != session.StatusStarting {
		t.Fatalf("pane-only session status: want starting, got %q", s.Status)
	}
	if s.Interaction != nil {
		t.Fatalf("starting session must not synthesize an interaction, got %+v", s.Interaction)
	}
}

func TestReconcileSessionsStartingUpgradesToIdle(t *testing.T) {
	r := New()
	// First scan: pane-only, stuck at a startup gate.
	r.ReconcileSessions("claude", []DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%7"},
	})
	// Second scan: proc-session file appeared → same pane now carries an
	// AgentSessionID, still no transcript.
	r.ReconcileSessions("claude", []DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%7", AgentSessionID: "sess-1"},
	})
	s, _ := r.Get("default:%7")
	if s.Status != session.StatusIdle {
		t.Fatalf("upgraded session status: want idle, got %q", s.Status)
	}
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("upgraded session must synthesize idle interaction, got %+v", s.Interaction)
	}
}

// A pane-only discovered session (no AgentSessionID — e.g. a claude stuck at a
// startup gate before it writes its session id) must be created as an attachable
// pane-keyed record and survive a later scan that still only sees the pane, so it
// isn't pruned while the user is trying to open it.
func TestReconcileSessionsPaneOnlyControllableAndSurvives(t *testing.T) {
	r := New()
	paneOnly := DiscoveredSession{
		HasPane: true, Server: session.TmuxServerArgus, PaneID: "%3",
		Frontend: session.FrontendTmux,
	}
	r.ReconcileSessions("claude", []DiscoveredSession{paneOnly})

	s, ok := r.Get("argus:%3")
	if !ok || !s.Controllable() || s.AgentSessionID != "" || s.Frontend != session.FrontendTmux {
		t.Fatalf("pane-only session not created controllable: ok=%v %+v", ok, s)
	}

	// Still stuck: an identical pane-only scan must keep it alive.
	r.ReconcileSessions("claude", []DiscoveredSession{paneOnly})
	if _, ok := r.Get("argus:%3"); !ok {
		t.Fatal("pane-only session should survive a rescan that still sees the pane")
	}
}

// A pane-only session (no AgentSessionID) is kept alive solely by its pane. When
// the process exits and the pane is gone from the scan, it has no claude id to
// fall back on and must be pruned — e.g. a short-lived `claude -p` run.
func TestReconcileSessionsPaneOnlyPrunedWhenPaneGone(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", []DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerArgus, PaneID: "%3", Frontend: session.FrontendTmux},
	})
	if _, ok := r.Get("argus:%3"); !ok {
		t.Fatal("pane-only session should be created")
	}
	r.ReconcileSessions("claude", nil)
	if _, ok := r.Get("argus:%3"); ok {
		t.Error("pane-only session should be pruned once the pane is gone")
	}
}

// A pane-only record must not clobber the cwd/repo a proc-session already set for
// the same pane: the pane's current path can drift (the user cd's in a subshell)
// while the authoritative launch cwd stays fixed.
func TestReconcileSessionsPaneOnlyDoesNotOverwriteCwd(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", []DiscoveredSession{{
		AgentSessionID: "c0", HasPane: true, Server: session.TmuxServerDefault,
		PaneID: "%0", Frontend: session.FrontendTmux, Cwd: "/work/repo", Repo: "repo",
	}})
	// Transient scan: proc-session file unreadable, only the pane seen with a
	// drifted current path.
	r.ReconcileSessions("claude", []DiscoveredSession{{
		HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0",
		Frontend: session.FrontendTmux, Cwd: "/tmp/sub", Repo: "sub",
	}})
	s, _ := r.Get("default:%0")
	if s.Cwd != "/work/repo" || s.Repo != "repo" {
		t.Fatalf("pane-only scan overwrote authoritative cwd/repo: %+v", s)
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
	// A scan that no longer sees the argus pane must prune it, even though
	// default:%0 shares the pane id.
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
