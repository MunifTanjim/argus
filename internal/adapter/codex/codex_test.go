package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestShouldBlockOnlyPermissionRequest(t *testing.T) {
	block := ShouldBlock(HookEvent{Event: "PermissionRequest"})
	if !block {
		t.Fatal("PermissionRequest must block")
	}
	if ShouldBlock(HookEvent{Event: "PreToolUse"}) {
		t.Fatal("PreToolUse must not block for codex")
	}
}

func TestStatusFor(t *testing.T) {
	cases := map[string]session.Status{
		"SessionStart":      session.StatusIdle,
		"UserPromptSubmit":  session.StatusWorking,
		"PreToolUse":        session.StatusWorking,
		"PostToolUse":       session.StatusWorking,
		"PermissionRequest": session.StatusAwaitingInput,
		"Stop":              session.StatusAwaitingInput,
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
	if s.Agent != Agent {
		t.Errorf("Agent = %q; want %q", s.Agent, Agent)
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
	ProcessHook(reg, codexHook("%9", map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "s"}))
	s, alive := ProcessHook(reg, codexHook("%9", map[string]any{"hook_event_name": "Stop", "session_id": "s"}))
	if !alive || s.Status != session.StatusAwaitingInput {
		t.Fatalf("after Stop: alive=%v status=%q; want awaiting_input", alive, s.Status)
	}
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Errorf("Stop should set an idle interaction, got %+v", s.Interaction)
	}
}

func TestPermissionInteractionGeneric(t *testing.T) {
	p := hookPayload{
		ToolName:  "exec_command",
		ToolInput: json.RawMessage(`{"cmd":"rm -rf /tmp/x","description":"clean up temp file"}`),
	}
	ix := permissionInteraction(p)
	if ix.Kind != session.InteractionPermission {
		t.Fatalf("Kind = %q; want permission", ix.Kind)
	}
	if ix.ToolName != "exec_command" {
		t.Errorf("ToolName = %q; want exec_command", ix.ToolName)
	}
	if ix.ToolInput == "" {
		t.Error("ToolInput should carry the raw tool_input JSON")
	}
	if ix.Message != "clean up temp file" {
		t.Errorf("Message = %q; want the tool_input.description", ix.Message)
	}
	if len(ix.Options) != 2 {
		t.Fatalf("want 2 options (Allow/Deny), got %+v", ix.Options)
	}
	if ix.Options[0].Value != "allow" {
		t.Errorf("Options[0].Value = %q; want allow", ix.Options[0].Value)
	}
	if ix.Options[1].Value != "deny" || !ix.Options[1].Reject {
		t.Errorf("Options[1] = %+v; want deny + Reject", ix.Options[1])
	}
}

func TestPermissionInteractionWithoutDescription(t *testing.T) {
	p := hookPayload{
		ToolName:  "apply_patch",
		ToolInput: json.RawMessage(`{"input":"*** Begin Patch\n*** End Patch\n"}`),
	}
	ix := permissionInteraction(p)
	if ix.Message != "" {
		t.Errorf("Message = %q; want empty when tool_input has no description field", ix.Message)
	}
}

func TestProcessHookPermissionRequestAwaitsInput(t *testing.T) {
	reg := registry.New()
	s, alive := ProcessHook(reg, codexHook("%9", map[string]any{
		"hook_event_name": "PermissionRequest",
		"session_id":      "s",
		"tool_name":       "exec_command",
		"tool_input":      map[string]any{"cmd": "rm -rf /tmp/x", "description": "clean up temp file"},
	}))
	if !alive || s.Status != session.StatusAwaitingInput {
		t.Fatalf("after PermissionRequest: alive=%v status=%q; want awaiting_input", alive, s.Status)
	}
	ix := s.Interaction
	if ix == nil || ix.Kind != session.InteractionPermission {
		t.Fatalf("want a permission interaction, got %+v", ix)
	}
	if ix.ToolName != "exec_command" {
		t.Errorf("ToolName = %q; want exec_command", ix.ToolName)
	}
	if ix.Message != "clean up temp file" {
		t.Errorf("Message = %q; want the tool_input.description", ix.Message)
	}
}

