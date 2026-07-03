package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestStatusFor(t *testing.T) {
	cases := map[string]session.Status{
		"SessionStart":     session.StatusIdle,
		"UserPromptSubmit": session.StatusWorking,
		"PreToolUse":       session.StatusWorking,
		"PostToolUse":      session.StatusWorking,
		"Notification":     session.StatusAwaitingInput,
		"Stop":             session.StatusAwaitingInput,
		"SessionEnd":       session.StatusDead,
	}
	for event, want := range cases {
		got, ok := statusFor(event)
		if !ok || got != want {
			t.Errorf("%s: want %q, got %q (ok=%v)", event, want, got, ok)
		}
	}
	if _, ok := statusFor("Whatever"); ok {
		t.Error("unknown event should not set status")
	}
}

func TestServerFromSocket(t *testing.T) {
	if serverFromSocket("argus") != session.TmuxServerArgus {
		t.Error("argus socket -> argus server")
	}
	if serverFromSocket("/private/tmp/tmux-501/default") != session.TmuxServerDefault {
		t.Error("default socket -> default server")
	}
}

func TestProcessHookDrivesStatus(t *testing.T) {
	reg := registry.New()
	reg.ReconcileSessions(Tool, []registry.DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", SessionName: "a", Frontend: session.FrontendTmux},
	})

	mk := func(event string) HookEvent {
		payload, _ := json.Marshal(map[string]string{
			"session_id":      "sess-1",
			"transcript_path": "/t/sess-1.jsonl",
			"cwd":             "/work",
		})
		return HookEvent{Event: event, TmuxPane: "%0", TmuxSocket: "default", Payload: payload}
	}

	got, _ := ProcessHook(reg, mk("UserPromptSubmit"))
	if got.Status != session.StatusWorking {
		t.Fatalf("want working, got %q", got.Status)
	}
	got, _ = ProcessHook(reg, mk("Notification"))
	if got.Status != session.StatusAwaitingInput {
		t.Fatalf("want awaiting_input, got %q", got.Status)
	}
	got, _ = ProcessHook(reg, mk("Stop"))
	if got.Status != session.StatusAwaitingInput {
		t.Fatalf("want awaiting_input, got %q", got.Status)
	}
	if got.ClaudeSessionID != "sess-1" || got.TranscriptPath == "" {
		t.Fatalf("hook fields not propagated: %+v", got)
	}

	_, alive := ProcessHook(reg, mk("SessionEnd"))
	if alive {
		t.Fatal("SessionEnd should remove the session")
	}
}

func TestProcessHookClearSurfacesRespondPrompt(t *testing.T) {
	reg := registry.New()
	reg.ReconcileSessions(Tool, []registry.DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", SessionName: "a", Frontend: session.FrontendTmux},
	})
	hook := func(event string, payload map[string]any) HookEvent {
		raw, _ := json.Marshal(payload)
		return HookEvent{Event: event, TmuxPane: "%0", TmuxSocket: "default", Payload: raw}
	}

	// Drive the session into awaiting-input with a pending plan interaction.
	s, _ := ProcessHook(reg, hook("PreToolUse", map[string]any{
		"tool_name":  "ExitPlanMode",
		"tool_input": map[string]any{"plan": "do the thing"},
	}))
	if s.Status != session.StatusAwaitingInput || s.Interaction == nil {
		t.Fatalf("setup: want awaiting with interaction, got %v / %+v", s.Status, s.Interaction)
	}

	// /clear = SessionEnd(reason=clear) then SessionStart(source=clear): end goes
	// idle without removing the session, start lands fresh on awaiting-input.
	mid, alive := ProcessHook(reg, hook("SessionEnd", map[string]any{"reason": "clear"}))
	if !alive {
		t.Fatal("SessionEnd(reason=clear) must not remove the session")
	}
	if mid.Status != session.StatusIdle {
		t.Fatalf("want idle after SessionEnd(clear), got %q", mid.Status)
	}
	if mid.Interaction != nil {
		t.Fatalf("SessionEnd(clear) must drop the stale prompt, got %+v", mid.Interaction)
	}

	// SessionStart(source=clear) brings up the fresh session's respond prompt.
	got, alive := ProcessHook(reg, hook("SessionStart", map[string]any{"source": "clear"}))
	if !alive {
		t.Fatal("SessionStart(source=clear) must keep the session")
	}
	if got.Status != session.StatusAwaitingInput {
		t.Fatalf("want awaiting-input after SessionStart(clear), got %q", got.Status)
	}
	if got.Interaction == nil || got.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("respond prompt must show after SessionStart(clear), got %+v", got.Interaction)
	}
}

