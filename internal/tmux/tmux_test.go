package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestParsePaneSeparators checks parsePane handles both the raw 0x1F separator
// (tmux <3.4) and the "\037" escape (tmux >=3.4).
func TestParsePaneSeparators(t *testing.T) {
	fields := []string{
		"%0", "work", "1", "0", "9416", "bash",
		"/home/runner/work/argus/argus", "/dev/pts/1", "1", "0", "0",
	}
	want := Pane{
		PaneID: "%0", SessionName: "work", WindowIndex: 1, PaneIndex: 0,
		PanePID: 9416, CurrentCommand: "bash",
		CurrentPath: "/home/runner/work/argus/argus", TTY: "/dev/pts/1",
		Active: true, Dead: false, InMode: false,
	}
	cases := map[string]string{
		"raw 0x1F":      strings.Join(fields, "\x1f"),
		"escaped \\037": strings.Join(fields, `\037`),
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := parsePane(line)
			if err != nil {
				t.Fatalf("parsePane: %v", err)
			}
			if got != want {
				t.Errorf("parsePane(%q)\n got %+v\nwant %+v", line, got, want)
			}
		})
	}
}

// TestPaneFocused checks the active-pane / active-window / attached-client logic
// that decides whether a pane is currently on a user's screen.
func TestPaneFocused(t *testing.T) {
	sep := "\x1f"
	line := func(id, paneActive, windowActive, attached string) string {
		return strings.Join([]string{id, paneActive, windowActive, attached}, sep)
	}
	out := strings.Join([]string{
		line("%1", "1", "1", "1"), // focused: active pane, active window, attached
		line("%2", "1", "1", "0"), // not focused: no client attached
		line("%3", "0", "1", "1"), // not focused: not the active pane
		line("%4", "1", "0", "1"), // not focused: not the active window
		line("%5", "1", "1", "2"), // focused: two clients attached
	}, "\n") + "\n"

	want := map[string]bool{"%1": true, "%2": false, "%3": false, "%4": false, "%5": true, "%absent": false}
	for id, exp := range want {
		got, err := paneFocused(out, id)
		if err != nil {
			t.Fatalf("paneFocused(%q): %v", id, err)
		}
		if got != exp {
			t.Errorf("paneFocused(%q) = %v, want %v", id, got, exp)
		}
	}
}

// TestPaneFocusedEscapedSep verifies the tmux >=3.4 "\037"-escaped separator is
// normalized, matching parsePane's handling.
func TestPaneFocusedEscapedSep(t *testing.T) {
	got, err := paneFocused(strings.Join([]string{"%9", "1", "1", "1"}, `\037`), "%9")
	if err != nil {
		t.Fatalf("paneFocused: %v", err)
	}
	if !got {
		t.Fatal("escaped-separator line not parsed as focused")
	}
}

