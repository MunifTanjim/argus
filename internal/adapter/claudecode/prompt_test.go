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
		cmds := a.ModBridge().Wait(ctx, sessionID, time.Minute, nil)
		for _, c := range cmds {
			a.ModBridge().Ack(c.ID, "", true, nil)
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
			go func() { a.ModBridge().Wait(ctx, "s1", time.Minute, nil); close(done) }()
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

func TestSendPromptQueuesWhileWorking(t *testing.T) {
	a, _ := newWiredAdapter(t)
	got := modPoll(a, context.Background(), "s1")
	waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })
	s := session.Session{ID: "x", AgentSessionID: "s1", Status: session.StatusWorking}
	if err := a.SendPrompt(context.Background(), s, "steer"); err != nil {
		t.Fatal(err)
	}
	if cmds := <-got; len(cmds) != 1 || cmds[0].Kind != modbridge.KindPrompt || cmds[0].Text != "steer" {
		t.Fatalf("poll got %v", cmds)
	}
}

func TestSendPromptRefusesAPendingInteraction(t *testing.T) {
	a, _ := newWiredAdapter(t)
	got := modPoll(a, context.Background(), "s1")
	waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })
	s := session.Session{ID: "x", AgentSessionID: "s1", Status: session.StatusAwaitingInput,
		Interaction: &session.Interaction{Kind: session.InteractionPermission}}
	if err := a.SendPrompt(context.Background(), s, "hi"); !errors.Is(err, api.ErrSessionBusy) {
		t.Fatalf("err = %v, want ErrSessionBusy", err)
	}
	if _, err := a.FillPrompt(context.Background(), s, "hi"); !errors.Is(err, api.ErrSessionBusy) {
		t.Fatalf("fill err = %v, want ErrSessionBusy", err)
	}
	select {
	case cmds := <-got:
		t.Fatalf("a session with a pending interaction was sent %v", cmds)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFillPromptSendsAFillCommand(t *testing.T) {
	a, _ := newWiredAdapter(t)
	got := modPoll(a, context.Background(), "s1")
	waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })
	s := session.Session{ID: "x", AgentSessionID: "s1", Status: session.StatusWorking}
	if filled, err := a.FillPrompt(context.Background(), s, "steer"); err != nil || !filled {
		t.Fatalf("filled = %v, err = %v", filled, err)
	}
	if cmds := <-got; len(cmds) != 1 || cmds[0].Kind != modbridge.KindFill || cmds[0].Text != "steer" {
		t.Fatalf("poll got %v", cmds)
	}
}

func TestFillPromptSendsASlashCommandAsAPrompt(t *testing.T) {
	a, _ := newWiredAdapter(t)
	got := modPoll(a, context.Background(), "s1")
	waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })
	s := session.Session{ID: "x", AgentSessionID: "s1", Status: session.StatusWorking}
	if filled, err := a.FillPrompt(context.Background(), s, "/compact"); err != nil || filled {
		t.Fatalf("filled = %v, err = %v; want sent, not filled", filled, err)
	}
	if cmds := <-got; len(cmds) != 1 || cmds[0].Kind != modbridge.KindPrompt || cmds[0].Text != "/compact" {
		t.Fatalf("poll got %v", cmds)
	}
}

func TestFillPromptRefusedByADialogIsBusy(t *testing.T) {
	a, _ := newWiredAdapter(t)
	go func() {
		for _, c := range a.ModBridge().Wait(context.Background(), "s1", time.Minute, nil) {
			a.ModBridge().Ack(c.ID, modbridge.FailBusy, false, nil)
		}
	}()
	waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })
	s := session.Session{ID: "x", AgentSessionID: "s1", Status: session.StatusWorking}
	if _, err := a.FillPrompt(context.Background(), s, "steer"); !errors.Is(err, api.ErrSessionBusy) {
		t.Fatalf("err = %v, want ErrSessionBusy", err)
	}
}

func TestFillPromptOnlyAcceptedIsAnError(t *testing.T) {
	a, _ := newWiredAdapter(t)
	go func() {
		for _, c := range a.ModBridge().Wait(context.Background(), "s1", time.Minute, nil) {
			a.ModBridge().Ack(c.ID, "", false, nil)
		}
	}()
	waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })
	s := session.Session{ID: "x", AgentSessionID: "s1", Status: session.StatusWorking}
	_, err := a.FillPrompt(context.Background(), s, "steer")
	if err == nil || errors.Is(err, api.ErrSessionBusy) {
		t.Fatalf("err = %v, want a not-filled error", err)
	}
}

func TestQueuesPromptsFollowsTheModConnection(t *testing.T) {
	a := New().(*ccAdapter)
	if !a.QueuesPrompts(session.Session{Input: session.InputAPI}) {
		t.Fatal("a mod-connected session should queue prompts")
	}
	if a.QueuesPrompts(session.Session{Input: session.InputPane}) {
		t.Fatal("a keystrokes-only session must not queue prompts")
	}
}

func TestSendShellInputSendsAShellCommand(t *testing.T) {
	a, _ := newWiredAdapter(t)
	if _, ok := adapter.Adapter(a).(adapter.ShellInput); !ok {
		t.Fatal("claude adapter must implement adapter.ShellInput")
	}
	if !a.IsShellInput("! ls") || a.IsShellInput("ls") || a.IsShellInput(" !ls") {
		t.Fatal("IsShellInput must match only a leading !")
	}
	got := modPoll(a, context.Background(), "s1")
	waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })
	paneless := session.Session{ID: "x", AgentSessionID: "s1", Status: session.StatusWorking}
	if _, err := a.SendShellInput(context.Background(), paneless, "! ls"); !errors.Is(err, api.ErrShellNeedsPane) {
		t.Fatalf("paneless: err = %v, want ErrShellNeedsPane", err)
	}
	s := session.Session{ID: "x", AgentSessionID: "s1", Status: session.StatusWorking, Tmux: session.TmuxLocation{PaneID: "%1"}}
	if submit, err := a.SendShellInput(context.Background(), s, "! ls"); err != nil || submit {
		t.Fatalf("submit = %v, err = %v; want the mod to type the Enter", submit, err)
	}
	if cmds := <-got; len(cmds) != 1 || cmds[0].Kind != modbridge.KindShell || cmds[0].Text != "! ls" {
		t.Fatalf("poll got %v", cmds)
	}
}

func TestSendShellInputIsBusyUnderAnInteractionOrDialog(t *testing.T) {
	a, _ := newWiredAdapter(t)
	pending := session.Session{ID: "x", AgentSessionID: "s1", Status: session.StatusAwaitingInput, Tmux: session.TmuxLocation{PaneID: "%1"},
		Interaction: &session.Interaction{Kind: session.InteractionPermission}}
	if _, err := a.SendShellInput(context.Background(), pending, "! ls"); !errors.Is(err, api.ErrSessionBusy) {
		t.Fatalf("pending interaction: err = %v, want ErrSessionBusy", err)
	}
	go func() {
		for _, c := range a.ModBridge().Wait(context.Background(), "s1", time.Minute, nil) {
			a.ModBridge().Ack(c.ID, modbridge.FailBusy, false, nil)
		}
	}()
	waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })
	s := session.Session{ID: "x", AgentSessionID: "s1", Status: session.StatusIdle, Tmux: session.TmuxLocation{PaneID: "%1"}}
	if _, err := a.SendShellInput(context.Background(), s, "! ls"); !errors.Is(err, api.ErrSessionBusy) {
		t.Fatalf("dialog: err = %v, want ErrSessionBusy", err)
	}
}
