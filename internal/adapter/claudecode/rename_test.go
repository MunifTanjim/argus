package claudecode

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/modbridge"
	"github.com/MunifTanjim/argus/internal/session"
)

// Without a mod connection the rename fails and names the missing connection.
func TestRenameSessionWithoutModIsNoTerminalControl(t *testing.T) {
	a, _ := newWiredAdapter(t)
	_, err := a.RenameSession(context.Background(), session.Session{ID: "x", AgentSessionID: "s1"}, "New name")
	if !errors.Is(err, api.ErrNoTerminalControl) || !strings.Contains(err.Error(), "Argus plugin") {
		t.Fatalf("err = %v, want ErrNoTerminalControl naming the Argus plugin", err)
	}
}

// A mod-connected session renames in-process through /rename, applied once the
// mod reports it ran.
func TestRenameSessionUsesTheModWhenReachable(t *testing.T) {
	a, _ := newWiredAdapter(t)
	sess := session.Session{ID: "x", AgentSessionID: "s1"}

	got := modPoll(a, context.Background(), "s1")
	waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })
	applied, err := a.RenameSession(context.Background(), sess, "New name")
	if err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if !applied {
		t.Error("applied = false, want true: the mod ran /rename")
	}
	cmds := <-got
	if len(cmds) != 1 || cmds[0].Kind != modbridge.KindPrompt || cmds[0].Text != "/rename New name" {
		t.Fatalf("poll got %v, want the /rename command", cmds)
	}
}

// Behind a running turn the mod only accepts /rename, so it is not applied yet.
func TestRenameSessionAcceptedIsNotApplied(t *testing.T) {
	a, _ := newWiredAdapter(t)
	b := a.ModBridge()
	go func() {
		for _, c := range b.Wait(context.Background(), "s1", time.Minute, nil) {
			b.Ack(c.ID, "", false, nil)
		}
	}()
	waitUntil(t, func() bool { return b.Reachable("s1") })
	applied, err := a.RenameSession(context.Background(), session.Session{ID: "x", AgentSessionID: "s1"}, "New name")
	if err != nil || applied {
		t.Fatalf("RenameSession = (%v, %v), want (false, nil)", applied, err)
	}
}

// A session with no agent session id says so, not that the plugin is missing.
func TestRenameSessionNeedsAnAgentSessionID(t *testing.T) {
	a, _ := newWiredAdapter(t)
	_, err := a.RenameSession(context.Background(), session.Session{ID: "x"}, "New name")
	if err == nil || !strings.Contains(err.Error(), "no agent session id") {
		t.Fatalf("err = %v, want the missing agent session id", err)
	}
}