// TestPanesShareWindow checks the pane→window-id pairing that decides whether two
// panes sit in the same window (grouped sessions share window ids).
func TestPanesShareWindow(t *testing.T) {
	sep := "\x1f"
	line := func(id, window string) string {
		return strings.Join([]string{id, window}, sep)
	}
	out := strings.Join([]string{
		line("%1", "@1"), // agent pane
		line("%2", "@1"), // split sibling on the agent's window
		line("%3", "@2"), // a different window
		line("%4", "@1"), // grouped-session pane sharing the agent window
	}, "\n") + "\n"

	cases := []struct {
		a, b string
		want bool
	}{
		{"%2", "%1", true},       // caller split into the agent's window
		{"%4", "%1", true},       // caller attached to a grouped session on that window
		{"%3", "%1", false},      // caller in a different window
		{"%absent", "%1", false}, // caller pane not on this server
		{"%1", "%absent", false}, // agent pane missing
	}
	for _, tc := range cases {
		got, err := panesShareWindow(out, tc.a, tc.b)
		if err != nil {
			t.Fatalf("panesShareWindow(%q,%q): %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Errorf("panesShareWindow(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestPanesShareWindowEscapedSep verifies the tmux >=3.4 "\037"-escaped separator is
// normalized, matching parsePane's handling.
func TestPanesShareWindowEscapedSep(t *testing.T) {
	out := strings.Join([]string{
		strings.Join([]string{"%1", "@1"}, `\037`),
		strings.Join([]string{"%2", "@1"}, `\037`),
	}, "\n")
	got, err := panesShareWindow(out, "%1", "%2")
	if err != nil {
		t.Fatalf("panesShareWindow: %v", err)
	}
	if !got {
		t.Fatal("escaped-separator lines not parsed as sharing a window")
	}
}

// TestPanesShareWindowSingleServer checks two panes in one session's window share a
// window, while a pane in another window does not — verifiable headlessly.
func TestPanesShareWindowSingleServer(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	p1, err := c.NewSession(ctx, NewSessionOpts{Name: "s1"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	p2, err := c.NewSession(ctx, NewSessionOpts{Name: "s2"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if same, err := c.PanesShareWindow(ctx, p1, p1); err != nil || !same {
		t.Errorf("PanesShareWindow(%q,%q) = %v,%v; want true,nil", p1, p1, same, err)
	}
	if same, err := c.PanesShareWindow(ctx, p1, p2); err != nil || same {
		t.Errorf("PanesShareWindow(%q,%q) = %v,%v; want false,nil", p1, p2, same, err)
	}
	if same, err := c.PanesShareWindow(ctx, p1, "%absent"); err != nil || same {
		t.Errorf("PanesShareWindow(%q,absent) = %v,%v; want false,nil", p1, same, err)
	}
}

// TestIsFocusedDetached checks a detached pane is never reported focused — the
// no-false-positive case verifiable headlessly (attaching needs a real terminal).
func TestIsFocusedDetached(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	pane, err := c.NewSession(ctx, NewSessionOpts{Name: "s1"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	focused, err := c.IsFocused(ctx, pane)
	if err != nil {
		t.Fatalf("IsFocused: %v", err)
	}
	if focused {
		t.Errorf("IsFocused(%q) = true; want false (no client attached)", pane)
	}
}

// TestIsFocusedNoServer returns false, not an error, when nothing runs.
func TestIsFocusedNoServer(t *testing.T) {
	c := testClient(t)
	focused, err := c.IsFocused(context.Background(), "%0")
	if err != nil {
		t.Fatalf("IsFocused (no server): %v", err)
	}
	if focused {
		t.Fatal("IsFocused on empty server = true, want false")
	}
}

// testClient returns a Client on a throwaway tmux socket, killed at test end.
// Skips if tmux is not installed.
func testClient(t *testing.T) *Client {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := "argus-test-" + t.Name()
	c := New(socket)
	t.Cleanup(func() {
		// Best effort; ignore error if the server is already gone.
		_ = c.KillServer(context.Background())
	})
	return c
}

func TestListPanesNoServer(t *testing.T) {
	c := testClient(t)
	panes, err := c.ListPanes(context.Background())
	if err != nil {
		t.Fatalf("ListPanes on empty server: %v", err)
	}
	if len(panes) != 0 {
		t.Fatalf("want 0 panes, got %d", len(panes))
	}
}

func TestNoServerClassification(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"no server running on /tmp/tmux-501/argus", true},
		{"error connecting to /tmp/tmux-501/argus", true},
		{"no current session", true},
		{"server exited unexpectedly", true},
		{"can't find session: work", false},
		{"", false},
	}
	for _, tc := range cases {
		err := &Error{Args: []string{"list-panes"}, Stderr: tc.stderr}
		if got := noServer(err); got != tc.want {
			t.Errorf("noServer(%q) = %v, want %v", tc.stderr, got, tc.want)
		}
	}
	if noServer(errors.New("plain error")) {
		t.Error("noServer(non-tmux error) = true, want false")
	}
}

// fakeTmux writes a script that fails with the given stderr for its first
// failCount invocations, then prints "ok" and exits 0. It records every call in a
// counter file so the test can assert how many attempts happened.
func fakeTmux(t *testing.T, stderr string, failCount int) (bin, counter string) {
	t.Helper()
	dir := t.TempDir()
	counter = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "faketmux")
	script := "#!/bin/sh\n" +
		"echo x >> " + counter + "\n" +
		"n=$(wc -l < " + counter + ")\n" +
		"if [ \"$n\" -le " + strconv.Itoa(failCount) + " ]; then echo '" + stderr + "' >&2; exit 1; fi\n" +
		"echo ok\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, counter
}

func callCount(t *testing.T, counter string) int {
	t.Helper()
	b, err := os.ReadFile(counter)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n")
}

// TestRunRetriesServerExited: a private socket retries the transient and succeeds.
func TestRunRetriesServerExited(t *testing.T) {
	bin, counter := fakeTmux(t, "server exited unexpectedly", 2)
	c := &Client{bin: bin, socket: "priv"}
	out, err := c.run(context.Background(), "list-panes")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Fatalf("out = %q, want ok", out)
	}
	if got := callCount(t, counter); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

// TestRunRetryBounded: a persistently-failing server exhausts the bounded retries.
func TestRunRetryBounded(t *testing.T) {
	bin, counter := fakeTmux(t, "server exited unexpectedly", 99)
	c := &Client{bin: bin, socket: "priv"}
	if _, err := c.run(context.Background(), "list-panes"); !serverExited(err) {
		t.Fatalf("err = %v, want serverExited", err)
	}
	if got := callCount(t, counter); got != 3 {
		t.Fatalf("attempts = %d, want 3 (bounded)", got)
	}
}

// TestRunDefaultServerNoRetry: the user's default server is never retried, so it
// can't spawn a server argus must not create.
func TestRunDefaultServerNoRetry(t *testing.T) {
	bin, counter := fakeTmux(t, "server exited unexpectedly", 99)
	c := &Client{bin: bin, socket: ""}
	if _, err := c.run(context.Background(), "list-panes"); !serverExited(err) {
		t.Fatalf("err = %v, want serverExited", err)
	}
	if got := callCount(t, counter); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on default server)", got)
	}
}

// TestRunNoRetryOnOtherErrors: a non-transient error is returned immediately.
func TestRunNoRetryOnOtherErrors(t *testing.T) {
	bin, counter := fakeTmux(t, "can't find session: work", 99)
	c := &Client{bin: bin, socket: "priv"}
	if _, err := c.run(context.Background(), "list-panes"); err == nil {
		t.Fatal("want error")
	}
	if got := callCount(t, counter); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry)", got)
	}
}

func TestNewSessionAndListPanes(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	paneID, err := c.NewSession(ctx, NewSessionOpts{Name: "work", Width: 120, Height: 40})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if paneID == "" || paneID[0] != '%' {
		t.Fatalf("want pane id like %%N, got %q", paneID)
	}

	panes, err := c.ListPanes(ctx)
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("want 1 pane, got %d: %+v", len(panes), panes)
	}
	p := panes[0]
	if p.PaneID != paneID {
		t.Errorf("pane id: want %q, got %q", paneID, p.PaneID)
	}
	if p.SessionName != "work" {
		t.Errorf("session name: want %q, got %q", "work", p.SessionName)
	}
	if p.PanePID <= 0 {
		t.Errorf("pane pid: want >0, got %d", p.PanePID)
	}
	if p.CurrentCommand == "" {
		t.Errorf("current command: want non-empty")
	}
	if p.CurrentPath == "" {
		t.Errorf("current path: want non-empty")
	}
}

func TestSendKeysAndCapturePane(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	paneID, err := c.NewSession(ctx, NewSessionOpts{Name: "io", Width: 80, Height: 24})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Send a marker via a shell echo and submit it.
	if err := c.SendText(ctx, paneID, "echo ARGUS_MARKER_123"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if err := c.SendKeys(ctx, paneID, "Enter"); err != nil {
		t.Fatalf("SendKeys Enter: %v", err)
	}

	// Poll capture-pane until the marker output appears.
	if !waitFor(func() bool {
		out, err := c.CapturePane(ctx, paneID, CaptureOpts{})
		return err == nil && contains(out, "ARGUS_MARKER_123")
	}) {
		t.Fatalf("marker never appeared in captured pane output")
	}
}

func TestBracketedPaste(t *testing.T) {
	const start, end = "\x1b[200~", "\x1b[201~"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", start + "hello" + end},
		{"lf to cr", "a\nb\nc", start + "a\rb\rc" + end},
		{"crlf normalized", "a\r\nb", start + "a\rb" + end},
		{"trailing newline", "a\n", start + "a\r" + end},
		{"empty", "", start + end},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bracketedPaste(tc.in); got != tc.want {
				t.Errorf("bracketedPaste(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewSessionCwdAndKillPane(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	dir := t.TempDir()
	paneID, err := c.NewSession(ctx, NewSessionOpts{Name: "cw", Cwd: dir})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	panes, err := c.ListPanes(ctx)
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	// macOS prefixes TMPDIR paths with /private; allow suffix match.
	if len(panes) != 1 || !endsWith(panes[0].CurrentPath, baseOf(dir)) {
		t.Fatalf("cwd not honored: %+v (want under %s)", panes, dir)
	}

	if err := c.KillPane(ctx, paneID); err != nil {
		t.Fatalf("KillPane: %v", err)
	}
	panes, _ = c.ListPanes(ctx)
	if len(panes) != 0 {
		t.Fatalf("want 0 panes after KillPane, got %d", len(panes))
	}
}

func TestKillSession(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	if _, err := c.NewSession(ctx, NewSessionOpts{Name: "doomed"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := c.KillSession(ctx, "doomed"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	panes, err := c.ListPanes(ctx)
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	if len(panes) != 0 {
		t.Fatalf("want 0 panes after kill, got %d", len(panes))
	}
}

func TestNewSessionArgsIncludesCommandAndArgs(t *testing.T) {
	got := newSessionArgs(NewSessionOpts{
		Name: "argus", Cwd: "/p", Command: "claude", Args: []string{"do the thing"},
	})
	// The command and each arg must be separate trailing elements (tmux execs
	// them directly, so no shell quoting and newlines survive).
	if len(got) < 2 || got[len(got)-2] != "claude" || got[len(got)-1] != "do the thing" {
		t.Fatalf("args tail = %#v, want [... claude \"do the thing\"]", got)
	}
	// Sanity: cwd is passed via -c.
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-c /p") {
		t.Fatalf("missing cwd flag in %q", joined)
	}
}

func TestNewSessionArgsIncludesEnvBeforeCommand(t *testing.T) {
	got := newSessionArgs(NewSessionOpts{
		Name: "argus", Command: "opencode", Args: []string{"--session", "ses_1"},
		Env: []string{"FOO=bar"},
	})
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-e FOO=bar") {
		t.Fatalf("missing env flag in %q", joined)
	}
	// -e must precede the command so tmux applies it to the session, not as an arg.
	ei, ci := -1, -1
	for i, a := range got {
		switch a {
		case "FOO=bar":
			ei = i
		case "opencode":
			ci = i
		}
	}
	if ei < 0 || ci < 0 || ei > ci {
		t.Fatalf("env must appear before command: %#v", got)
	}
}

func TestNewSessionArgsOmitsArgsWhenEmpty(t *testing.T) {
	got := newSessionArgs(NewSessionOpts{Name: "x", Command: "claude"})
	if got[len(got)-1] != "claude" {
		t.Fatalf("trailing element = %q, want \"claude\"", got[len(got)-1])
	}
}

func TestAttachArgs(t *testing.T) {
	// Private socket: argv includes -u, -L <socket> and the config-less -f /dev/null.
	priv := New("argus").attachArgs("/usr/bin/tmux", "work")
	want := []string{"/usr/bin/tmux", "-u", "-L", "argus", "-f", "/dev/null", "attach-session", "-t", "work"}
	if strings.Join(priv, " ") != strings.Join(want, " ") {
		t.Fatalf("attachArgs = %#v, want %#v", priv, want)
	}
	// Default server: never touched — no -L, and no -f (argus must not alter how
	// the user's own tmux loads its config).
	def := New("").attachArgs("/usr/bin/tmux", "work")
	if strings.Join(def, " ") != "/usr/bin/tmux -u attach-session -t work" {
		t.Fatalf("default attachArgs = %#v", def)
	}
}

func TestAttachEnvGuaranteesTerm(t *testing.T) {
	got := attachEnv([]string{"HOME=/home/argus"})
	if !slices.Contains(got, "TERM="+fallbackTerm) {
		t.Fatalf("attachEnv without TERM = %#v, want a %s entry", got, fallbackTerm)
	}
	got = attachEnv([]string{"HOME=/home/argus", "TERM="})
	if slices.Contains(got, "TERM=") {
		t.Fatalf("attachEnv kept the empty TERM: %#v", got)
	}
	if !slices.Contains(got, "TERM="+fallbackTerm) {
		t.Fatalf("attachEnv with empty TERM = %#v, want a %s entry", got, fallbackTerm)
	}
	got = attachEnv([]string{"TERM=tmux-256color", "HOME=/home/argus"})
	if !slices.Equal(got, []string{"HOME=/home/argus", "TERM=" + fallbackTerm}) {
		t.Fatalf("attachEnv kept an inherited TERM: %#v, want %s (tmux sends cursor styles only to xterm*)", got, fallbackTerm)
	}
}

func TestSetOption(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	if _, err := c.NewSession(ctx, NewSessionOpts{Name: "opt"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := c.SetOption(ctx, "opt", "status", "off"); err != nil {
		t.Fatalf("SetOption: %v", err)
	}
	// Confirm it took effect by reading the session's status option back.
	out, err := c.run(ctx, "show-options", "-t", "opt", "-v", "status")
	if err != nil {
		t.Fatalf("show-options: %v", err)
	}
	if strings.TrimSpace(out) != "off" {
		t.Fatalf("status = %q, want \"off\"", strings.TrimSpace(out))
	}
}

func TestGroupedMirrorLifecycle(t *testing.T) {
	c := testClient(t) // isolated -L socket, killed on cleanup
	ctx := context.Background()
	// origin session with a shell
	if _, err := c.NewSession(ctx, NewSessionOpts{Name: "origin", Command: "sh"}); err != nil {
		t.Fatal(err)
	}
	if err := c.NewGroupedSession(ctx, "_argus-mirror-t1_", "origin"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetOption(ctx, "_argus-mirror-t1_", "status", "off"); err != nil {
		t.Fatal(err)
	}
	info, err := c.WindowInfo(ctx, "_argus-mirror-t1_")
	if err != nil {
		t.Fatal(err)
	}
	if info.Panes != 1 || info.ActivePane == "" {
		t.Fatalf("unexpected window info: %+v", info)
	}
	if err := c.KillSession(ctx, "_argus-mirror-t1_"); err != nil {
		t.Fatal(err)
	}
}

func TestNewWindowCreatesTheSessionThenAddsWindows(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	dir := t.TempDir()

	if ws, err := c.ListWindows(ctx, "terminal"); err != nil || ws != nil {
		t.Fatalf("ListWindows with no server = %v, %v; want nil, nil", ws, err)
	}
	w1, err := c.NewWindow(ctx, "terminal", dir)
	if err != nil {
		t.Fatalf("NewWindow (no session): %v", err)
	}
	w2, err := c.NewWindow(ctx, "terminal", dir)
	if err != nil {
		t.Fatalf("NewWindow (session exists): %v", err)
	}
	ws, err := c.ListWindows(ctx, "terminal")
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(ws) != 2 || ws[0].ID != w1 || ws[1].ID != w2 {
		t.Fatalf("windows = %+v, want [%s %s] in order", ws, w1, w2)
	}
	if !ws[0].AutoRename || ws[0].CurrentCommand == "" || !endsWith(ws[0].CurrentPath, baseOf(dir)) {
		t.Errorf("window = %+v, want auto-rename, a command, and cwd under %s", ws[0], dir)
	}
}

func TestListWindowsOfAMissingSessionIsEmpty(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	if _, err := c.NewSession(ctx, NewSessionOpts{Name: "other"}); err != nil {
		t.Fatal(err)
	}
	if ws, err := c.ListWindows(ctx, "terminal"); err != nil || ws != nil {
		t.Fatalf("ListWindows = %v, %v; want nil, nil", ws, err)
	}
}

func TestRenameWindowStopsAutoRename(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	w, err := c.NewWindow(ctx, "terminal", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RenameWindow(ctx, w, "-build"); err != nil {
		t.Fatalf("RenameWindow: %v", err)
	}
	ws, _ := c.ListWindows(ctx, "terminal")
	if len(ws) != 1 || ws[0].Name != "-build" || ws[0].AutoRename {
		t.Fatalf("windows = %+v, want one named -build with auto-rename off", ws)
	}
}

func TestRenameWindowKeepsHashLiteral(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	w, err := c.NewWindow(ctx, "terminal", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const name = "a #S #{session_name} #(echo hi) ##"
	if err := c.RenameWindow(ctx, w, name); err != nil {
		t.Fatalf("RenameWindow: %v", err)
	}
	// Newer tmux stores '#' in window names as '_'.
	ws, _ := c.ListWindows(ctx, "terminal")
	if len(ws) != 1 || (ws[0].Name != name && ws[0].Name != strings.ReplaceAll(name, "#", "_")) {
		t.Fatalf("windows = %+v, want one named %q", ws, name)
	}
}

func TestKillWindow(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	w1, _ := c.NewWindow(ctx, "terminal", t.TempDir())
	w2, _ := c.NewWindow(ctx, "terminal", t.TempDir())
	if err := c.KillWindow(ctx, w1); err != nil {
		t.Fatalf("KillWindow: %v", err)
	}
	ws, _ := c.ListWindows(ctx, "terminal")
	if len(ws) != 1 || ws[0].ID != w2 {
		t.Fatalf("windows = %+v, want only %s", ws, w2)
	}
}

func TestLinkedSingleWindowSession(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	w1, _ := c.NewWindow(ctx, "terminal", t.TempDir())
	w2, _ := c.NewWindow(ctx, "terminal", t.TempDir())

	first, err := c.NewEmptySession(ctx, "mirror")
	if err != nil {
		t.Fatalf("NewEmptySession: %v", err)
	}
	if err := c.LinkWindow(ctx, w1, "mirror"); err != nil {
		t.Fatalf("LinkWindow: %v", err)
	}
	if err := c.KillWindow(ctx, first); err != nil {
		t.Fatalf("KillWindow first: %v", err)
	}
	if ws, _ := c.ListWindows(ctx, "mirror"); len(ws) != 1 || ws[0].ID != w1 {
		t.Fatalf("mirror windows = %+v, want only %s", ws, w1)
	}
	if err := c.KillSession(ctx, "mirror"); err != nil {
		t.Fatal(err)
	}
	if ws, _ := c.ListWindows(ctx, "terminal"); len(ws) != 2 || ws[0].ID != w1 || ws[1].ID != w2 {
		t.Fatalf("terminal windows after mirror kill = %+v, want both kept", ws)
	}
}

func TestPaneWindowID(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	pane, err := c.NewSession(ctx, NewSessionOpts{Name: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := c.PaneWindowID(ctx, pane)
	if err != nil || w == "" || w[0] != '@' {
		t.Fatalf("PaneWindowID = %q, %v; want @N", w, err)
	}
	ws, _ := c.ListWindows(ctx, "agent")
	if len(ws) != 1 || ws[0].ID != w {
		t.Fatalf("agent windows = %+v, want %s", ws, w)
	}
}

func TestNewWindowConcurrentFirstCreates(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	dir := t.TempDir()
	errs := make(chan error, 4)
	for range 4 {
		go func() {
			_, err := c.NewWindow(ctx, "terminal", dir)
			errs <- err
		}()
	}
	for range 4 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent NewWindow: %v", err)
		}
	}
	if ws, _ := c.ListWindows(ctx, "terminal"); len(ws) != 4 {
		t.Fatalf("windows = %d, want 4", len(ws))
	}
}
