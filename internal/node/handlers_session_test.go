package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

type fakePrompter struct {
	adapter.Adapter
	called  bool
	gotText string
}

func (f *fakePrompter) SendPrompt(_ context.Context, _ session.Session, text string) error {
	f.called = true
	f.gotText = text
	return nil
}

type fakeNonPrompter struct{ adapter.Adapter }

type fakeSpawner struct {
	adapter.Adapter
	gotCwd, gotPrompt string
	gotModel          string
	id                string
	err               error
	models            []api.ModelInfo
	modelsErr         error
}

func (f *fakeSpawner) ListModels(context.Context) ([]api.ModelInfo, error) {
	return f.models, f.modelsErr
}

// fakePlain is an adapter without a model choice (not a ModelLister).
type fakePlain struct{ adapter.Adapter }

func (fakePlain) Agent() string     { return "plain" }
func (fakePlain) AgentName() string { return "Plain" }

func (fakeSpawner) IsHeadless() bool                                     { return true }
func (fakeSpawner) Agent() string                                        { return "fake" }
func (fakeSpawner) AgentName() string                                    { return "Fake" }
func (fakeSpawner) AgentColor() string                                   { return "#000000" }
func (fakeSpawner) SpawnCommand(adapter.SpawnOptions) (string, []string) { return "sh", nil } // sh exists in PATH
func (f *fakeSpawner) SpawnSession(_ context.Context, cwd string, opts adapter.SpawnOptions) (string, error) {
	f.gotCwd, f.gotPrompt, f.gotModel = cwd, opts.Prompt, opts.Model
	return f.id, f.err
}

func TestHandleSessionSpawnHeadlessUsesServiceNoTmux(t *testing.T) {
	fs := &fakeSpawner{id: "ses_x"}
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.caps.SpawnSession = false // no tmux; the service path must not require it
	d.adapters["fake"] = fs

	repo := t.TempDir()
	params, _ := json.Marshal(api.SpawnParams{Agent: "fake", Cwd: repo, Prompt: "hi"})
	res, err := d.handleSessionSpawn(context.Background(), params)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	sr := res.(api.SpawnResult)
	if sr.SessionID != "fake:ses_x" {
		t.Fatalf("SessionID = %q, want fake:ses_x", sr.SessionID)
	}
	if sr.PaneID != "" {
		t.Fatalf("PaneID = %q, want empty (no pane spawned)", sr.PaneID)
	}
	if fs.gotCwd != repo || fs.gotPrompt != "hi" {
		t.Fatalf("SpawnSession args: cwd=%q prompt=%q", fs.gotCwd, fs.gotPrompt)
	}
}

func TestHandleSessionSpawnHeadlessSurfacesError(t *testing.T) {
	fs := &fakeSpawner{err: errors.New("service unavailable")}
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters["fake"] = fs

	params, _ := json.Marshal(api.SpawnParams{Agent: "fake", Cwd: t.TempDir(), Prompt: "hi"})
	_, err := d.handleSessionSpawn(context.Background(), params)
	if err == nil || !strings.Contains(err.Error(), "service unavailable") {
		t.Fatalf("expected wrapped spawn error, got %v", err)
	}
}

func TestHandleAgentsListSpawnerSpawnableWithoutTmux(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.caps.SpawnSession = false
	d.adapterList = append(d.adapterList, &fakeSpawner{})

	res, err := d.handleAgentsList(context.Background(), mustSpawnJSON(t, api.AgentsListParams{}))
	if err != nil {
		t.Fatalf("agents.list: %v", err)
	}
	agents := res.(api.AgentsListResult).Agents
	byID := map[string]api.AgentInfo{}
	for _, a := range agents {
		byID[a.ID] = a
	}
	if !byID["fake"].Spawnable {
		t.Fatalf("headless Spawner must be spawnable without tmux: %+v", byID["fake"])
	}
	if byID["claude"].Spawnable {
		t.Fatalf("pane agent must not be spawnable without tmux: %+v", byID["claude"])
	}
}

func mustSpawnJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestHandleSessionSpawnNonHeadlessRequiresTmux(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.caps.SpawnSession = false

	params, _ := json.Marshal(api.SpawnParams{Agent: "claude", Cwd: t.TempDir(), Prompt: "hi"})
	_, err := d.handleSessionSpawn(context.Background(), params)
	if err == nil || !strings.Contains(err.Error(), "tmux not found") {
		t.Fatalf("expected tmux guard error, got %v", err)
	}
}

