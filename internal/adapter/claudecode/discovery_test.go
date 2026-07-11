package claudecode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// TestScanOnceAttachesPaneFromProcSession spins a real tmux pane, then stubs ps so
// a claude pid sits on that pane's tty and writes a matching proc-session file.
// ScanOnce must correlate the two into a pane-bearing session keyed by pane.
func TestScanOnceAttachesPaneFromProcSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	client := tmux.New("argus-disc-test")
	t.Cleanup(func() { _ = client.KillServer(ctx) })
	if _, err := client.NewSession(ctx, tmux.NewSessionOpts{Name: "s1"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	panes, err := client.ListPanes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatalf("ListPanes: err=%v len=%d", err, len(panes))
	}
	pane := panes[0]
	tty := normalizeTTY(pane.TTY)

	dir := t.TempDir()
	claudeSessionsDirOverride = dir
	t.Cleanup(func() { claudeSessionsDirOverride = "" })
	if err := os.WriteFile(filepath.Join(dir, "4242.json"),
		[]byte(`{"pid":4242,"sessionId":"c1","cwd":"/repo","name":"n","entrypoint":"cli"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := runPS
	t.Cleanup(func() { runPS = orig })
	runPS = func() (string, error) { return "  4242 " + tty + " S+ claude", nil }

	reg := registry.New()
	d := NewDiscoverer(reg, map[session.TmuxServer]*tmux.Client{session.TmuxServerArgus: client})
	if err := d.ScanOnce(ctx); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	s, ok := reg.Get("argus:" + pane.PaneID)
	if !ok {
		t.Fatalf("expected pane-keyed session argus:%s", pane.PaneID)
	}
	if s.Frontend != session.FrontendTmux || s.AgentSessionID != "c1" || s.Tmux.PaneID != pane.PaneID {
		t.Fatalf("unexpected session: %+v", s)
	}
}

func TestScanOnceDiscoversAndPrunesVSCode(t *testing.T) {
	dir := t.TempDir()
	claudeSessionsDirOverride = dir
	t.Cleanup(func() { claudeSessionsDirOverride = "" })
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("777.json", `{"pid":777,"sessionId":"vs-1","cwd":"/repo","name":"n1","entrypoint":"claude-vscode"}`)
	write("888.json", `{"pid":888,"sessionId":"cli-1","cwd":"/repo","entrypoint":"cli"}`)

	orig := runPS
	t.Cleanup(func() { runPS = orig })

	// VSCode claude 777 alive (ttyless), a shell on 888.
	runPS = func() (string, error) {
		return "  777 ?? S /opt/homebrew/bin/claude\n  888 ttys001 S+ -zsh", nil
	}

	reg := registry.New()
	d := NewDiscoverer(reg, nil) // no tmux servers; only the vscode pass runs
	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	s, ok := reg.Get("claude:vs-1")
	if !ok {
		t.Fatal("live vscode session vs-1 should be discovered")
	}
	if s.Frontend != session.FrontendVSCode || s.Tmux.PaneID != "" || s.Name != "n1" {
		t.Fatalf("unexpected vscode session: %+v", s)
	}
	if _, ok := reg.Get("claude:cli-1"); ok {
		t.Error("cli entrypoint must not be discovered by the vscode pass")
	}

	// 777 gone → next scan prunes it.
	runPS = func() (string, error) { return "  888 ttys001 S+ -zsh", nil }
	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if _, ok := reg.Get("claude:vs-1"); ok {
		t.Error("vs-1 should be pruned once its process is gone")
	}
}

func TestScanOnceVSCodeNoPruneOnPSError(t *testing.T) {
	dir := t.TempDir()
	claudeSessionsDirOverride = dir
	t.Cleanup(func() { claudeSessionsDirOverride = "" })

	reg := registry.New()
	reg.ReconcileSessions(Agent, []registry.DiscoveredSession{{AgentSessionID: "vs-1", Frontend: session.FrontendVSCode}})

	orig := runPS
	t.Cleanup(func() { runPS = orig })
	runPS = func() (string, error) { return "", errors.New("ps blew up") }

	d := NewDiscoverer(reg, nil)
	_ = d.ScanOnce(context.Background()) // ps error is returned; reconcile is skipped
	if _, ok := reg.Get("claude:vs-1"); !ok {
		t.Error("a failed ps probe must not prune vscode sessions")
	}
}

func TestParseClaudeProcs(t *testing.T) {
	psOut := strings.Join([]string{
		"  111 ttys002 Ss   -zsh",
		"  222 ttys002 S+   claude",
		"  444 ttys005 S+   node /x/cli.js",
		"  777 ??      S    /opt/homebrew/bin/claude", // vscode: ttyless
		"  888 ??      S    some-daemon",
	}, "\n")

	got := parseClaudeProcs(psOut)
	if got[222] != "ttys002" {
		t.Errorf("222 tty: got %q want ttys002", got[222])
	}
	if tty, ok := got[777]; !ok || tty != "" {
		t.Errorf("777 ttyless: ok=%v tty=%q want ok+empty", ok, tty)
	}
	if _, ok := got[444]; ok {
		t.Error("node 444 should be absent")
	}
	if _, ok := got[888]; ok {
		t.Error("daemon 888 should be absent")
	}
	if len(got) != 2 {
		t.Errorf("want 2 claude procs, got %d", len(got))
	}
}

func TestCachedTranscriptStatusHint(t *testing.T) {
	d := NewDiscoverer(registry.New(), nil)

	if _, hint := d.cachedTranscript("parser/testdata/ongoing_tooluse.jsonl"); hint != session.StatusWorking {
		t.Errorf("ongoing_tooluse: hint=%q want working", hint)
	}
	if _, hint := d.cachedTranscript("parser/testdata/not_ongoing_text.jsonl"); hint != session.StatusIdle {
		t.Errorf("not_ongoing_text: hint=%q want idle", hint)
	}
	if sum, hint := d.cachedTranscript(""); sum != nil || hint != "" {
		t.Errorf("empty path: sum=%v hint=%q want nil, \"\"", sum, hint)
	}
}

// TestBuildDiscoveredPaneOnly: a file-less live claude on a pane is emitted;
// the other procs (see inline) are not.
func TestBuildDiscoveredPaneOnly(t *testing.T) {
	procs := map[int]string{
		100: "ttys002", // has a proc-session file below → entries pass, not pane-only
		500: "ttys003", // live on a pane, no file → pane-only
		600: "ttys099", // tty but no pane → skipped
		700: "",        // ttyless → skipped
		800: "ttys002", // shares pid 100's tty/pane, no file → must not double-emit that pane
	}
	paneByTTY := map[string]paneInfo{
		"ttys002": {server: session.TmuxServerArgus, paneID: "%0", sessionName: "s0", currentPath: "/repo/a"},
		"ttys003": {server: session.TmuxServerArgus, paneID: "%1", sessionName: "s1", currentPath: "/repo/b"},
	}
	entries := []procSession{
		{PID: 100, SessionID: "tmux-1", Entrypoint: "cli", Cwd: "/x"},
	}

	var paneOnly []registry.DiscoveredSession
	full := map[string]registry.DiscoveredSession{}
	for _, d := range buildDiscovered(procs, paneByTTY, entries) {
		if d.AgentSessionID == "" {
			paneOnly = append(paneOnly, d)
		} else {
			full[d.AgentSessionID] = d
		}
	}

	// The pid with a file is emitted once (not double-counted by the pane-only pass).
	if len(full) != 1 || full["tmux-1"].PaneID != "%0" {
		t.Fatalf("entries pass: %+v", full)
	}
	// Only 500 is surfaced pane-only.
	if len(paneOnly) != 1 {
		t.Fatalf("want 1 pane-only session, got %d: %+v", len(paneOnly), paneOnly)
	}
	po := paneOnly[0]
	if !po.HasPane || po.PaneID != "%1" || po.Frontend != session.FrontendTmux ||
		po.AgentSessionID != "" || po.Cwd != "/repo/b" || po.Repo != repoName("/repo/b") {
		t.Fatalf("pane-only session: %+v", po)
	}
}

// TestScanOnceSurfacesStuckPaneThenUpgrades drives a real tmux pane. First scan sees
// a live claude on that pane with no proc-session file (stuck at the trust/model
// gate): it must surface as an attachable pane-keyed session. Once the proc-session
// file appears, the next scan upgrades the same record in place with the session id.
func TestScanOnceSurfacesStuckPaneThenUpgrades(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	client := tmux.New("argus-stuck-test")
	t.Cleanup(func() { _ = client.KillServer(ctx) })
	if _, err := client.NewSession(ctx, tmux.NewSessionOpts{Name: "s1"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	panes, err := client.ListPanes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatalf("ListPanes: err=%v len=%d", err, len(panes))
	}
	pane := panes[0]
	tty := normalizeTTY(pane.TTY)

	dir := t.TempDir()
	claudeSessionsDirOverride = dir
	t.Cleanup(func() { claudeSessionsDirOverride = "" })

	orig := runPS
	t.Cleanup(func() { runPS = orig })
	runPS = func() (string, error) { return "  4242 " + tty + " S+ claude", nil }

	reg := registry.New()
	d := NewDiscoverer(reg, map[session.TmuxServer]*tmux.Client{session.TmuxServerArgus: client})

	// Stuck at the gate: live claude on the pane, no proc-session file yet.
	if err := d.ScanOnce(ctx); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	id := "argus:" + pane.PaneID
	s, ok := reg.Get(id)
	if !ok {
		t.Fatalf("stuck session %s should be surfaced", id)
	}
	if !s.Controllable() || s.Frontend != session.FrontendTmux || s.AgentSessionID != "" {
		t.Fatalf("stuck session should be an attachable pane-only record: %+v", s)
	}

	// Gate cleared: claude writes its proc-session file → next scan upgrades in place.
	if err := os.WriteFile(filepath.Join(dir, "4242.json"),
		[]byte(`{"pid":4242,"sessionId":"c1","cwd":"/repo","name":"n","entrypoint":"cli"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.ScanOnce(ctx); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	s2, ok := reg.Get(id)
	if !ok {
		t.Fatalf("session %s should survive the upgrade scan (not pruned)", id)
	}
	if s2.AgentSessionID != "c1" || s2.Tmux.PaneID != pane.PaneID {
		t.Fatalf("session should upgrade in place with the session id: %+v", s2)
	}
}

func TestBuildDiscovered(t *testing.T) {
	procs := map[int]string{100: "ttys002", 200: "", 300: "ttys009"}
	paneByTTY := map[string]paneInfo{
		"ttys002": {server: session.TmuxServerDefault, paneID: "%0", sessionName: "s", currentPath: "/repo"},
	}
	entries := []procSession{
		{PID: 100, SessionID: "tmux-1", Entrypoint: "cli", Cwd: "/x"},
		{PID: 200, SessionID: "vs-1", Entrypoint: "claude-vscode", Cwd: "/x"},
		{PID: 300, SessionID: "ext-1", Entrypoint: "cli", Cwd: "/x"}, // tty but no pane
		{PID: 999, SessionID: "dead-1", Entrypoint: "cli"},           // pid not alive
	}

	by := map[string]registry.DiscoveredSession{}
	for _, d := range buildDiscovered(procs, paneByTTY, entries) {
		by[d.AgentSessionID] = d
	}
	if len(by) != 3 {
		t.Fatalf("want 3, got %d", len(by))
	}
	if !by["tmux-1"].HasPane || by["tmux-1"].PaneID != "%0" || by["tmux-1"].Frontend != session.FrontendTmux {
		t.Errorf("tmux-1: %+v", by["tmux-1"])
	}
	if by["vs-1"].HasPane || by["vs-1"].Frontend != session.FrontendVSCode {
		t.Errorf("vs-1: %+v", by["vs-1"])
	}
	if by["ext-1"].HasPane || by["ext-1"].Frontend != session.FrontendExternal {
		t.Errorf("ext-1 (tty, no pane) should be external paneless: %+v", by["ext-1"])
	}
	if _, ok := by["dead-1"]; ok {
		t.Error("dead-1 (pid absent from procs) should be skipped")
	}
}
