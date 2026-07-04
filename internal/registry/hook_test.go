package registry

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestApplyHookEnrichesDiscoveredSession(t *testing.T) {
	r := New()
	// First discovered via tmux.
	r.ReconcileSessions("claude", []DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", SessionName: "a", Frontend: session.FrontendTmux},
	})

	// Then a hook arrives for that pane.
	got, alive := r.ApplyHook(HookUpdate{
		Agent:          "claude",
		Server:         session.TmuxServerDefault,
		PaneID:         "%0",
		AgentSessionID: "sess-abc",
		Cwd:            "/work",
		TranscriptPath: "/t/sess-abc.jsonl",
		Status:         session.StatusWorking,
	})
	if !alive {
		t.Fatal("session should be alive")
	}
	if got.AgentSessionID != "sess-abc" || got.Status != session.StatusWorking {
		t.Fatalf("hook not applied: %+v", got)
	}
	if got.Source != session.SourceDiscovered {
		t.Errorf("source should remain discovered, got %q", got.Source)
	}
	// Must still be a single session (correlated, not duplicated).
	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("want 1 session, got %d", n)
	}
}

func TestApplyHookCreatesWhenNoMatch(t *testing.T) {
	r := New()
	got, alive := r.ApplyHook(HookUpdate{
		Agent:          "claude",
		Server:         session.TmuxServerArgus,
		PaneID:         "%5",
		AgentSessionID: "z",
		Status:         session.StatusIdle,
	})
	if !alive || got.Source != session.SourceHooked {
		t.Fatalf("want hooked session, got %+v alive=%v", got, alive)
	}
	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("want 1 session, got %d", n)
	}
}

func TestApplyHookStatusDeadRemoves(t *testing.T) {
	r := New()
	r.ReconcileSessions("claude", []DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", SessionName: "a", Frontend: session.FrontendTmux},
	})
	_, alive := r.ApplyHook(HookUpdate{
		Agent:  "claude",
		Server: session.TmuxServerDefault,
		PaneID: "%0",
		Status: session.StatusDead,
	})
	if alive {
		t.Fatal("session should be removed on dead status")
	}
	if n := len(r.Snapshot()); n != 0 {
		t.Fatalf("want 0 sessions, got %d", n)
	}
}

// A bare Notification fallback (Permission with no ToolInput) must not wipe the
// richer interaction the PermissionRequest hook already recorded, regardless of
// which hook the node happens to process last.
func TestApplyHookNotificationFallbackKeepsRicherInteraction(t *testing.T) {
	const tool = "Bash"
	const input = `{"command":"ls"}`
	rich := func() *session.Interaction {
		return &session.Interaction{Kind: session.InteractionPermission, ToolName: tool, ToolInput: input}
	}
	fallback := func() *session.Interaction {
		return &session.Interaction{Kind: session.InteractionPermission, ToolName: tool, Message: "needs permission"}
	}
	apply := func(r *Registry, ix *session.Interaction, status session.Status) session.Session {
		got, _ := r.ApplyHook(HookUpdate{
			Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
			Status: status, Interaction: ix,
		})
		return got
	}

	// Rich first, then the empty fallback → ToolInput survives, message adopted.
	r := New()
	apply(r, rich(), session.StatusAwaitingInput)
	got := apply(r, fallback(), session.StatusAwaitingInput)
	if got.Interaction == nil || got.Interaction.ToolInput != input {
		t.Fatalf("fallback clobbered ToolInput: %+v", got.Interaction)
	}
	if got.Interaction.Message != "needs permission" {
		t.Errorf("newer message not adopted: %+v", got.Interaction)
	}

	// Reverse order (empty first, then rich) → the rich update replaces normally.
	r = New()
	apply(r, fallback(), session.StatusAwaitingInput)
	got = apply(r, rich(), session.StatusAwaitingInput)
	if got.Interaction == nil || got.Interaction.ToolInput != input {
		t.Fatalf("rich update should replace the fallback: %+v", got.Interaction)
	}

	// A nil interaction (session moves on) still clears, no regression to dismissal.
	got = apply(r, nil, session.StatusIdle)
	if got.Interaction != nil {
		t.Errorf("interaction should clear on a nil update: %+v", got.Interaction)
	}

	// A fallback must not overwrite a pending Question either.
	r = New()
	question := &session.Interaction{Kind: session.InteractionQuestion,
		Questions: []session.QuestionSpec{{Question: "Q", Options: []string{"A"}}}}
	apply(r, question, session.StatusAwaitingInput)
	got = apply(r, fallback(), session.StatusAwaitingInput)
	if got.Interaction == nil || len(got.Interaction.Questions) != 1 {
		t.Fatalf("fallback clobbered the question interaction: %+v", got.Interaction)
	}

	// An idle Notification arriving after a permission must not clobber it (the
	// back-to-back prompt bug); it only adopts the newer message.
	idle := func() *session.Interaction {
		return &session.Interaction{Kind: session.InteractionIdle, Message: "waiting"}
	}
	r = New()
	apply(r, rich(), session.StatusAwaitingInput)
	got = apply(r, idle(), session.StatusAwaitingInput)
	if got.Interaction == nil || got.Interaction.Kind != session.InteractionPermission ||
		got.Interaction.ToolInput != input {
		t.Fatalf("idle clobbered the permission: %+v", got.Interaction)
	}
	if got.Interaction.Message != "waiting" {
		t.Errorf("newer message not adopted over permission: %+v", got.Interaction)
	}

	// A real permission still replaces an earlier idle interaction.
	r = New()
	apply(r, idle(), session.StatusAwaitingInput)
	got = apply(r, rich(), session.StatusAwaitingInput)
	if got.Interaction == nil || got.Interaction.Kind != session.InteractionPermission ||
		got.Interaction.ToolInput != input {
		t.Fatalf("permission should replace idle: %+v", got.Interaction)
	}
}