func TestHandleSessionInputPromptFallback(t *testing.T) {
	fp := &fakePrompter{}
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters["opencode"] = fp

	input := func(id, text string) error {
		params, _ := json.Marshal(api.InputParams{SessionID: id, Text: text, Submit: true})
		_, err := d.handleSessionInput(context.Background(), params)
		return err
	}

	s, _ := d.reg.ApplyHook(registry.HookUpdate{Agent: "opencode", AgentSessionID: "ses_1", Status: session.StatusIdle, Input: session.InputAPI})
	if err := input(s.ID, "hello"); err != nil || !fp.called || fp.gotText != "hello" {
		t.Fatalf("idle prompt: err=%v called=%v text=%q", err, fp.called, fp.gotText)
	}

	// The agent decides whether a busy session takes the prompt.
	fp.called = false
	s, _ = d.reg.ApplyHook(registry.HookUpdate{Agent: "opencode", AgentSessionID: "ses_1", Status: session.StatusWorking})
	if err := input(s.ID, "hi"); err != nil || !fp.called || fp.gotText != "hi" {
		t.Fatalf("working prompt: err=%v called=%v text=%q", err, fp.called, fp.gotText)
	}
}

func TestCanSpawnViewer(t *testing.T) {
	d := New()
	d.caps.SpawnSession = true
	d.binCache = map[string]bool{"opencode": true, "claude": true}
	mk := func(agent string) session.Session {
		return session.Session{Agent: agent, AgentSessionID: "s1", Cwd: "/tmp"}
	}

	// opencode's pane is a viewer of a service-backed session, so it can respawn.
	if !d.canSpawnViewer(mk("opencode")) {
		t.Fatal("opencode: pane is a viewer, should be spawnable")
	}
	// claude is resumable, but its pane is the session's own process: never respawn.
	if d.canSpawnViewer(mk("claude")) {
		t.Fatal("claude: pane is the process, must not be viewer-spawnable")
	}
	noCwd := mk("opencode")
	noCwd.Cwd = ""
	if d.canSpawnViewer(noCwd) {
		t.Fatal("unknown cwd must not be viewer-spawnable")
	}
	d.caps.SpawnSession = false
	if d.canSpawnViewer(mk("opencode")) {
		t.Fatal("no tmux must not be viewer-spawnable")
	}
}

func TestHandleSessionInputPanelessNonPrompter(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters["ghost"] = &fakeNonPrompter{}

	s, _ := d.reg.ApplyHook(registry.HookUpdate{Agent: "ghost", AgentSessionID: "g1", Status: session.StatusIdle})
	params, _ := json.Marshal(api.InputParams{SessionID: s.ID, Text: "x", Submit: true})
	if _, err := d.handleSessionInput(context.Background(), params); !errors.Is(err, api.ErrNoTerminalControl) {
		t.Fatalf("want ErrNoTerminalControl, got %v", err)
	}
}

// fakeDiscoverer registers the target session on its Nth ScanOnce, modelling the
// spawn→ps lag: the process only becomes visible to discovery after a few scans.
type fakeDiscoverer struct {
	calls    int
	after    int
	register func()
}

func (f *fakeDiscoverer) ScanOnce(context.Context) error {
	f.calls++
	if f.calls >= f.after {
		f.register()
	}
	return nil
}

func TestSpawnEnvDisablesOpencodeTabs(t *testing.T) {
	if env := spawnEnv("opencode"); len(env) != 1 || env[0] != `OPENCODE_CLI_CONFIG_CONTENT={"tabs":{"enabled":false}}` {
		t.Fatalf("opencode spawn env = %#v", env)
	}
	if env := spawnEnv("/usr/local/bin/opencode"); len(env) != 1 {
		t.Fatalf("opencode env must apply to an absolute path too: %#v", env)
	}
	if env := spawnEnv("claude"); env != nil {
		t.Fatalf("non-opencode command must get no extra env: %#v", env)
	}
}

// The post-spawn rescan must retry until the session is registered, then stop as
// soon as it appears (not run the full backoff schedule).
func TestRescanUntilRegisteredStopsWhenFound(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{
		session.TmuxServerArgus: tmux.New("argus-rescan-test"),
	})
	id := "argus:%7"
	fd := &fakeDiscoverer{after: 3, register: func() {
		d.reg.ReconcileSessions("claude", []registry.DiscoveredSession{{
			HasPane: true, Server: session.TmuxServerArgus, PaneID: "%7",
			Frontend: session.FrontendTmux,
		}})
	}}
	d.discs = []adapter.Discoverer{fd}

	d.rescanUntilRegistered(id)

	if _, ok := d.reg.Get(id); !ok {
		t.Fatalf("session %s should be registered after retries", id)
	}
	if fd.calls != 3 {
		t.Fatalf("rescan should stop as soon as the session appears: got %d scans, want 3", fd.calls)
	}
}

