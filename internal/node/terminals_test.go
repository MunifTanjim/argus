package node

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

var termSockets sync.Map // *tmux.Client -> socket name

func testSocketOf(t *testing.T, c *tmux.Client) string {
	t.Helper()
	s, ok := termSockets.Load(c)
	if !ok {
		t.Fatal("client was not made by terminalNode")
	}
	return s.(string)
}

// terminalNode returns a node whose argus server is a throwaway tmux socket,
// with HOME on a temp dir so cwds compare against "~". SHELL is plain sh: the
// developer's shell config can run slow startup work in the empty HOME and
// swallow typed input.
func terminalNode(t *testing.T) (*Node, *tmux.Client) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/sh")
	c, socket := newTestClientSocket(t)
	termSockets.Store(c, socket)
	d := newNode(map[session.TmuxServer]*tmux.Client{session.TmuxServerArgus: c})
	d.mirrorPrefix, d.mirrorSuffix = "_", "_"
	if !d.caps.Terminal {
		t.Skip("tmux unavailable")
	}
	return d, c
}

func listTerminals(t *testing.T, d *Node) []api.Terminal {
	t.Helper()
	res, err := d.handleTerminalList(context.Background(), nil)
	if err != nil {
		t.Fatalf("terminal.list: %v", err)
	}
	return res.(api.TerminalListResult).Terminals
}

func createTerminal(t *testing.T, d *Node) api.Terminal {
	t.Helper()
	res, err := d.handleTerminalCreate(context.Background(), mustJSON(api.TerminalCreateParams{}))
	if err != nil {
		t.Fatalf("terminal.create: %v", err)
	}
	return res.(api.Terminal)
}

// notesOf waits briefly for n's background notifications and counts method.
func notesOf(n *recordingNotifier, method string) int {
	c := 0
	quiet := 2 * time.Second
	for {
		select {
		case note := <-n.other:
			if note.Method == method {
				c++
				quiet = 100 * time.Millisecond
			}
		case <-time.After(quiet):
			return c
		}
	}
}

func TestTerminalCreateListRenameKill(t *testing.T) {
	d, _ := terminalNode(t)
	ctx := context.Background()
	if got := listTerminals(t, d); len(got) != 0 {
		t.Fatalf("list before create = %+v, want empty", got)
	}

	a := createTerminal(t, d)
	b := createTerminal(t, d)
	if a.ID == "" || a.Cwd != "~" || a.Command == "" || a.Name != "" {
		t.Fatalf("created = %+v, want an id, cwd ~, a command, no name", a)
	}
	got := listTerminals(t, d)
	if len(got) != 2 || got[0].ID != a.ID || got[1].ID != b.ID {
		t.Fatalf("list = %+v, want [%s %s]", got, a.ID, b.ID)
	}

	if _, err := d.handleTerminalRename(ctx, mustJSON(api.TerminalRenameParams{TerminalID: a.ID, Name: " build "})); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := listTerminals(t, d); got[0].Name != "build" {
		t.Errorf("name after rename = %q, want build", got[0].Name)
	}
	_, err := d.handleTerminalRename(ctx, mustJSON(api.TerminalRenameParams{TerminalID: a.ID, Name: "  "}))
	wantErr(t, "rename to a blank name", err, "name required")

	if _, err := d.handleTerminalKill(ctx, mustJSON(api.TerminalRef{TerminalID: a.ID})); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if got := listTerminals(t, d); len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("list after kill = %+v, want only %s", got, b.ID)
	}
}

func TestTerminalsUnsupportedWithoutTmux(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	if got := listTerminals(t, d); len(got) != 0 {
		t.Fatalf("list = %+v, want empty", got)
	}
	_, err := d.handleTerminalCreate(context.Background(), mustJSON(api.TerminalCreateParams{}))
	if err == nil || err.Error() != "terminals not supported on this node" {
		t.Fatalf("create err = %v, want not supported", err)
	}
}

func TestTerminalManagementRefusesAgentWindows(t *testing.T) {
	d, c := terminalNode(t)
	ctx := context.Background()
	pane, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: "agent", Command: "sh"})
	if err != nil {
		t.Fatal(err)
	}
	agentWin, err := c.PaneWindowID(ctx, pane)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.handleTerminalKill(ctx, mustJSON(api.TerminalRef{TerminalID: agentWin}))
	wantErr(t, "kill of an agent window", err, "unknown terminal: "+agentWin)
	_, err = d.handleTerminalRename(ctx, mustJSON(api.TerminalRenameParams{TerminalID: agentWin, Name: "x"}))
	wantErr(t, "rename of an agent window", err, "unknown terminal: "+agentWin)
	if ws, _ := c.ListWindows(ctx, "agent"); len(ws) != 1 || !ws[0].AutoRename {
		t.Fatalf("agent windows = %+v, want the window kept and unrenamed", ws)
	}
}

func TestTerminalChangesNotifyConnections(t *testing.T) {
	d, _ := terminalNode(t)
	n := newRecordingNotifier()
	d.registerConn(n)
	a := createTerminal(t, d)
	if _, err := d.handleTerminalKill(context.Background(), mustJSON(api.TerminalRef{TerminalID: a.ID})); err != nil {
		t.Fatal(err)
	}
	if got := notesOf(n, api.MethodTerminalChanged); got != 2 {
		t.Errorf("terminal.changed sent %d times, want 2 (create, kill)", got)
	}
}

func TestTerminalListJSON(t *testing.T) {
	d, _ := terminalNode(t)
	createTerminal(t, d)
	res, _ := d.handleTerminalList(context.Background(), nil)
	b, _ := json.Marshal(res)
	var back api.TerminalListResult
	if err := json.Unmarshal(b, &back); err != nil || len(back.Terminals) != 1 {
		t.Fatalf("round trip = %s, %v", b, err)
	}
}

func TestTerminalListKeepsCreationOrderAfterAKill(t *testing.T) {
	d, _ := terminalNode(t)
	a := createTerminal(t, d)
	b := createTerminal(t, d)
	c := createTerminal(t, d)
	if _, err := d.handleTerminalKill(context.Background(), mustJSON(api.TerminalRef{TerminalID: a.ID})); err != nil {
		t.Fatal(err)
	}
	n := createTerminal(t, d)
	var got []string
	for _, x := range listTerminals(t, d) {
		got = append(got, x.ID)
	}
	if want := []string{b.ID, c.ID, n.ID}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v (creation order)", got, want)
	}
}

func wantErr(t *testing.T, what string, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Errorf("%s: err = %v, want %q", what, err, want)
	}
}