func TestProcessHookSessionStartStartupIsIdle(t *testing.T) {
	reg := registry.New()
	reg.ReconcileSessions(Tool, []registry.DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", SessionName: "a", Frontend: session.FrontendTmux},
	})
	raw, _ := json.Marshal(map[string]any{"source": "startup"})
	got, alive := ProcessHook(reg, HookEvent{Event: "SessionStart", TmuxPane: "%0", TmuxSocket: "default", Payload: raw})
	if !alive {
		t.Fatal("SessionStart(source=startup) must keep the session")
	}
	// A genuinely fresh session is idle with no pending prompt — only /clear surfaces one.
	if got.Status != session.StatusIdle {
		t.Fatalf("want idle after startup, got %q", got.Status)
	}
	if got.Interaction != nil {
		t.Fatalf("startup must not synthesize a respond prompt, got %+v", got.Interaction)
	}
}

func TestProcessHookSessionStartResumeSurfacesRespondPrompt(t *testing.T) {
	reg := registry.New()
	reg.ReconcileSessions(Tool, []registry.DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", SessionName: "a", Frontend: session.FrontendTmux},
	})
	raw, _ := json.Marshal(map[string]any{"source": "resume"})
	got, alive := ProcessHook(reg, HookEvent{Event: "SessionStart", TmuxPane: "%0", TmuxSocket: "default", Payload: raw})
	if !alive {
		t.Fatal("SessionStart(source=resume) must keep the session")
	}
	// A resumed session is waiting for the user to continue: surface awaiting-input
	// with the compose prompt, like /clear.
	if got.Status != session.StatusAwaitingInput {
		t.Fatalf("want awaiting-input after resume, got %q", got.Status)
	}
	if got.Interaction == nil || got.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("respond prompt must show after SessionStart(resume), got %+v", got.Interaction)
	}
}

func TestProcessHookSessionEndRemovesOnGenuineEnd(t *testing.T) {
	reg := registry.New()
	reg.ReconcileSessions(Tool, []registry.DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", SessionName: "a", Frontend: session.FrontendTmux},
	})
	end := func(reason string) HookEvent {
		raw, _ := json.Marshal(map[string]any{"reason": reason})
		return HookEvent{Event: "SessionEnd", TmuxPane: "%0", TmuxSocket: "default", Payload: raw}
	}
	if _, alive := ProcessHook(reg, end("logout")); alive {
		t.Fatal("SessionEnd(reason=logout) should remove the session")
	}
}