func TestBuildDiscovered(t *testing.T) {
	snaps := []snapshot{
		{threadID: "t-pane", paneID: "%3", socketPath: "/tmp/tmux-501/default"},
		{threadID: "t-gone", paneID: "%9", socketPath: "/tmp/tmux-501/default"},
		{threadID: "t-ext"},
	}
	meta := map[string]threadMeta{
		"t-pane": {rolloutPath: "/r/pane.jsonl", cwd: "/db/cwd", model: "gpt-5.5", title: "do a thing", tokens: 42},
		"t-ext":  {rolloutPath: "/r/ext.jsonl", cwd: "/home/u/proj", model: "gpt-5"},
	}
	panes := map[string]paneInfo{
		paneKey(session.TmuxServerDefault, "%3"): {server: session.TmuxServerDefault, paneID: "%3", sessionName: "work", currentPath: "/pane/cwd"},
	}

	got := buildDiscovered(snaps, meta, panes, nil, nil)
	if len(got) != 3 {
		t.Fatalf("discovered %d; want 3: %+v", len(got), got)
	}
	byID := map[string]registry.DiscoveredSession{}
	for _, d := range got {
		byID[d.AgentSessionID] = d
	}

	pane, ok := byID["t-pane"]
	if !ok {
		t.Fatal("pane-bound session missing")
	}
	if !pane.HasPane || pane.PaneID != "%3" || pane.Frontend != session.FrontendTmux {
		t.Errorf("pane-bound fields wrong: %+v", pane)
	}
	if pane.TranscriptPath != "/r/pane.jsonl" {
		t.Errorf("transcript = %q; want rollout from DB", pane.TranscriptPath)
	}
	if pane.Cwd != "/pane/cwd" { // pane current path wins over DB cwd
		t.Errorf("cwd = %q; want pane current path", pane.Cwd)
	}
	if pane.Summary == nil || pane.Summary.ModelName != "gpt-5.5" || pane.Summary.Tokens != 42 || pane.Summary.Task != "do a thing" {
		t.Errorf("summary not populated from DB: %+v", pane.Summary)
	}

	ext, ok := byID["t-ext"]
	if !ok {
		t.Fatal("paneless session missing (should be tracked, not dropped)")
	}
	if ext.HasPane || ext.Frontend != session.FrontendExternal {
		t.Errorf("paneless fields wrong: %+v", ext)
	}
	if ext.Cwd != "/home/u/proj" || ext.TranscriptPath != "/r/ext.jsonl" {
		t.Errorf("paneless meta not applied: %+v", ext)
	}

	gone, ok := byID["t-gone"]
	if !ok {
		t.Fatal("pane-gone session missing")
	}
	if gone.HasPane || gone.Frontend != session.FrontendExternal {
		t.Errorf("a snapshot whose pane is gone should fall through to external: %+v", gone)
	}
}

func TestSummaryForResolvesModelName(t *testing.T) {
	names := map[string]string{"gpt-5.5": "GPT-5.5"}

	s := summaryFor(threadMeta{model: "gpt-5.5", title: "t", tokens: 5}, names)
	if s == nil || s.ModelName != "GPT-5.5" || s.ModelColor != modelBrandColor {
		t.Errorf("model slug not resolved to display name: %+v", s)
	}
	s = summaryFor(threadMeta{model: "gpt-x"}, names)
	if s == nil || s.ModelName != "gpt-x" {
		t.Errorf("unknown slug should fall back to raw: %+v", s)
	}
	if s := summaryFor(threadMeta{}, names); s != nil {
		t.Errorf("empty meta should yield nil summary, got %+v", s)
	}
}

func TestLoadModelNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	body := `{"models":[
		{"slug":"gpt-5.5","display_name":"GPT-5.5"},
		{"slug":"gpt-5","display_name":"GPT-5"},
		{"slug":"","display_name":"skip"}
	]}`
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadModelNames()
	if got["gpt-5.5"] != "GPT-5.5" || got["gpt-5"] != "GPT-5" {
		t.Errorf("unexpected model map: %v", got)
	}
	if _, ok := got[""]; ok {
		t.Error("blank slug should be skipped")
	}

	t.Setenv("CODEX_HOME", t.TempDir())
	if got := loadModelNames(); got != nil {
		t.Errorf("missing cache should yield nil, got %v", got)
	}
}

