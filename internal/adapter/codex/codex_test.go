package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestStatusFor(t *testing.T) {
	cases := map[string]session.Status{
		"SessionStart":     session.StatusIdle,
		"UserPromptSubmit": session.StatusWorking,
		"PreToolUse":       session.StatusWorking,
		"PostToolUse":      session.StatusWorking,
		"Stop":             session.StatusAwaitingInput,
	}
	for event, want := range cases {
		got, ok := statusFor(event)
		if !ok || got != want {
			t.Errorf("statusFor(%q) = %q,%v; want %q,true", event, got, ok, want)
		}
	}
	if _, ok := statusFor("Nonsense"); ok {
		t.Error("statusFor(unknown) should report ok=false")
	}
}

// codexHook builds a HookEvent with a Codex-shaped payload.
func codexHook(pane string, payload map[string]any) adapter.HookEvent {
	b, _ := json.Marshal(payload)
	return adapter.HookEvent{TmuxPane: pane, Payload: b}
}

func TestEventNameAndPermissionPayload(t *testing.T) {
	ev := codexHook("%1", map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "shell",
		"tool_input":      map[string]any{"command": "ls"},
	})
	if got := EventName(ev); got != "PreToolUse" {
		t.Errorf("EventName = %q; want PreToolUse", got)
	}
	name, input := PermissionPayload(ev)
	if name != "shell" {
		t.Errorf("PermissionPayload tool = %q; want shell", name)
	}
	if !strings.Contains(string(input), "ls") {
		t.Errorf("PermissionPayload input = %s; want it to carry the command", input)
	}
}

func TestProcessHookAppliesStatusAndFields(t *testing.T) {
	reg := registry.New()
	ev := codexHook("%7", map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      "sess-123",
		"cwd":             "/home/u/proj",
		"transcript_path": "/home/u/.codex/sessions/2026/01/01/rollout-x.jsonl",
	})
	s, alive := ProcessHook(reg, ev)
	if !alive {
		t.Fatal("session should exist after PreToolUse")
	}
	if s.Tool != Tool {
		t.Errorf("Tool = %q; want %q", s.Tool, Tool)
	}
	if s.Status != session.StatusWorking {
		t.Errorf("Status = %q; want working", s.Status)
	}
	if s.Tmux.PaneID != "%7" {
		t.Errorf("PaneID = %q; want %%7", s.Tmux.PaneID)
	}
	if s.TranscriptPath == "" || s.Cwd != "/home/u/proj" {
		t.Errorf("cwd/transcript not applied: cwd=%q transcript=%q", s.Cwd, s.TranscriptPath)
	}
}

func TestProcessHookStopAwaitsInput(t *testing.T) {
	reg := registry.New()
	// Prime the session as working, then Stop should flip it to awaiting + idle prompt.
	ProcessHook(reg, codexHook("%9", map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "s"}))
	s, alive := ProcessHook(reg, codexHook("%9", map[string]any{"hook_event_name": "Stop", "session_id": "s"}))
	if !alive || s.Status != session.StatusAwaitingInput {
		t.Fatalf("after Stop: alive=%v status=%q; want awaiting_input", alive, s.Status)
	}
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Errorf("Stop should set an idle interaction, got %+v", s.Interaction)
	}
}

func TestParseCodexProcs(t *testing.T) {
	psOut := strings.Join([]string{
		"  100 ttys001 S+ /usr/bin/codex",
		"  101 ttys002 S+ /opt/homebrew/bin/codex --model gpt-5",
		"  102 ??      S  /usr/bin/claude", // wrong tool
		"  103 ttys003 S  vim",             // unrelated
	}, "\n")
	got := parseCodexProcs(psOut)
	if len(got) != 2 {
		t.Fatalf("parsed %d codex procs; want 2 (%v)", len(got), got)
	}
	if got[100] != "ttys001" || got[101] != "ttys002" {
		t.Errorf("tty correlation wrong: %v", got)
	}
}

func TestBuildDiscoveredCorrelatesPane(t *testing.T) {
	procs := map[int]string{100: "ttys001", 200: ""} // 200 ttyless → skipped
	panes := map[string]paneInfo{
		"ttys001": {server: session.TmuxServerDefault, paneID: "%3", sessionName: "work", currentPath: "/home/u/proj"},
	}
	got := buildDiscovered(procs, panes)
	if len(got) != 1 {
		t.Fatalf("discovered %d; want 1", len(got))
	}
	d := got[0]
	if !d.HasPane || d.PaneID != "%3" || d.Cwd != "/home/u/proj" || d.Frontend != session.FrontendTmux {
		t.Errorf("unexpected discovered session: %+v", d)
	}
}

func TestInstallerRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	path, err := SettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "hooks.json"); path != want {
		t.Fatalf("SettingsPath = %q; want %q", path, want)
	}

	if err := Install("/usr/local/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	for _, event := range DefaultHookEvents {
		if !strings.Contains(content, event) {
			t.Errorf("hooks.json missing event %q", event)
		}
	}
	if !strings.Contains(content, "hook --tool codex") {
		t.Error("hooks.json missing the managed codex command")
	}

	// Idempotent: a second install must not duplicate managed entries.
	if err := Install("/usr/local/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	hooks, err := loadHooks(path)
	if err != nil {
		t.Fatal(err)
	}
	for event, groups := range hooks {
		managed := 0
		for _, g := range groups {
			for _, c := range g.Hooks {
				if isManaged(c) {
					managed++
				}
			}
		}
		if managed != 1 {
			t.Errorf("event %q has %d managed entries; want 1", event, managed)
		}
	}

	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	hooks, err = loadHooks(path)
	if err != nil {
		t.Fatal(err)
	}
	if anyManaged(hooks) {
		t.Error("Uninstall left managed hooks behind")
	}
}

func TestInstallerPreservesUserHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path, _ := SettingsPath()

	// A pre-existing user hook that argus must not touch.
	user := map[string][]hookGroup{
		"PreToolUse": {{Hooks: []hookCmd{{Type: "command", Command: "/usr/bin/user-script"}}}},
	}
	b, _ := json.MarshalIndent(user, "", "  ")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Install("/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	hooks, err := loadHooks(path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range hooks["PreToolUse"] {
		for _, c := range g.Hooks {
			if c.Command == "/usr/bin/user-script" {
				found = true
			}
		}
	}
	if !found {
		t.Error("user hook was lost across install/uninstall")
	}
}

func TestReconcileOnlyWhenInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	// No file yet: reconcile is a no-op (opt-in preserved).
	if added, err := ReconcileIfInstalled("/bin/argus"); err != nil || len(added) != 0 {
		t.Fatalf("reconcile with no hooks.json: added=%v err=%v; want none", added, err)
	}
	// After install, reconcile with the same bin needs no changes.
	if err := Install("/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	if added, err := ReconcileIfInstalled("/bin/argus"); err != nil || len(added) != 0 {
		t.Fatalf("reconcile after matching install: added=%v err=%v; want none", added, err)
	}
}
