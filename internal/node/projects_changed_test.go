package node

import (
	"context"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// changedCount waits briefly for n's project.changed notifications, which are
// sent in the background, and counts them.
func changedCount(n *recordingNotifier) int {
	c := 0
	quiet := 2 * time.Second
	for {
		select {
		case note := <-n.other:
			if note.Method == api.MethodProjectChanged {
				c++
				quiet = 100 * time.Millisecond
			}
		case <-time.After(quiet):
			return c
		}
	}
}

func TestProjectChangedReachesConnections(t *testing.T) {
	d := newTestNode(t)
	n := newRecordingNotifier()
	d.registerConn(n)
	d.notifyProjectsChanged()
	if got := changedCount(n); got != 1 {
		t.Errorf("project.changed sent %d times, want 1", got)
	}
}

func TestAdoptOfANewWorkspaceNotifies(t *testing.T) {
	ctx := context.Background()
	d := nodeWithRegistry(t)
	n := newRecordingNotifier()
	d.registerConn(n)
	d.reg.ReconcileSessions("claude", []registry.DiscoveredSession{{AgentSessionID: "s1", Cwd: t.TempDir(), Frontend: session.FrontendTmux}})
	for _, s := range d.reg.Snapshot() {
		d.adoptSessionWorkspace(ctx, s)
	}
	if got := changedCount(n); got != 1 {
		t.Errorf("a new workspace should send project.changed once, got %d", got)
	}
}

type stalledNotifier struct{ release chan struct{} }

func (s stalledNotifier) Notify(string, any) error {
	<-s.release
	return nil
}

func TestProjectChangedDoesNotWaitForAStalledConnection(t *testing.T) {
	d := newTestNode(t)
	stalled := stalledNotifier{release: make(chan struct{})}
	defer close(stalled.release)
	d.registerConn(stalled)
	done := make(chan struct{})
	go func() {
		d.notifyProjectsChanged()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notifyProjectsChanged should not wait for a stalled connection")
	}
}