func TestClassifyLiveness(t *testing.T) {
	snaps := []snapshot{
		{threadID: "alive-pane", paneID: "%1", socketPath: "/s/default"},
		{threadID: "dead"},       // pid known but not in procs → dropped
		{threadID: "paneless"},   // pid alive, tty maps to a pane → fallback
		{threadID: "unverified"}, // no pid known → kept, no fallback
	}
	pidByThread := map[string]int{"alive-pane": 10, "dead": 20, "paneless": 30}
	procs := map[int]string{10: "ttys001", 30: "/dev/ttys009"} // 20 absent → dead
	paneByTTY := map[string]paneInfo{
		"ttys009": {server: session.TmuxServerDefault, paneID: "%8"},
	}

	live, fallback := classifyLiveness(snaps, pidByThread, procs, paneByTTY)

	liveIDs := map[string]bool{}
	for _, s := range live {
		liveIDs[s.threadID] = true
	}
	if liveIDs["dead"] {
		t.Error("a thread whose pid is gone should be dropped")
	}
	for _, id := range []string{"alive-pane", "paneless", "unverified"} {
		if !liveIDs[id] {
			t.Errorf("%s should be live", id)
		}
	}
	if pi, ok := fallback["paneless"]; !ok || pi.paneID != "%8" {
		t.Errorf("paneless thread should recover pane %%8 from its pid tty, got %+v ok=%v", pi, ok)
	}
	if _, ok := fallback["alive-pane"]; ok {
		t.Errorf("alive-pane has no matching tty pane; unexpected fallback")
	}
}

func TestClassifyLivenessNoPIDsTrustsSnapshots(t *testing.T) {
	snaps := []snapshot{{threadID: "a", paneID: "%1"}, {threadID: "b"}}
	live, fallback := classifyLiveness(snaps, map[string]int{}, nil, map[string]paneInfo{})
	if len(live) != 2 {
		t.Errorf("with no pid info, all snapshots stay live: got %d", len(live))
	}
	if len(fallback) != 0 {
		t.Errorf("no fallback without pid info: %+v", fallback)
	}
}

func TestBuildDiscoveredPanelessFallback(t *testing.T) {
	// A snapshot with no TMUX_PANE, but the logs-pid fallback recovered a pane.
	snaps := []snapshot{{threadID: "t-ext"}}
	meta := map[string]threadMeta{"t-ext": {rolloutPath: "/r/x.jsonl", cwd: "/db/cwd"}}
	fallback := map[string]paneInfo{
		"t-ext": {server: session.TmuxServerDefault, paneID: "%8", currentPath: "/pane/cwd"},
	}

	got := buildDiscovered(snaps, meta, map[string]paneInfo{}, fallback, nil)
	if len(got) != 1 {
		t.Fatalf("got %d; want 1", len(got))
	}
	d := got[0]
	if !d.HasPane || d.PaneID != "%8" || d.Frontend != session.FrontendTmux {
		t.Errorf("fallback should bind the recovered pane: %+v", d)
	}
	if d.Cwd != "/pane/cwd" {
		t.Errorf("cwd = %q; want pane current path", d.Cwd)
	}
}

func TestParseProcessPID(t *testing.T) {
	if pid, ok := parseProcessPID("pid:49542:7e83f68c-54f4-49dc-aa0a-b81829adc712"); !ok || pid != 49542 {
		t.Errorf("parseProcessPID = %d,%v; want 49542,true", pid, ok)
	}
	for _, bad := range []string{"", "49542", "uuid:1:x", "pid:notanum:x", "pid:-1:x"} {
		if _, ok := parseProcessPID(bad); ok {
			t.Errorf("parseProcessPID(%q) should fail", bad)
		}
	}
}

func TestLastProcessPID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	db, err := sql.Open("sqlite", filepath.Join(home, "logs_2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE logs(
		id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER, ts_nanos INTEGER,
		thread_id TEXT, process_uuid TEXT)`); err != nil {
		t.Fatal(err)
	}
	// Two processes for the thread; the newer ts must win even with fewer rows.
	if _, err := db.Exec(`INSERT INTO logs(ts,ts_nanos,thread_id,process_uuid) VALUES
		(100,0,'t','pid:111:aaa'),
		(100,0,'t','pid:111:aaa'),
		(200,0,'t','pid:222:bbb'),
		(150,0,'t',NULL)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if pid, ok := lastProcessPID("t"); !ok || pid != 222 {
		t.Errorf("lastProcessPID = %d,%v; want 222,true (latest ts)", pid, ok)
	}
	if _, ok := lastProcessPID("missing"); ok {
		t.Error("unknown thread should yield ok=false")
	}
}