// ReplaceInteraction (used by the Stop hook) overwrites a richer pending
// interaction with the idle one instead of letting mergeInteraction protect it —
// the turn has ended, so a permission/question/plan the user already resolved in
// their own terminal must be dismissed.
func TestApplyHookReplaceInteractionSupersedesRicher(t *testing.T) {
	rich := func() *session.Interaction {
		return &session.Interaction{Kind: session.InteractionPlan, Plan: "stale plan"}
	}
	idle := &session.Interaction{Kind: session.InteractionIdle}
	apply := func(r *Registry, ix *session.Interaction, replace bool) session.Session {
		got, _ := r.ApplyHook(HookUpdate{
			Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
			Status: session.StatusAwaitingInput, Interaction: ix, ReplaceInteraction: replace,
		})
		return got
	}

	// With ReplaceInteraction the idle prompt wins over the pending plan.
	r := New()
	apply(r, rich(), false)
	got := apply(r, idle, true)
	if got.Interaction == nil || got.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("replace should supersede the plan with idle: %+v", got.Interaction)
	}

	// Without the flag the same idle update is held off by mergeInteraction.
	r = New()
	apply(r, rich(), false)
	got = apply(r, idle, false)
	if got.Interaction == nil || got.Interaction.Kind != session.InteractionPlan {
		t.Fatalf("merge should keep the plan without replace: %+v", got.Interaction)
	}
}

// A refreshed Summary is stored; a later status-only update (Summary == nil) keeps
// the cached digest. Repo persists likewise.
func TestApplyHookCachesSummaryAndRepo(t *testing.T) {
	r := New()
	sum := &session.Summary{ModelName: "Opus 4.8", HasContext: true, ContextPct: 42, Task: "do the thing"}
	got, _ := r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		Repo: "argus", Status: session.StatusWorking, Summary: sum,
	})
	if got.Summary == nil || got.Summary.ModelName != "Opus 4.8" || got.Repo != "argus" {
		t.Fatalf("summary/repo not stored: %+v repo=%q", got.Summary, got.Repo)
	}

	// A status-only update with no summary keeps the cached one (and repo).
	got, _ = r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		Status: session.StatusIdle,
	})
	if got.Summary == nil || got.Summary.Task != "do the thing" {
		t.Errorf("cached summary should survive a status-only update: %+v", got.Summary)
	}
	if got.Repo != "argus" {
		t.Errorf("repo should persist: %q", got.Repo)
	}
}

func TestApplyHookCorrelatesByClaudeIDAcrossReconcile(t *testing.T) {
	r := New()
	// Hook first (claude not yet discovered in tmux), keyed by pane.
	r.ApplyHook(HookUpdate{Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0", AgentSessionID: "s1", Status: session.StatusIdle})
	// A later hook for same claude id but reporting no pane still finds it.
	got, alive := r.ApplyHook(HookUpdate{Agent: "claude", AgentSessionID: "s1", Status: session.StatusWorking})
	if !alive || got.Status != session.StatusWorking {
		t.Fatalf("expected correlation by claude id: %+v", got)
	}
	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("want 1 session, got %d", n)
	}
}

func TestApplyHookStoresFrontend(t *testing.T) {
	r := New()
	s, alive := r.ApplyHook(HookUpdate{
		Agent: "claude", AgentSessionID: "vs1",
		Frontend: session.FrontendVSCode, Status: session.StatusIdle,
	})
	if !alive || s.Frontend != session.FrontendVSCode || s.Controllable() {
		t.Fatalf("want paneless vscode session, got frontend=%q controllable=%v", s.Frontend, s.Controllable())
	}
}

func TestApplyHookNeverDowngradesTmuxFrontend(t *testing.T) {
	r := New()
	// First: a tmux session via hook (pane present).
	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "s1", Frontend: session.FrontendTmux, Status: session.StatusIdle,
	})
	// Then: a later hook correlated by claude id arrives paneless/vscode — must NOT downgrade.
	s, _ := r.ApplyHook(HookUpdate{
		Agent: "claude", AgentSessionID: "s1",
		Frontend: session.FrontendVSCode, Status: session.StatusWorking,
	})
	if s.Frontend != session.FrontendTmux {
		t.Fatalf("frontend downgraded to %q, want tmux", s.Frontend)
	}
}

// A /clear reuses the pane record but swaps AgentSessionID + transcript path: the
// stale summary must reset and the superseded claude id must leave the index.
func TestApplyHookTranscriptSwapResetsSummaryAndClaudeIndex(t *testing.T) {
	r := New()
	r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "c0", TranscriptPath: "/tmp/c0.jsonl",
		Status: session.StatusIdle, Summary: &session.Summary{Task: "pre-clear"},
	})
	got, _ := r.ApplyHook(HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%0",
		AgentSessionID: "c1", TranscriptPath: "/tmp/c1.jsonl",
		Status: session.StatusAwaitingInput,
	})
	if got.Summary != nil {
		t.Fatalf("transcript swap must reset the stale summary, got %+v", got.Summary)
	}
	if got.TranscriptPath != "/tmp/c1.jsonl" || got.AgentSessionID != "c1" {
		t.Fatalf("swap not applied: %+v", got)
	}
	if _, ok := r.index.findByAgentSession("c0"); ok {
		t.Error("superseded claude id should be cleared from the index")
	}
	if id, ok := r.index.findByAgentSession("c1"); !ok || id != got.ID {
		t.Errorf("new claude id should resolve to the record: %q %v", id, ok)
	}
}