// A node without tmux must advertise no spawn support and reject sessions.spawn
// with an error, even if a client bypasses the UI gating.
func TestSpawnGuardAndIdentifyReportTmux(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{
		session.TmuxServerArgus: tmux.New("argus-guard-test"),
	})

	d.caps.SpawnSession = false // simulate a host without the tmux binary
	if r, _ := d.handleNodeIdentify(context.Background(), nil); r.(api.IdentifyResult).Capabilities.SpawnSession {
		t.Fatal("identify should report spawn_session=false")
	}
	if _, err := d.handleSessionSpawn(context.Background(), nil); err == nil {
		t.Fatal("spawn should be rejected when tmux is unavailable")
	}

	d.caps.SpawnSession = true
	if r, _ := d.handleNodeIdentify(context.Background(), nil); !r.(api.IdentifyResult).Capabilities.SpawnSession {
		t.Fatal("identify should report spawn_session=true")
	}
}

// A plain node answers server.info with its version and just itself: an empty ID
// (addressed implicitly, no routing namespace) carrying its spawn capability, so a
// direct client can show the version and gate the spawn UI.
func TestServerInfoReportsSelf(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{
		session.TmuxServerArgus: tmux.New("argus-list-test"),
	})
	d.label = "boxy"
	d.version = "9.9"
	d.caps.SpawnSession = false

	r, _ := d.handleServerInfo(context.Background(), nil)
	info := r.(api.ServerInfo)
	if info.Version != "9.9" {
		t.Fatalf("version = %q, want 9.9", info.Version)
	}
	if len(info.Nodes) != 1 {
		t.Fatalf("nodes = %d entries, want 1", len(info.Nodes))
	}
	n := info.Nodes[0]
	if n.ID != "" || n.Label != "boxy" || n.Version != "9.9" || n.Capabilities.SpawnSession {
		t.Fatalf("self entry = %+v", n)
	}
}

