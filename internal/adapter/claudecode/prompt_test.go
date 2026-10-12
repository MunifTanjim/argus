package claudecode

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/modbridge"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func newWiredAdapter(t *testing.T) (*ccAdapter, *registry.Registry) {
	t.Helper()
	reg := registry.New()
	a := New().(*ccAdapter)
	a.NewDiscoverer(reg, map[session.TmuxServer]*tmux.Client{})
	return a, reg
}

// modPoll stands in for the mod: it opens one poll for sessionID and acks
// whatever command it receives.
func modPoll(a *ccAdapter, ctx context.Context, sessionID string) <-chan []modbridge.Command {
	got := make(chan []modbridge.Command, 1)
	go func() {
		cmds := a.ModBridge().Wait(ctx, sessionID, time.Minute)
		for _, c := range cmds {
			a.ModBridge().Ack(c.ID, "")
		}
		got <- cmds
	}()
	return got
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 1s")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSendPromptWithoutModIsNoTerminalControl(t *testing.T) {
	a, _ := newWiredAdapter(t)
	err := a.SendPrompt(context.Background(), session.Session{ID: "x", AgentSessionID: "s1"}, "hi")
	if !errors.Is(err, api.ErrNoTerminalControl) {
		t.Fatalf("err = %v, want ErrNoTerminalControl", err)
	}
}

func TestSendPromptRoutesPromptsAndSlashCommands(t *testing.T) {
	a, _ := newWiredAdapter(t)
	for _, tc := range []struct {
		text string
		want modbridge.Command
	}{
		{"fix the bug", modbridge.Command{Kind: modbridge.KindPrompt, Text: "fix the bug"}},
		{"/compact keep tests", modbridge.Command{Kind: modbridge.KindPrompt, Text: "/compact keep tests"}},
	} {
		got := modPoll(a, context.Background(), "s1")
		waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })
		if err := a.SendPrompt(context.Background(), session.Session{ID: "x", AgentSessionID: "s1"}, tc.text); err != nil {
			t.Fatal(err)
		}
		if cmds := <-got; len(cmds) != 1 || cmds[0].Kind != tc.want.Kind || cmds[0].Text != tc.want.Text {
			t.Fatalf("%q: poll got %v, want [%v]", tc.text, cmds, tc.want)
		}
	}
}

func TestClaudeInputFollowsModConnection(t *testing.T) {
	old := modGrace
	modGrace = 50 * time.Millisecond
	t.Cleanup(func() { modGrace = old })

	for _, tc := range []struct {
		name    string
		paneID  string
		without session.InputMode
	}{
		{"pane", "%1", session.InputPane},
		{"paneless", "", session.InputNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, reg := newWiredAdapter(t)
			u := registry.HookUpdate{Agent: Agent, AgentSessionID: "s1", Status: session.StatusIdle}
			if tc.paneID != "" {
				u.Server, u.PaneID = session.TmuxServerDefault, tc.paneID
			}
			s, _ := reg.ApplyHook(u)
			if s.Input != tc.without {
				t.Fatalf("Input = %q before the mod connects, want %q", s.Input, tc.without)
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { a.ModBridge().Wait(ctx, "s1", time.Minute); close(done) }()
			waitUntil(t, func() bool { got, _ := reg.Get(s.ID); return got.Input == session.InputAPI })

			cancel()
			<-done
			waitUntil(t, func() bool { got, _ := reg.Get(s.ID); return got.Input == tc.without })
		})
	}
}

func TestDismissRunsExitThroughMod(t *testing.T) {
	a, _ := newWiredAdapter(t)
	sess := session.Session{ID: "x", AgentSessionID: "s1"}
	if err := a.Dismiss(context.Background(), sess); !errors.Is(err, api.ErrNoTerminalControl) {
		t.Fatalf("err = %v without a mod, want ErrNoTerminalControl", err)
	}
	got := modPoll(a, context.Background(), "s1")
	waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })
	if err := a.Dismiss(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if cmds := <-got; len(cmds) != 1 || cmds[0].Kind != modbridge.KindExit {
		t.Fatalf("poll got %v, want exit", cmds)
	}
}

func TestClaudeAdapterIsPrompterAndModBridged(t *testing.T) {
	var a adapter.Adapter = New()
	if _, ok := a.(adapter.Prompter); !ok {
		t.Fatal("claude adapter must implement adapter.Prompter")
	}
	if _, ok := a.(adapter.Dismisser); !ok {
		t.Fatal("claude adapter must implement adapter.Dismisser")
	}
	if _, ok := a.(adapter.ModBridged); !ok {
		t.Fatal("claude adapter must implement adapter.ModBridged")
	}
}