func TestLastProcessPIDMissingDB(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	if _, ok := lastProcessPID("t"); ok {
		t.Error("missing logs DB should yield ok=false, not panic")
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
	if !strings.Contains(content, "--agent codex") {
		t.Error("hooks.json missing the managed codex command")
	}

	// Idempotent: a second install must not duplicate managed entries.
	if err := Install("/usr/local/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	hooks, err := loadHooks()
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
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Uninstall should remove hooks.json; stat err = %v", err)
	}
}

func TestPermissionRequestGetsLongTimeout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	if err := Install("/usr/local/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	hooks, err := loadHooks()
	if err != nil {
		t.Fatal(err)
	}

	groups, ok := hooks["PermissionRequest"]
	if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
		t.Fatalf("PermissionRequest hook not installed: %+v", hooks["PermissionRequest"])
	}
	if got := groups[0].Hooks[0].Timeout; got != 1500 {
		t.Errorf("PermissionRequest timeout = %d; want 1500", got)
	}

	otherGroups, ok := hooks["PreToolUse"]
	if !ok || len(otherGroups) == 0 || len(otherGroups[0].Hooks) == 0 {
		t.Fatalf("PreToolUse hook not installed: %+v", hooks["PreToolUse"])
	}
	if got := otherGroups[0].Hooks[0].Timeout; got != 5 {
		t.Errorf("PreToolUse timeout = %d; want 5", got)
	}
}

func TestInstallerPreservesUserHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path, _ := SettingsPath()

	user := map[string]any{
		"hooks": map[string][]hookGroup{
			"PreToolUse": {{Hooks: []hookCmd{{Type: "command", Command: "/usr/bin/user-script"}}}},
		},
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
	hooks, err := loadHooks()
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

	if added, err := ReconcileIfInstalled("/bin/argus"); err != nil || len(added) != 0 {
		t.Fatalf("reconcile with no hooks.json: added=%v err=%v; want none", added, err)
	}
	if err := Install("/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	if added, err := ReconcileIfInstalled("/bin/argus"); err != nil || len(added) != 0 {
		t.Fatalf("reconcile after matching install: added=%v err=%v; want none", added, err)
	}
}

func TestReconcileAddsMissingEvents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	if err := Install("/bin/argus", []string{"SessionStart", "Stop"}); err != nil {
		t.Fatal(err)
	}
	added, err := ReconcileIfInstalled("/bin/argus")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"UserPromptSubmit": true, "PreToolUse": true, "PostToolUse": true}
	for _, e := range added {
		if e == "SessionStart" || e == "Stop" {
			t.Errorf("already-managed %q should not be in added: %v", e, added)
		}
		delete(want, e)
	}
	if len(want) != 0 {
		t.Errorf("reconcile did not add %v (added=%v)", want, added)
	}
	hooks, err := loadHooks()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range DefaultHookEvents {
		if !hasManaged(hooks[e]) {
			t.Errorf("event %q not managed after reconcile", e)
		}
	}
	if added2, err := ReconcileIfInstalled("/bin/argus"); err != nil || len(added2) != 0 {
		t.Errorf("second reconcile should be no-op: added=%v err=%v", added2, err)
	}
}

func TestServerFromSocket(t *testing.T) {
	if got := serverFromSocket("/private/tmp/tmux-501/argus"); got != session.TmuxServerArgus {
		t.Errorf("argus socket → %q; want %q", got, session.TmuxServerArgus)
	}
	if got := serverFromSocket("/private/tmp/tmux-501/default"); got != session.TmuxServerDefault {
		t.Errorf("default socket → %q; want %q", got, session.TmuxServerDefault)
	}
	if got := serverFromSocket(""); got != session.TmuxServerDefault {
		t.Errorf("empty socket → %q; want default", got)
	}
}