func TestHandleAgentsList(t *testing.T) {
	d := New()

	dir := t.TempDir()
	for _, bin := range []string{"claude", "codex"} { // agy intentionally absent
		p := filepath.Join(dir, bin)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)

	spawnableByID := func() map[string]bool {
		r, err := d.handleAgentsList(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, a := range r.(api.AgentsListResult).Agents {
			if a.ID == "" || a.Name == "" || a.Color == "" {
				t.Fatalf("agent missing metadata: %+v", a)
			}
			m[a.ID] = a.Spawnable
		}
		return m
	}

	// Every known agent is listed; only those with a binary on PATH are spawnable.
	d.caps.SpawnSession = true
	got := spawnableByID()
	if _, ok := got["antigravity"]; !ok {
		t.Fatalf("antigravity should be listed even without a binary: %v", got)
	}
	if !got["claude"] || !got["codex"] || got["antigravity"] {
		t.Fatalf("spawnable flags = %v, want claude+codex true, antigravity false", got)
	}

	// No tmux → everything still listed; only Spawner (headless) agents such as
	// codex stay spawnable, since they create sessions over a service API.
	d.caps.SpawnSession = false
	for id, sp := range spawnableByID() {
		if sp && id != "codex" {
			t.Fatalf("no-tmux: %s must not be spawnable", id)
		}
	}
}

func TestWithCapsStampsCanPrompt(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	working := func(agent string, in session.InputMode) session.Session {
		return session.Session{Agent: agent, Input: in, Status: session.StatusWorking}
	}
	if !d.withCaps(working("claude", session.InputAPI)).CanPrompt {
		t.Fatal("working claude over the mod: can_prompt should be set")
	}
	if d.withCaps(working("claude", session.InputPane)).CanPrompt {
		t.Fatal("working claude over keystrokes: can_prompt must be unset")
	}
	if d.withCaps(working("opencode", session.InputAPI)).CanPrompt {
		t.Fatal("working opencode: can_prompt must be unset")
	}
	if !d.withCaps(session.Session{Agent: "opencode", Input: session.InputAPI, Status: session.StatusIdle}).CanPrompt {
		t.Fatal("idle opencode: can_prompt should be set")
	}
}

func TestResolveSpawnCommand(t *testing.T) {
	d := New()
	// Default agent (empty) resolves to the first adapter, claude.
	cmd, args := d.resolveSpawnCommand(api.SpawnParams{Prompt: "fix the bug", Model: "sonnet"})
	if cmd != "claude" || !slices.Equal(args, []string{"--model", "sonnet", "fix the bug"}) {
		t.Fatalf("default: cmd=%q args=%#v, want claude [--model sonnet \"fix the bug\"]", cmd, args)
	}
	// A named agent resolves to its own binary.
	if cmd, _ := d.resolveSpawnCommand(api.SpawnParams{Agent: "codex"}); cmd != "codex" {
		t.Fatalf("codex: cmd=%q, want codex", cmd)
	}
	// An unknown agent falls back to the default adapter.
	if cmd, _ := d.resolveSpawnCommand(api.SpawnParams{Agent: "nope"}); cmd != "claude" {
		t.Fatalf("unknown: cmd=%q, want claude", cmd)
	}
	// An explicit command overrides the agent; the prompt becomes its arg.
	cmd, args = d.resolveSpawnCommand(api.SpawnParams{Agent: "codex", Command: "zsh", Prompt: "hi"})
	if cmd != "zsh" || len(args) != 1 || args[0] != "hi" {
		t.Fatalf("override: cmd=%q args=%#v, want zsh [\"hi\"]", cmd, args)
	}
}

func TestHandleSessionSpawnRejectsMissingBinary(t *testing.T) {
	d := New()
	d.caps.SpawnSession = true
	d.label = "boxy"
	t.Setenv("PATH", t.TempDir()) // no agent CLI installed

	// Name is set so the handler skips tmux ListPanes and reaches the PATH check.
	params := []byte(`{"name":"s","cwd":"/tmp","agent":"claude","prompt":"hi"}`)
	_, err := d.handleSessionSpawn(context.Background(), params)
	if err == nil {
		t.Fatal("spawn should be rejected when the agent binary is not on PATH")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Fatalf("error should name the missing binary, got %v", err)
	}
}

func TestHandleSessionResumeRejectsNoTmux(t *testing.T) {
	d := New()
	d.caps.SpawnSession = false
	d.label = "boxy"
	raw, _ := json.Marshal(api.ResumeParams{Agent: "claude", AgentSessionID: "x", Cwd: t.TempDir()})
	if _, err := d.handleSessionResume(context.Background(), raw); err == nil {
		t.Fatal("expected error when tmux unavailable")
	}
}

func TestHandleSessionResumeRejectsMissingBinary(t *testing.T) {
	d := New()
	d.caps.SpawnSession = true
	d.label = "boxy"
	t.Setenv("PATH", t.TempDir()) // no agent CLI installed
	raw, _ := json.Marshal(api.ResumeParams{Agent: "claude", AgentSessionID: "x", Cwd: t.TempDir()})
	if _, err := d.handleSessionResume(context.Background(), raw); err == nil {
		t.Fatal("expected error when binary missing")
	}
}

func TestHandleSessionResumeRejectsEmptyParams(t *testing.T) {
	d := New()
	d.caps.SpawnSession = true
	for _, p := range []api.ResumeParams{
		{Agent: "", AgentSessionID: "x", Cwd: t.TempDir()},
		{Agent: "claude", AgentSessionID: "", Cwd: t.TempDir()},
		{Agent: "claude", AgentSessionID: "x", Cwd: ""}, // unknown cwd
	} {
		raw, _ := json.Marshal(p)
		if _, err := d.handleSessionResume(context.Background(), raw); err == nil {
			t.Fatalf("expected error for empty params %+v", p)
		}
	}
}

func TestHandleSessionResumeJumpsToInflightLivePane(t *testing.T) {
	d := New()
	d.caps.SpawnSession = true
	d.discs = nil // resume rescans first; keep the seeded pane from being pruned
	// A live pane exists under id "argus:%7" but its agent session id hasn't been
	// reported yet, so the by-agent-session check misses and the guard is consulted.
	d.reg.ReconcileSessions("claude", []registry.DiscoveredSession{{
		HasPane:     true,
		Server:      session.TmuxServerArgus,
		PaneID:      "%7",
		SessionName: "proj",
		CurrentPath: t.TempDir(),
	}})
	d.resuming["claude\x00sess-1"] = "argus:%7"
	raw, _ := json.Marshal(api.ResumeParams{Agent: "claude", AgentSessionID: "sess-1", Cwd: t.TempDir()})
	res, err := d.handleSessionResume(context.Background(), raw)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if r := res.(api.ResumeResult); r.SessionID != "argus:%7" {
		t.Fatalf("got %#v, want in-flight session id argus:%%7", r)
	}
}

func TestHandleSessionResumeErrorsWhenInflightPaneGone(t *testing.T) {
	d := New()
	d.caps.SpawnSession = true
	d.discs = nil // resume rescans first; keep test state deterministic
	d.resuming["claude\x00sess-1"] = "argus:%dead"
	raw, _ := json.Marshal(api.ResumeParams{Agent: "claude", AgentSessionID: "sess-1", Cwd: t.TempDir()})
	if _, err := d.handleSessionResume(context.Background(), raw); err == nil {
		t.Fatal("expected error when the in-flight pane is gone")
	}
}

func TestClearResumingOnKill(t *testing.T) {
	d := New()
	d.resuming["claude\x00sess-1"] = "argus:%7"
	d.resuming["claude\x00sess-2"] = "argus:%8"
	d.clearResuming("argus:%7")
	if _, ok := d.resuming["claude\x00sess-1"]; ok {
		t.Fatal("guard for the killed pane should be cleared")
	}
	if _, ok := d.resuming["claude\x00sess-2"]; !ok {
		t.Fatal("guard for an unrelated pane must survive")
	}
}

func TestHandleSessionResumeJumpsToLiveSession(t *testing.T) {
	d := New()
	d.caps.SpawnSession = true
	d.discs = nil // resume rescans first; keep the seeded session from being pruned
	// Seed a live, controllable session with a matching agent session id.
	d.reg.ReconcileSessions("claude", []registry.DiscoveredSession{{
		AgentSessionID: "live-1",
		HasPane:        true,
		Server:         session.TmuxServerArgus,
		PaneID:         "%9",
		SessionName:    "proj",
		CurrentPath:    t.TempDir(),
	}})
	var live string
	for _, s := range d.reg.Snapshot() {
		if s.AgentSessionID == "live-1" {
			live = s.ID
		}
	}
	if live == "" {
		t.Fatal("seed failed: no live session")
	}
	raw, _ := json.Marshal(api.ResumeParams{Agent: "claude", AgentSessionID: "live-1", Cwd: t.TempDir()})
	res, err := d.handleSessionResume(context.Background(), raw)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	r := res.(api.ResumeResult)
	if r.SessionID != live {
		t.Fatalf("got %#v, want SessionID=%q", r, live)
	}
}

func TestHandleNodeIdentifyTip(t *testing.T) {
	t.Run("with trust store", func(t *testing.T) {
		sk, err := trustlog.GenerateSigner()
		if err != nil {
			t.Fatal(err)
		}
		tlog, err := trustlog.NewGenesis([][]byte{sk.Public}, sk, nil)
		if err != nil {
			t.Fatal(err)
		}
		genesisHash := tlog.Tip()
		chain := trustlog.MarshalChain(tlog.Entries())

		st := trustlog.NewSyncStore(genesisHash)
		if _, err := st.Ingest(chain); err != nil {
			t.Fatal(err)
		}

		d := New()
		d.trust.Store(st)

		res, err := d.handleNodeIdentify(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		got := res.(api.IdentifyResult)
		if !bytes.Equal(got.Tip, st.Tip()) {
			t.Errorf("Tip = %x, want %x", got.Tip, st.Tip())
		}
	})

	t.Run("no trust store", func(t *testing.T) {
		d := New()
		res, err := d.handleNodeIdentify(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		got := res.(api.IdentifyResult)
		if len(got.Tip) != 0 {
			t.Errorf("Tip = %x, want empty", got.Tip)
		}
	})
}

func TestHandleSessionSpawnRejectsMissingDir(t *testing.T) {
	fs := &fakeSpawner{id: "ses_x"}
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.label = "boxy"
	d.adapters["fake"] = fs
	gone := filepath.Join(t.TempDir(), "gone")

	params, _ := json.Marshal(api.SpawnParams{Agent: "fake", Cwd: gone, Prompt: "hi"})
	_, err := d.handleSessionSpawn(context.Background(), params)
	if err == nil || !strings.Contains(err.Error(), "does not exist on node boxy") {
		t.Fatalf("expected missing-directory error, got %v", err)
	}
	if fs.gotPrompt != "" {
		t.Fatal("SpawnSession must not run for a missing directory")
	}
}

func TestHandleSessionSpawnExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSpawner{id: "ses_x"}
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters["fake"] = fs

	params, _ := json.Marshal(api.SpawnParams{Agent: "fake", Cwd: "~/proj", Prompt: "hi"})
	if _, err := d.handleSessionSpawn(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "proj"); fs.gotCwd != want {
		t.Fatalf("cwd = %q, want %q", fs.gotCwd, want)
	}
}

func TestHandleSessionResumeRejectsMissingDir(t *testing.T) {
	d := New()
	d.caps.SpawnSession = true
	d.label = "boxy"
	gone := filepath.Join(t.TempDir(), "gone")
	raw, _ := json.Marshal(api.ResumeParams{Agent: "claude", AgentSessionID: "x", Cwd: gone})
	_, err := d.handleSessionResume(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "cannot resume: directory "+gone) {
		t.Fatalf("expected missing-directory error, got %v", err)
	}
}

func TestDirMissing(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cwd  string
		want bool
	}{
		{"", false},
		{dir, false},
		{file, true},
		{filepath.Join(dir, "gone"), true},
	} {
		if got := dirMissing(tc.cwd); got != tc.want {
			t.Errorf("dirMissing(%q) = %v, want %v", tc.cwd, got, tc.want)
		}
	}
}

func TestCwdError(t *testing.T) {
	d := New()
	d.label = "boxy"
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cwd  string
		want string // "" for no error
	}{
		{"", ""},
		{dir, ""},
		{"rel/dir", "is not an absolute path"},
		{filepath.Join(dir, "gone"), "does not exist on node boxy"},
		{file, "does not exist on node boxy"},
		{locked, "is not accessible on node boxy"},
	} {
		if tc.cwd == locked && os.Geteuid() == 0 {
			continue // root enters any directory
		}
		err := d.cwdError("cannot spawn", tc.cwd)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("cwdError(%q) = %v, want none", tc.cwd, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("cwdError(%q) = %v, want %q", tc.cwd, err, tc.want)
		}
	}
}

