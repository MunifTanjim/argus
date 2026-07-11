package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

// TestWindowVisible checks the window-current / attached-client logic that decides
// whether a pane's window is on a user's screen (ignores pane_active).
func TestWindowVisible(t *testing.T) {
	sep := "\x1f"
	line := func(id, windowActive, attached, session string) string {
		return strings.Join([]string{id, windowActive, attached, session}, sep)
	}
	out := strings.Join([]string{
		line("%1", "1", "1", "origin"),       // visible: current window, attached
		line("%2", "1", "0", "origin"),       // not visible: no client attached
		line("%3", "0", "1", "origin"),       // not visible: window not current
		line("%4", "1", "1", "origin"),       // visible: inactive pane still shares the window
		line("%5", "1", "2", "origin"),       // visible: two clients attached
		line("%6", "1", "1", "argus-mirror"), // ignored: our own mirror session
	}, "\n") + "\n"

	ignore := func(s string) bool { return s == "argus-mirror" }
	want := map[string]bool{"%1": true, "%2": false, "%3": false, "%4": true, "%5": true, "%6": false, "%absent": false}
	for id, exp := range want {
		got, err := windowVisible(out, id, ignore)
		if err != nil {
			t.Fatalf("windowVisible(%q): %v", id, err)
		}
		if got != exp {
			t.Errorf("windowVisible(%q) = %v, want %v", id, got, exp)
		}
	}
}

// TestWindowVisibleEscapedSep verifies the tmux >=3.4 "\037"-escaped separator is
// normalized, matching parsePane's handling.
func TestWindowVisibleEscapedSep(t *testing.T) {
	got, err := windowVisible(strings.Join([]string{"%9", "1", "1", "origin"}, `\037`), "%9", nil)
	if err != nil {
		t.Fatalf("windowVisible: %v", err)
	}
	if !got {
		t.Fatal("escaped-separator line not parsed as visible")
	}
}

// TestWindowVisibleDetached checks a detached pane's window is never reported
// visible — verifiable headlessly (attaching needs a real terminal).
func TestWindowVisibleDetached(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	pane, err := c.NewSession(ctx, NewSessionOpts{Name: "s1"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	visible, err := c.WindowVisible(ctx, pane, nil)
	if err != nil {
		t.Fatalf("WindowVisible: %v", err)
	}
	if visible {
		t.Errorf("WindowVisible(%q) = true; want false (no client attached)", pane)
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

func TestNewSessionArgsOmitsArgsWhenEmpty(t *testing.T) {
	got := newSessionArgs(NewSessionOpts{Name: "x", Command: "claude"})
	if got[len(got)-1] != "claude" {
		t.Fatalf("trailing element = %q, want \"claude\"", got[len(got)-1])
	}
}

func TestAttachArgs(t *testing.T) {
	// Private socket: argv includes -L <socket> and the config-less -f /dev/null.
	priv := New("argus").attachArgs("/usr/bin/tmux", "work")
	want := []string{"/usr/bin/tmux", "-L", "argus", "-f", "/dev/null", "attach-session", "-t", "work"}
	if strings.Join(priv, " ") != strings.Join(want, " ") {
		t.Fatalf("attachArgs = %#v, want %#v", priv, want)
	}
	// Default server: never touched — no -L, and no -f (argus must not alter how
	// the user's own tmux loads its config).
	def := New("").attachArgs("/usr/bin/tmux", "work")
	if strings.Join(def, " ") != "/usr/bin/tmux attach-session -t work" {
		t.Fatalf("default attachArgs = %#v", def)
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