func TestProcessHookTracksPanelessSession(t *testing.T) {
	reg := registry.New()
	ev := codexHook("", map[string]any{"hook_event_name": "SessionStart", "session_id": "s1"})
	s, alive := ProcessHook(reg, ev)
	if !alive {
		t.Fatal("paneless hook should be tracked")
	}
	if s.Frontend != session.FrontendExternal {
		t.Errorf("paneless frontend = %q; want external", s.Frontend)
	}
	if s.Tmux.PaneID != "" {
		t.Errorf("paneless session should have no pane, got %q", s.Tmux.PaneID)
	}
}

func TestProcessHookSessionStartShowsCompose(t *testing.T) {
	reg := registry.New()
	s, alive := ProcessHook(reg, codexHook("%2", map[string]any{
		"hook_event_name": "SessionStart", "session_id": "s1",
	}))
	if !alive {
		t.Fatal("pane-bound SessionStart should create a session")
	}
	if s.Status != session.StatusIdle {
		t.Errorf("SessionStart status = %q; want idle", s.Status)
	}
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Errorf("SessionStart should show an idle compose prompt, got %+v", s.Interaction)
	}
}

type fakePane struct {
	inMode   bool
	modeErr  error
	cancErr  error
	sendErr  error
	canceled bool
	sent     []string
}

func (f *fakePane) PaneInMode(context.Context, string) (bool, error) { return f.inMode, f.modeErr }
func (f *fakePane) CancelMode(context.Context, string) error {
	f.canceled = true
	f.inMode = false
	return f.cancErr
}
func (f *fakePane) SendKeys(_ context.Context, _ string, keys ...string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, keys...)
	return nil
}

func TestPrepareTextInput(t *testing.T) {
	f := &fakePane{}
	if err := PrepareTextInput(context.Background(), f, "%1"); err != nil {
		t.Fatal(err)
	}
	if f.canceled {
		t.Error("a pane not in copy mode should not be canceled")
	}
	if want := []string{"i", "BSpace"}; !equalStrings(f.sent, want) {
		t.Errorf("want %v, got %v", want, f.sent)
	}

	f = &fakePane{inMode: true}
	if err := PrepareTextInput(context.Background(), f, "%1"); err != nil {
		t.Fatal(err)
	}
	if !f.canceled {
		t.Error("a pane in copy mode should be canceled")
	}
	if want := []string{"i", "BSpace"}; !equalStrings(f.sent, want) {
		t.Errorf("want %v, got %v", want, f.sent)
	}

	f = &fakePane{modeErr: errors.New("boom")}
	if err := PrepareTextInput(context.Background(), f, "%1"); err == nil {
		t.Error("a PaneInMode error should propagate")
	}
	if len(f.sent) != 0 {
		t.Errorf("no keys should be sent when the mode check fails, got %v", f.sent)
	}

	f = &fakePane{sendErr: errors.New("boom")}
	if err := PrepareTextInput(context.Background(), f, "%1"); err == nil {
		t.Error("a SendKeys error should propagate")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestHooksJSONNesting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	if err := Install("/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["hooks"]; !ok {
		t.Fatalf("hooks.json must nest events under a \"hooks\" key; got keys %v", top)
	}
	if _, stray := top["SessionStart"]; stray {
		t.Error("events must not sit at the top level of hooks.json")
	}
	nested := map[string][]hookGroup{}
	if err := json.Unmarshal(top["hooks"], &nested); err != nil {
		t.Fatal(err)
	}
	if !hasManaged(nested["SessionStart"]) {
		t.Error("SessionStart managed hook not written under hooks key")
	}
}

func TestConfigTOMLBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(home, "config.toml")
	seed := `model = "gpt-5"

[[hooks.PreToolUse]]
matcher = "Bash"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "/usr/bin/user-script"
statusMessage = "user check"
`
	if err := os.WriteFile(tomlPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	// config.toml has hooks → it is the active target.
	if p, err := SettingsPath(); err != nil || p != tomlPath {
		t.Fatalf("SettingsPath = %q,%v; want config.toml", p, err)
	}

	if err := Install("/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); !os.IsNotExist(err) {
		t.Error("install must not create hooks.json when config.toml owns hooks")
	}
	got, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(got)
	for _, want := range []string{"gpt-5", "/usr/bin/user-script", "user check", "--agent codex"} {
		if !strings.Contains(content, want) {
			t.Errorf("config.toml missing %q after install:\n%s", want, content)
		}
	}

	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(tomlPath)
	if err != nil {
		t.Fatalf("config.toml must not be deleted on uninstall: %v", err)
	}
	content = string(got)
	if strings.Contains(content, "--agent codex") {
		t.Error("uninstall left argus-managed hooks in config.toml")
	}
	for _, want := range []string{"gpt-5", "/usr/bin/user-script"} {
		if !strings.Contains(content, want) {
			t.Errorf("uninstall dropped user content %q:\n%s", want, content)
		}
	}
}

func TestConfigTOMLWithoutHooksUsesJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Install("/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); err != nil {
		t.Errorf("expected hooks.json when config.toml has no hooks: %v", err)
	}
}