func TestHandleAgentModelList(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters["fake"] = &fakeSpawner{models: []api.ModelInfo{{ID: "m1", Name: "M1"}}}
	d.adapters["plain"] = fakePlain{}
	d.adapters["broken"] = &fakeSpawner{modelsErr: errors.New("boom")}

	models := func(agent string) (any, error) {
		return d.handleAgentModelList(context.Background(), mustSpawnJSON(t, api.AgentModelListParams{Agent: agent}))
	}

	res, err := models("fake")
	if err != nil {
		t.Fatalf("fake: %v", err)
	}
	if got := res.(api.AgentModelListResult).Models; len(got) != 1 || got[0] != (api.ModelInfo{ID: "m1", Name: "M1"}) {
		t.Fatalf("fake models = %+v", got)
	}

	res, err = models("plain")
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	got := res.(api.AgentModelListResult).Models
	if got == nil || len(got) != 0 {
		t.Fatalf("plain models = %#v, want empty non-nil", got)
	}

	_, err = models("nope")
	var rpcErr *api.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Fatalf("unknown agent err = %v, want CodeInvalidRequest", err)
	}

	_, err = models("broken")
	if !errors.As(err, &rpcErr) || !strings.Contains(rpcErr.Message, "boom") {
		t.Fatalf("list error = %v, want message containing boom", err)
	}
}

