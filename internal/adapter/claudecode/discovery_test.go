package claudecode

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func TestIsClaudePane(t *testing.T) {
	claudeTTYs := map[string]int{"ttys002": 4242}

	// Direct launch: command is literally "claude".
	if !isClaudePane(tmux.Pane{CurrentCommand: "claude"}, nil) {
		t.Error("claude command should match")
	}
	// Disguised (version-named) command, but its tty runs claude in foreground.
	if !isClaudePane(tmux.Pane{CurrentCommand: "2.1.173", TTY: "/dev/ttys002"}, claudeTTYs) {
		t.Error("version-named pane on a claude tty should match")
	}
	// A shell pane whose tty is not in the claude set.
	if isClaudePane(tmux.Pane{CurrentCommand: "zsh", TTY: "/dev/ttys009"}, claudeTTYs) {
		t.Error("zsh should not match")
	}
}

func TestParseForegroundClaude(t *testing.T) {
	psOut := strings.Join([]string{
		"  111 ttys002 Ss   -zsh",
		"  222 ttys002 S+   claude",
		"  333 ttys009 Ss+  -zsh",
		"  444 ttys005 S+   node /x/cli.js",
		"  555 ttys010 S+   /opt/homebrew/bin/claude --continue",
		"  666 ??      S    some-daemon",
	}, "\n")

	got := parseForegroundClaude(psOut)
	if got["ttys002"] != 222 {
		t.Errorf("ttys002 should map to claude pid 222, got %d", got["ttys002"])
	}
	if got["ttys010"] != 555 {
		t.Errorf("ttys010 should map to claude pid 555 (abs path), got %d", got["ttys010"])
	}
	if _, ok := got["ttys009"]; ok {
		t.Error("ttys009 foreground is zsh, not claude")
	}
	if _, ok := got["ttys005"]; ok {
		t.Error("ttys005 foreground is node, not claude")
	}
}

// TestScanOnceReconciles spawns a real tmux session and verifies the discoverer
// wires panes into the registry. It matches all panes (not just claude) so it
// needs no real claude binary.
func TestScanOnceReconciles(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	client := tmux.New("argus-disc-test")
	t.Cleanup(func() { _ = client.KillServer(ctx) })

	if _, err := client.NewSession(ctx, tmux.NewSessionOpts{Name: "s1"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	reg := registry.New()
	d := NewDiscoverer(reg, map[session.TmuxServer]*tmux.Client{
		session.TmuxServerArgus: client,
	})
	d.match = func(tmux.Pane) bool { return true } // match everything

	if err := d.ScanOnce(ctx); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	snap := reg.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 discovered session, got %d", len(snap))
	}
	if snap[0].Tool != Tool || snap[0].Source != session.SourceDiscovered {
		t.Fatalf("unexpected session: %+v", snap[0])
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
