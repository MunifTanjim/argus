package codex

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func TestParseCodexResumeProcs(t *testing.T) {
	ps := `4154494 ?        Ss   tmux -L cxprobe new-session -d -s t codex resume 01a1-aaaa
4154496 pts/23   Ssl+ codex resume 01a1-aaaa
4154500 pts/24   Ssl+ /opt/homebrew/bin/codex resume --yolo 01a1-bbbb
4154501 pts/25   Ssl+ codex
4154502 pts/26   Ssl+ vim resume.txt
4154503 pts/27   Ssl+ codex resume --last`
	got := parseCodexResumeProcs(ps)
	want := map[string]string{"01a1-aaaa": "pts/23", "01a1-bbbb": "pts/24"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}

func TestReconcilePanesAdoptsAndDetaches(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	d.threads["t1"] = &threadEntry{cwd: "/w", path: "/r.jsonl"}
	d.upsert("t1", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})

	d.reconcilePanes(map[string]paneRef{"t1": {server: session.TmuxServerArgus, paneID: "%4"}, "unknown": {paneID: "%5"}})
	s, _ := reg.Get(registry.AgentSessionKey(Agent, "t1"))
	if s.Tmux.PaneID != "%4" || s.Input != session.InputAPI {
		t.Fatalf("after adopt: pane=%q input=%v", s.Tmux.PaneID, s.Input)
	}
	if _, ok := d.panes["unknown"]; ok {
		t.Fatal("panes for untracked threads must not be adopted")
	}

	d.reconcilePanes(map[string]paneRef{})
	s, _ = reg.Get(registry.AgentSessionKey(Agent, "t1"))
	if s.Tmux.PaneID != "" {
		t.Fatalf("after pane gone: pane=%q; want detached", s.Tmux.PaneID)
	}
}

func TestScanPanesKeepsPanesOnListError(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	reg := registry.New()
	d := newDiscoverer(reg, nil)

	oldRunPS := runPS
	defer func() { runPS = oldRunPS }()
	runPS = func() (string, error) {
		return "4154496 pts/23   Ssl+ codex resume t1\n", nil
	}

	// Add a server with a failing ListPanes.
	d.servers = append(d.servers, serverClient{
		server: session.TmuxServerArgus,
		client: nil, // not used; listPanes is stubbed
	})
	d.listPanes = func(ctx context.Context, sc serverClient) ([]tmux.Pane, error) {
		return nil, errors.New("ListPanes failed")
	}

	// Scan should return ok=false, keeping existing panes.
	bound, ok := d.scanPanes(context.Background())
	if ok {
		t.Fatal("scanPanes should return ok=false when ListPanes fails")
	}
	if bound != nil {
		t.Fatalf("scanPanes should return nil bound map, got %v", bound)
	}
}

func TestReconcilePanesSkipsForgottenThread(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	reg := registry.New()
	d := newDiscoverer(reg, nil)

	// reconcilePanes with a pane for a thread not in d.threads should not
	// create a session.
	d.reconcilePanes(map[string]paneRef{"unknown": {server: session.TmuxServerArgus, paneID: "%4"}})

	_, found := reg.Get(registry.AgentSessionKey(Agent, "unknown"))
	if found {
		t.Fatal("reconcilePanes should not create session for untracked thread")
	}
	if _, ok := d.panes["unknown"]; ok {
		t.Fatal("untracked pane should not be adopted")
	}
}

func TestReconcilePanesMovesPane(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	d.threads["t1"] = &threadEntry{cwd: "/w", path: "/r.jsonl"}
	d.upsert("t1", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})

	d.reconcilePanes(map[string]paneRef{"t1": {server: session.TmuxServerArgus, paneID: "%4"}})
	s, _ := reg.Get(registry.AgentSessionKey(Agent, "t1"))
	if s.Tmux.PaneID != "%4" {
		t.Fatalf("after first adopt: pane=%q; want %%4", s.Tmux.PaneID)
	}

	d.reconcilePanes(map[string]paneRef{"t1": {server: session.TmuxServerArgus, paneID: "%7"}})
	s, _ = reg.Get(registry.AgentSessionKey(Agent, "t1"))
	if s.Tmux.PaneID != "%7" {
		t.Fatalf("after move: pane=%q; want %%7", s.Tmux.PaneID)
	}
}