// TestConfigTOMLStateOnlyUsesJSON guards a crash where [hooks.state] was mistaken
// for a definitions backend.
func TestConfigTOMLStateOnlyUsesJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(home, "config.toml")
	seed := `model = "gpt-5"

[hooks.state."/x/hooks.json:pre_tool_use:0:0"]
trusted_hash = "sha256:abc123"
`
	if err := os.WriteFile(tomlPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	// Only trust bookkeeping → not a definitions backend → hooks.json is used.
	if p, err := SettingsPath(); err != nil || p != filepath.Join(home, "hooks.json") {
		t.Fatalf("SettingsPath = %q,%v; want hooks.json", p, err)
	}
	if err := Install("/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); err != nil {
		t.Errorf("expected hooks.json install: %v", err)
	}
	b, _ := os.ReadFile(tomlPath)
	if !strings.Contains(string(b), "trusted_hash") {
		t.Error("[hooks.state] must be preserved")
	}
	if strings.Contains(string(b), "argus-managed") {
		t.Errorf("argus must not write into a state-only config.toml:\n%s", b)
	}
}

func TestConfigTOMLPreservesStateAlongsideDefs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(home, "config.toml")
	seed := `[hooks.state."/x/hooks.json:pre:0:0"]
trusted_hash = "sha256:keepme"

[[hooks.PreToolUse]]
matcher = "Bash"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "/usr/bin/user-script"
`
	if err := os.WriteFile(tomlPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if p, err := SettingsPath(); err != nil || p != tomlPath {
		t.Fatalf("SettingsPath = %q,%v; want config.toml (has definitions)", p, err)
	}
	if err := Install("/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(tomlPath)
	content := string(b)
	for _, want := range []string{"trusted_hash", "sha256:keepme", "/usr/bin/user-script"} {
		if !strings.Contains(content, want) {
			t.Errorf("lost %q across install/uninstall:\n%s", want, content)
		}
	}
	if strings.Contains(content, "argus-managed") {
		t.Error("argus-managed hook not removed on uninstall")
	}
}

// TestSavePreservesAngleBrackets guards against json.Marshal HTML-escaping turning
// > into >.
func TestSavePreservesAngleBrackets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path, _ := SettingsPath()

	// Seed a hooks.json with a user hook whose command has a >> redirect.
	seed := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"sh -c 'env >> /tmp/x.txt'"}]}]}}`
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Install("/usr/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `\u003e`) {
		t.Errorf("'>' escaped as \\u003e in written file:\n%s", b)
	}
	if !strings.Contains(string(b), ">> /tmp/x.txt") {
		t.Errorf("redirect not preserved verbatim:\n%s", b)
	}
}

func TestConfigTOMLPreservesUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(home, "config.toml")
	seed := `model = "gpt-5"

[mcp_servers.fs]
command = "npx"
args = ["-y", "server-filesystem"]

[[hooks.PreToolUse]]
matcher = "Bash|Edit"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "/usr/bin/user-script"
commandWindows = "user-script.exe"
timeout = 30
statusMessage = "user check"
`
	if err := os.WriteFile(tomlPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Install("/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(got)
	// Unrelated config + every documented hook field must survive the round trip.
	for _, want := range []string{
		"mcp_servers", "server-filesystem", "Bash|Edit",
		"/usr/bin/user-script", "user-script.exe", "user check", "30",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("config.toml lost %q across install/uninstall:\n%s", want, content)
		}
	}
	if strings.Contains(content, "--agent codex") {
		t.Error("argus-managed hook not removed on uninstall")
	}
}