func TestInteractionDetection(t *testing.T) {
	reg := registry.New()
	reg.ReconcileSessions(Tool, []registry.DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", SessionName: "a", Frontend: session.FrontendTmux},
	})
	hook := func(event string, payload map[string]any) HookEvent {
		raw, _ := json.Marshal(payload)
		return HookEvent{Event: event, TmuxPane: "%0", TmuxSocket: "default", Payload: raw}
	}

	// Notification → idle "awaiting input" interaction (never a permission; those
	// are owned by the PermissionRequest hook).
	s, _ := ProcessHook(reg, hook("Notification", map[string]any{
		"message": "Claude needs your permission to use Bash",
	}))
	if s.Status != session.StatusAwaitingInput || s.Interaction == nil ||
		s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("notification idle: %+v / %+v", s.Status, s.Interaction)
	}

	// PreToolUse(AskUserQuestion) → question interaction with parsed options.
	s, _ = ProcessHook(reg, hook("PreToolUse", map[string]any{
		"tool_name": "AskUserQuestion",
		"tool_input": map[string]any{"questions": []map[string]any{{
			"header":      "Choice",
			"question":    "Pick one",
			"multiSelect": false,
			"options": []map[string]any{
				{"label": "A", "description": "first choice", "preview": "preview-A"},
				{"label": "B", "description": "second choice", "preview": "preview-B"},
			},
		}}},
	}))
	if s.Status != session.StatusAwaitingInput || s.Interaction == nil ||
		s.Interaction.Kind != session.InteractionQuestion ||
		len(s.Interaction.Questions) != 1 {
		t.Fatalf("question: %+v", s.Interaction)
	}
	q := s.Interaction.Questions[0]
	if q.Header != "Choice" || q.Question != "Pick one" || len(q.Options) != 2 {
		t.Fatalf("question[0]: %+v", q)
	}
	if len(q.OptionDescriptions) != 2 ||
		q.OptionDescriptions[0] != "first choice" || q.OptionDescriptions[1] != "second choice" {
		t.Fatalf("question descriptions: %+v", q.OptionDescriptions)
	}
	if len(q.OptionPreviews) != 2 ||
		q.OptionPreviews[0] != "preview-A" || q.OptionPreviews[1] != "preview-B" {
		t.Fatalf("question previews: %+v", q.OptionPreviews)
	}

	// PreToolUse(ExitPlanMode) → plan interaction.
	s, _ = ProcessHook(reg, hook("PreToolUse", map[string]any{
		"tool_name":  "ExitPlanMode",
		"tool_input": map[string]any{"plan": "do the thing"},
	}))
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionPlan ||
		s.Interaction.Plan != "do the thing" {
		t.Fatalf("plan: %+v", s.Interaction)
	}

	// A normal tool keeps working and clears any prior interaction.
	s, _ = ProcessHook(reg, hook("PreToolUse", map[string]any{"tool_name": "Bash"}))
	if s.Status != session.StatusWorking || s.Interaction != nil {
		t.Fatalf("normal tool: status=%v interaction=%+v", s.Status, s.Interaction)
	}

	// Stop marks the session waiting for input with an idle prompt, replacing any
	// stale prompt the user may have already answered in their own terminal (here
	// a plan from the ExitPlanMode above).
	s, _ = ProcessHook(reg, hook("PreToolUse", map[string]any{
		"tool_name":  "ExitPlanMode",
		"tool_input": map[string]any{"plan": "stale plan"},
	}))
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionPlan {
		t.Fatalf("expected a plan interaction before stop, got %+v", s.Interaction)
	}
	s, _ = ProcessHook(reg, hook("Stop", map[string]any{}))
	if s.Status != session.StatusAwaitingInput ||
		s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("stop: status=%v interaction=%+v", s.Status, s.Interaction)
	}
}

