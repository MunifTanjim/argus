package tmux

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// TestParsePaneSeparators verifies that parsePane handles the field separator
// both as a raw 0x1F byte (tmux <3.4) and as the literal "\037" octal escape
// that tmux >=3.4 emits for non-printable bytes in -F output.
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

// TestIsFocusedDetached checks that a detached session's pane (no client attached)
// is never reported focused — the no-false-positive case we can verify headlessly
// (attaching a client needs a real terminal).
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

// testClient returns a Client bound to a throwaway, isolated tmux server socket
// and ensures the server is killed when the test finishes. Tests are skipped if
// tmux is not installed.
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
