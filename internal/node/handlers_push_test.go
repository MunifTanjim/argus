package node

import (
	"context"
	"testing"

	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// fakeSink records notifications for assertions.
type fakeSink struct{ got []push.Notification }

func (f *fakeSink) Notify(_ context.Context, n push.Notification) { f.got = append(f.got, n) }

// desktopNodeWithSession builds an opted-in node holding one local session whose
// pane reports focused via the focusedFn seam, and routes rendering to sink.
func desktopNodeWithSession(t *testing.T, paneID string, focused bool, sink push.Sink) *Node {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{
		session.TmuxServerDefault: tmux.New("desktop-test"),
	})
	d.SetIdentity("nodeA", "nodeA")
	d.reg.ReconcileSessions("claude", []registry.DiscoveredSession{
		{HasPane: true, Server: session.TmuxServerDefault, PaneID: paneID, AgentSessionID: "abc", Frontend: session.FrontendTmux},
	})
	d.SetDesktopNotify(true, nil) // builds an OSNotifier; replaced below
	d.notifier = sink
	d.focusedFn = func(context.Context, *tmux.Client, string) (bool, error) { return focused, nil }
	return d
}

// TestRenderDesktopSuppressedWhenSessionFocused verifies that renderDesktop (via
// DesktopSink) suppresses a notification when the target session is already focused.
func TestRenderDesktopSuppressedWhenSessionFocused(t *testing.T) {
	paneID := "%7"
	sessID := "default:" + paneID // registry key assigned by ReconcileSessions
	sink := &fakeSink{}
	d := desktopNodeWithSession(t, paneID, true, sink)

	n := push.Notification{
		Title: "repo", Body: "Permission: Bash",
		Data: map[string]string{"session_id": sessID},
	}
	d.DesktopSink().Notify(context.Background(), n)
	if len(sink.got) != 0 {
		t.Fatalf("rendered %d notifications, want 0 (session already focused)", len(sink.got))
	}
}

// TestRenderDesktopRendersWhenSessionNotFocused verifies that renderDesktop renders
// when the session exists locally but is not currently focused.
func TestRenderDesktopRendersWhenSessionNotFocused(t *testing.T) {
	paneID := "%7"
	sessID := "default:" + paneID
	sink := &fakeSink{}
	d := desktopNodeWithSession(t, paneID, false, sink)

	n := push.Notification{
		Title: "repo", Body: "Permission: Bash",
		Data: map[string]string{"session_id": sessID},
	}
	d.DesktopSink().Notify(context.Background(), n)
	if len(sink.got) != 1 {
		t.Fatalf("rendered %d notifications, want 1 (session not focused)", len(sink.got))
	}
}

// TestRenderDesktopRendersForeignSession verifies that a notification for a session
// on another node always renders: it cannot be focused on this machine, so focus
// suppression must not apply.
func TestRenderDesktopRendersForeignSession(t *testing.T) {
	sink := &fakeSink{}
	d := desktopNodeWithSession(t, "%7", true, sink)

	n := push.Notification{
		Title: "repo", Body: "b",
		Data: map[string]string{"session_id": session.CompositeID("nodeB", "xyz")},
	}
	d.DesktopSink().Notify(context.Background(), n)
	if len(sink.got) != 1 {
		t.Fatalf("rendered %d notifications, want 1 (foreign session never focused here)", len(sink.got))
	}
}