func TestPermissionInteractionHasOptions(t *testing.T) {
	p := hookPayload{ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`)}
	ix := permissionInteraction(p, false)
	if ix == nil || ix.Kind != session.InteractionPermission {
		t.Fatalf("want permission interaction, got %+v", ix)
	}
	if len(ix.Options) != 2 {
		t.Fatalf("want 2 options, got %d", len(ix.Options))
	}
	if ix.Options[0].Value != "allow" || ix.Options[1].Value != "deny" {
		t.Fatalf("unexpected option values: %+v", ix.Options)
	}
	if !ix.Options[1].Reject {
		t.Fatalf("deny option must be the reject choice")
	}
}

func TestFrontendFor(t *testing.T) {
	cases := []struct {
		entrypoint string
		hasPane    bool
		want       session.Frontend
	}{
		{"cli", true, session.FrontendTmux},
		{"cli", false, session.FrontendExternal},
		{"claude-vscode", false, session.FrontendVSCode},
		{"claude-vscode", true, session.FrontendVSCode},
		{"", true, session.FrontendTmux},      // unknown entrypoint trusts the pane
		{"", false, session.FrontendExternal}, // unknown, no pane
	}
	for _, c := range cases {
		if got := frontendFor(c.entrypoint, c.hasPane); got != c.want {
			t.Errorf("frontendFor(%q,%v)=%q want %q", c.entrypoint, c.hasPane, got, c.want)
		}
	}
}

func TestProcessHookVSCodeIgnoresInheritedPane(t *testing.T) {
	dir := t.TempDir()
	claudeSessionsDirOverride = dir
	t.Cleanup(func() { claudeSessionsDirOverride = "" })
	if err := os.WriteFile(filepath.Join(dir, "111.json"),
		[]byte(`{"pid":111,"sessionId":"vs-1","entrypoint":"claude-vscode"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := registry.New()
	payload := []byte(`{"session_id":"vs-1","hook_event_name":"UserPromptSubmit"}`)
	s, alive := ProcessHook(reg, HookEvent{Event: "UserPromptSubmit", TmuxPane: "%5", TmuxSocket: "default", Payload: payload})
	if !alive {
		t.Fatal("session should be alive")
	}
	if s.Tmux.PaneID != "" {
		t.Fatalf("vscode session must be paneless, got pane %q", s.Tmux.PaneID)
	}
	if s.Frontend != session.FrontendVSCode {
		t.Fatalf("want vscode frontend, got %q", s.Frontend)
	}
	if s.ID != "claude:vs-1" {
		t.Fatalf("want claude:vs-1 key, got %q", s.ID)
	}

	// A discovery scan that does not list pane %5 must NOT prune the session;
	// the unified scan still reports the live VSCode session (by claude id).
	reg.ReconcileSessions(Tool, []registry.DiscoveredSession{
		{ClaudeSessionID: "vs-1", Frontend: session.FrontendVSCode},
	})
	if _, ok := reg.Get("claude:vs-1"); !ok {
		t.Fatal("vscode session was pruned by reconcile — regression")
	}
}

func TestProcessHookCLIKeepsPane(t *testing.T) {
	dir := t.TempDir()
	claudeSessionsDirOverride = dir
	t.Cleanup(func() { claudeSessionsDirOverride = "" })
	if err := os.WriteFile(filepath.Join(dir, "222.json"),
		[]byte(`{"pid":222,"sessionId":"cli-1","entrypoint":"cli"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := registry.New()
	payload := []byte(`{"session_id":"cli-1","hook_event_name":"UserPromptSubmit"}`)
	s, alive := ProcessHook(reg, HookEvent{Event: "UserPromptSubmit", TmuxPane: "%7", TmuxSocket: "default", Payload: payload})
	if !alive {
		t.Fatal("session should be alive")
	}
	if s.Tmux.PaneID != "%7" {
		t.Fatalf("cli session must keep its pane, got %q", s.Tmux.PaneID)
	}
	if s.Frontend != session.FrontendTmux {
		t.Fatalf("want tmux frontend, got %q", s.Frontend)
	}
}

func TestProcessHookUnknownEntrypointTrustsPane(t *testing.T) {
	dir := t.TempDir()
	claudeSessionsDirOverride = dir
	t.Cleanup(func() { claudeSessionsDirOverride = "" })
	// No proc-session file for this session id.

	reg := registry.New()
	payload := []byte(`{"session_id":"ghost","hook_event_name":"UserPromptSubmit"}`)
	s, alive := ProcessHook(reg, HookEvent{Event: "UserPromptSubmit", TmuxPane: "%9", TmuxSocket: "default", Payload: payload})
	if !alive {
		t.Fatal("session should be alive")
	}
	if s.Tmux.PaneID != "%9" {
		t.Fatalf("unknown entrypoint must trust the pane, got %q", s.Tmux.PaneID)
	}
	if s.Frontend != session.FrontendTmux {
		t.Fatalf("want tmux frontend, got %q", s.Frontend)
	}
}

func TestNotificationAlwaysIdle(t *testing.T) {
	reg := registry.New()
	reg.ReconcileSessions(Tool, []registry.DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: "%0", SessionName: "a", Frontend: session.FrontendTmux},
	})
	notify := func(msg string) session.Session {
		payload, _ := json.Marshal(map[string]any{"message": msg})
		s, _ := ProcessHook(reg, HookEvent{Event: "Notification", TmuxPane: "%0", TmuxSocket: "default", Payload: payload})
		return s
	}

	for _, msg := range []string{"Claude needs your permission to use Bash", "Claude is waiting for your input", ""} {
		s := notify(msg)
		if s.Status != session.StatusAwaitingInput || s.Interaction == nil ||
			s.Interaction.Kind != session.InteractionIdle {
			t.Fatalf("notification %q: status=%v interaction=%+v", msg, s.Status, s.Interaction)
		}
		if s.Interaction.ToolName != "" || s.Interaction.ToolInput != "" || len(s.Interaction.Questions) != 0 {
			t.Fatalf("notification %q leaked tool details: %+v", msg, s.Interaction)
		}
		if s.Interaction.Message != msg {
			t.Errorf("notification %q message = %q", msg, s.Interaction.Message)
		}
	}
}
