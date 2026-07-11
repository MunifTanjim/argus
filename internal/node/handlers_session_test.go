package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

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
