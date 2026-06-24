package registry

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestApplyHookEnrichesDiscoveredSession(t *testing.T) {
	r := New()
	// First discovered via tmux.
	r.ReconcileDiscovered("claude-code", session.TmuxServerDefault, []DiscoveredPane{pane("%0", "a")})

	// Then a hook arrives for that pane.
	got, alive := r.ApplyHook(HookUpdate{
		Tool:            "claude-code",
		Server:          session.TmuxServerDefault,
		PaneID:          "%0",
		ClaudeSessionID: "sess-abc",
		Cwd:             "/work",
		TranscriptPath:  "/t/sess-abc.jsonl",
		Status:          session.StatusWorking,
	})
	if !alive {
		t.Fatal("session should be alive")
	}
	if got.ClaudeSessionID != "sess-abc" || got.Status != session.StatusWorking {
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
		Tool:            "claude-code",
		Server:          session.TmuxServerArgus,
		PaneID:          "%5",
		ClaudeSessionID: "z",
		Status:          session.StatusIdle,
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
	r.ReconcileDiscovered("claude-code", session.TmuxServerDefault, []DiscoveredPane{pane("%0", "a")})
	_, alive := r.ApplyHook(HookUpdate{
		Tool:   "claude-code",
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
			Tool: "claude-code", Server: session.TmuxServerDefault, PaneID: "%0",
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
			Tool: "claude-code", Server: session.TmuxServerDefault, PaneID: "%0",
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
	sum := &session.Summary{Model: "claude-opus-4-8", HasContext: true, ContextPct: 42, Task: "do the thing"}
	got, _ := r.ApplyHook(HookUpdate{
		Tool: "claude-code", Server: session.TmuxServerDefault, PaneID: "%0",
		Repo: "argus", Status: session.StatusWorking, Summary: sum,
	})
	if got.Summary == nil || got.Summary.Model != "claude-opus-4-8" || got.Repo != "argus" {
		t.Fatalf("summary/repo not stored: %+v repo=%q", got.Summary, got.Repo)
	}

	// A status-only update with no summary keeps the cached one (and repo).
	got, _ = r.ApplyHook(HookUpdate{
		Tool: "claude-code", Server: session.TmuxServerDefault, PaneID: "%0",
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
	r.ApplyHook(HookUpdate{Tool: "claude-code", Server: session.TmuxServerDefault, PaneID: "%0", ClaudeSessionID: "s1", Status: session.StatusIdle})
	// A later hook for same claude id but reporting no pane still finds it.
	got, alive := r.ApplyHook(HookUpdate{Tool: "claude-code", ClaudeSessionID: "s1", Status: session.StatusWorking})
	if !alive || got.Status != session.StatusWorking {
		t.Fatalf("expected correlation by claude id: %+v", got)
	}
	if n := len(r.Snapshot()); n != 1 {
		t.Fatalf("want 1 session, got %d", n)
	}
}

// Discovery enrichment: a DiscoveredPane carrying Claude-side fields populates the
// session, registers the byClaude index (so a paneless hook still correlates), and
// a later nil-summary reconcile must not wipe the computed summary.
func TestReconcileDiscoveredEnriches(t *testing.T) {
	r := New()
	sum := &session.Summary{Model: "claude-opus-4-8", HasContext: true, ContextPct: 12}
	r.ReconcileDiscovered("claude-code", session.TmuxServerDefault, []DiscoveredPane{{
		Tool: "claude-code", Server: session.TmuxServerDefault, PaneID: "%0",
		SessionName: "tmux-name", ClaudeSessionID: "sess-1", Name: "claude-name",
		Cwd: "/repo/argus", TranscriptPath: "/t/sess-1.jsonl", Summary: sum,
	}})

	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 session, got %d", len(snap))
	}
	s := snap[0]
	if s.ClaudeSessionID != "sess-1" || s.Name != "claude-name" ||
		s.Cwd != "/repo/argus" || s.TranscriptPath != "/t/sess-1.jsonl" || s.Summary == nil {
		t.Fatalf("enrichment missing: %+v", s)
	}

	// A hook with no pane but a matching claude id correlates to the same session.
	got, ok := r.ApplyHook(HookUpdate{Tool: "claude-code", ClaudeSessionID: "sess-1", Status: session.StatusWorking})
	if !ok || got.ID != s.ID {
		t.Fatalf("hook should correlate by claude id: got %+v ok=%v", got, ok)
	}

	// A nil-summary reconcile keeps the existing summary.
	r.ReconcileDiscovered("claude-code", session.TmuxServerDefault, []DiscoveredPane{{
		Tool: "claude-code", Server: session.TmuxServerDefault, PaneID: "%0", ClaudeSessionID: "sess-1",
	}})
	if r.Snapshot()[0].Summary == nil {
		t.Error("nil-summary reconcile should not wipe the existing summary")
	}
}
