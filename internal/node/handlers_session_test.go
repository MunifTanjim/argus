package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	id                string
	err               error
}

func (fakeSpawner) IsHeadless() bool                       { return true }
func (fakeSpawner) Agent() string                          { return "fake" }
func (fakeSpawner) AgentName() string                      { return "Fake" }
func (fakeSpawner) AgentColor() string                     { return "#000000" }
func (fakeSpawner) SpawnCommand(string) (string, []string) { return "sh", nil } // sh exists in PATH
func (f *fakeSpawner) SpawnSession(_ context.Context, cwd, prompt string) (string, error) {
	f.gotCwd, f.gotPrompt = cwd, prompt
	return f.id, f.err
}

func TestHandleSessionSpawnHeadlessUsesServiceNoTmux(t *testing.T) {
	fs := &fakeSpawner{id: "ses_x"}
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.caps.SpawnSession = false // no tmux; the service path must not require it
	d.adapters["fake"] = fs

	params, _ := json.Marshal(api.SpawnParams{Agent: "fake", Cwd: "/repo", Prompt: "hi"})
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
	if fs.gotCwd != "/repo" || fs.gotPrompt != "hi" {
		t.Fatalf("SpawnSession args: cwd=%q prompt=%q", fs.gotCwd, fs.gotPrompt)
	}
}

func TestHandleSessionSpawnHeadlessSurfacesError(t *testing.T) {
	fs := &fakeSpawner{err: errors.New("service unavailable")}
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters["fake"] = fs

	params, _ := json.Marshal(api.SpawnParams{Agent: "fake", Cwd: "/repo", Prompt: "hi"})
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

	params, _ := json.Marshal(api.SpawnParams{Agent: "claude", Cwd: "/repo", Prompt: "hi"})
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

	fp.called = false
	s, _ = d.reg.ApplyHook(registry.HookUpdate{Agent: "opencode", AgentSessionID: "ses_1", Status: session.StatusWorking})
	if err := input(s.ID, "hi"); err == nil || fp.called {
		t.Fatalf("working prompt should be rejected: err=%v called=%v", err, fp.called)
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

	// No tmux → everything still listed, nothing spawnable.
	d.caps.SpawnSession = false
	for id, sp := range spawnableByID() {
		if sp {
			t.Fatalf("no-tmux: %s must not be spawnable", id)
		}
	}
}

func TestResolveSpawnCommand(t *testing.T) {
	d := New()
	// Default agent (empty) resolves to the first adapter, claude, with the prompt
	// as its argument.
	cmd, args := d.resolveSpawnCommand(api.SpawnParams{Prompt: "fix the bug"})
	if cmd != "claude" || len(args) != 1 || args[0] != "fix the bug" {
		t.Fatalf("default: cmd=%q args=%#v, want claude [\"fix the bug\"]", cmd, args)
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