func TestHandleSessionSpawnRejectsUnusableModel(t *testing.T) {
	fs := &fakeSpawner{id: "ses_x"}
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters["fake"] = fs
	d.adapters["plain"] = fakePlain{}
	repo := t.TempDir()

	spawn := func(p api.SpawnParams) error {
		p.Cwd = repo
		_, err := d.handleSessionSpawn(context.Background(), mustSpawnJSON(t, p))
		return err
	}
	wantInvalid := func(name string, err error, msg string) {
		t.Helper()
		var rpcErr *api.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest || !strings.Contains(rpcErr.Message, msg) {
			t.Fatalf("%s: err = %v, want CodeInvalidRequest containing %q", name, err, msg)
		}
	}

	wantInvalid("no model choice", spawn(api.SpawnParams{Agent: "plain", Model: "m1"}), "no model choice")
	wantInvalid("explicit command", spawn(api.SpawnParams{Agent: "fake", Command: "zsh", Model: "m1"}), "explicit command")
	if fs.gotCwd != "" {
		t.Fatalf("SpawnSession called for a rejected model")
	}

	if err := spawn(api.SpawnParams{Agent: "fake", Model: "m1", Prompt: "hi"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if fs.gotModel != "m1" {
		t.Fatalf("gotModel = %q, want m1", fs.gotModel)
	}
}

type fakeFiller struct {
	fakePrompter
	filled    string
	fillErr   error
	notFilled bool // took the prompt another way: no Enter
	onFill    func()
}

func (f *fakeFiller) FillPrompt(_ context.Context, _ session.Session, text string) (bool, error) {
	f.filled = text
	if f.onFill != nil {
		f.onFill()
	}
	return f.fillErr == nil && !f.notFilled, f.fillErr
}

func TestBusyPaneSessionIsFilledThenSubmitted(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	tc := tmux.New("argus-fill-test")
	t.Cleanup(func() { _ = tc.KillServer(context.Background()) })
	paneID, err := tc.NewSession(ctx, tmux.NewSessionOpts{Name: "sh", Command: "/bin/sh -c 'read x; echo GOT-ENTER; sleep 30'"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	d := newNode(map[session.TmuxServer]*tmux.Client{session.TmuxServerArgus: tc})
	ff := &fakeFiller{}
	d.adapters["opencode"] = ff
	s, _ := d.reg.ApplyHook(registry.HookUpdate{Agent: "opencode", Server: session.TmuxServerArgus, PaneID: paneID,
		AgentSessionID: "ses_1", Status: session.StatusWorking, Input: session.InputAPI})
	input := func(text string) error {
		params, _ := json.Marshal(api.InputParams{SessionID: s.ID, Text: text, Submit: true})
		_, err := d.handleSessionInput(ctx, params)
		return err
	}
	entered := func() bool {
		out, _ := tc.CapturePane(ctx, paneID, tmux.CaptureOpts{})
		return strings.Contains(out, "GOT-ENTER")
	}

	ff.fillErr = api.ErrSessionBusy
	if err := input("steer"); !errors.Is(err, api.ErrSessionBusy) {
		t.Fatalf("refused fill: err = %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if entered() || ff.called {
		t.Fatal("a refused fill pressed Enter or fell back to SendPrompt")
	}

	ff.fillErr, ff.notFilled = nil, true
	if err := input("/compact"); err != nil {
		t.Fatalf("unfilled prompt: err = %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if entered() {
		t.Fatal("an unfilled prompt pressed Enter")
	}

	ff.notFilled = false
	ff.onFill = func() {
		d.reg.ApplyHook(registry.HookUpdate{Agent: "opencode", AgentSessionID: "ses_1", Status: session.StatusAwaitingInput,
			Interaction: &session.Interaction{Kind: session.InteractionPermission}})
	}
	if err := input("steer"); !errors.Is(err, api.ErrSessionBusy) {
		t.Fatalf("fill under a new permission: err = %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if entered() {
		t.Fatal("Enter was sent while a permission was pending")
	}
	ff.onFill = nil
	d.reg.ApplyHook(registry.HookUpdate{Agent: "opencode", AgentSessionID: "ses_1", Status: session.StatusWorking,
		ReplaceInteraction: true})

	if err := input("steer"); err != nil {
		t.Fatal(err)
	}
	if ff.filled != "steer" || ff.called {
		t.Fatalf("filled %q, SendPrompt called %v; want a fill only", ff.filled, ff.called)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !entered() {
		if time.Now().After(deadline) {
			t.Fatal("Enter never reached the pane")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSendPromptIsUsedWhenNoFillApplies(t *testing.T) {
	for _, tc := range []struct {
		name string
		hook registry.HookUpdate
		text string
	}{
		{"working but paneless", registry.HookUpdate{AgentSessionID: "ses_2", Status: session.StatusWorking}, "hi"},
		{"idle with a pane", registry.HookUpdate{AgentSessionID: "ses_3", Status: session.StatusIdle,
			Server: session.TmuxServerArgus, PaneID: "%1"}, "hi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newNode(map[session.TmuxServer]*tmux.Client{})
			ff := &fakeFiller{}
			d.adapters["opencode"] = ff
			tc.hook.Agent, tc.hook.Input = "opencode", session.InputAPI
			s, _ := d.reg.ApplyHook(tc.hook)
			params, _ := json.Marshal(api.InputParams{SessionID: s.ID, Text: tc.text, Submit: true})
			if _, err := d.handleSessionInput(context.Background(), params); err != nil {
				t.Fatal(err)
			}
			if !ff.called || ff.gotText != tc.text || ff.filled != "" {
				t.Fatalf("called=%v text=%q filled=%q; want SendPrompt(%q) and no fill", ff.called, ff.gotText, ff.filled, tc.text)
			}
		})
	}
}

type fakeShell struct {
	fakeFiller
	shell string
}

func (f *fakeShell) IsShellInput(text string) bool { return strings.HasPrefix(text, "!") }

func (f *fakeShell) SendShellInput(_ context.Context, _ session.Session, text string) error {
	f.shell = text
	return nil
}

func TestShellInputRouting(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hook      registry.HookUpdate
		text      string
		submit    bool
		wantErr   error
		wantShell string
		wantSent  string
	}{
		{"working with a pane", registry.HookUpdate{AgentSessionID: "ses_1", Status: session.StatusWorking,
			Server: session.TmuxServerArgus, PaneID: "%1"}, "! ls", true, nil, "! ls", ""},
		{"idle with a pane", registry.HookUpdate{AgentSessionID: "ses_2", Status: session.StatusIdle,
			Server: session.TmuxServerArgus, PaneID: "%2"}, "! ls", true, nil, "! ls", ""},
		{"paneless", registry.HookUpdate{AgentSessionID: "ses_3", Status: session.StatusIdle}, "! ls", true,
			api.ErrShellNeedsPane, "", ""},
		{"not submitted", registry.HookUpdate{AgentSessionID: "ses_4", Status: session.StatusIdle}, "!", false,
			nil, "", "!"},
		{"a plain prompt", registry.HookUpdate{AgentSessionID: "ses_5", Status: session.StatusIdle}, "hi", true,
			nil, "", "hi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newNode(map[session.TmuxServer]*tmux.Client{})
			fs := &fakeShell{}
			d.adapters["opencode"] = fs
			tc.hook.Agent, tc.hook.Input = "opencode", session.InputAPI
			s, _ := d.reg.ApplyHook(tc.hook)
			params, _ := json.Marshal(api.InputParams{SessionID: s.ID, Text: tc.text, Submit: tc.submit})
			_, err := d.handleSessionInput(context.Background(), params)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if fs.shell != tc.wantShell || fs.gotText != tc.wantSent || fs.filled != "" {
				t.Fatalf("shell %q, SendPrompt %q, filled %q; want shell %q, SendPrompt %q",
					fs.shell, fs.gotText, fs.filled, tc.wantShell, tc.wantSent)
			}
		})
	}
}

type fakeCompleter struct {
	adapter.Adapter
	items      []api.PromptCompletion
	gotTrigger string
}

func (f *fakeCompleter) CompletePrompt(_ context.Context, _ session.Session, trigger string) ([]api.PromptCompletion, error) {
	f.gotTrigger = trigger
	return f.items, nil
}

func promptComplete(t *testing.T, d *Node, id, trigger string) (api.PromptCompleteResult, error) {
	t.Helper()
	params, _ := json.Marshal(api.PromptCompleteParams{SessionID: id, Trigger: trigger})
	res, err := d.handleSessionPromptComplete(context.Background(), params)
	if err != nil {
		return api.PromptCompleteResult{}, err
	}
	return res.(api.PromptCompleteResult), nil
}

func TestHandleSessionPromptCompleteReturnsAdapterItems(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	fc := &fakeCompleter{items: []api.PromptCompletion{{Name: "compact"}}}
	d.adapters["fake"] = fc
	s, _ := d.reg.ApplyHook(registry.HookUpdate{Agent: "fake", AgentSessionID: "f1", Status: session.StatusIdle})

	res, err := promptComplete(t, d, s.ID, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0] != fc.items[0] || fc.gotTrigger != "/" {
		t.Fatalf("items = %+v, trigger = %q", res.Items, fc.gotTrigger)
	}
}

func TestHandleSessionPromptCompleteWithoutCompleterIsEmpty(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters["plain"] = fakePlain{}
	s, _ := d.reg.ApplyHook(registry.HookUpdate{Agent: "plain", AgentSessionID: "p1", Status: session.StatusIdle})

	res, err := promptComplete(t, d, s.ID, "/")
	if err != nil {
		t.Fatal(err)
	}
	if res.Items == nil || len(res.Items) != 0 {
		t.Fatalf("items = %#v, want empty non-nil", res.Items)
	}
}

func TestHandleSessionPromptCompleteUnknownSession(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	_, err := promptComplete(t, d, "nope", "/")
	var rpcErr *api.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest || rpcErr.Message != "unknown session nope" {
		t.Fatalf("err = %v, want invalid request \"unknown session nope\"", err)
	}
}
