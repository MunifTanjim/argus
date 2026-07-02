package node

import (
	"context"
	"encoding/json"
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

func TestPushDesktopRendersWhenEnabled(t *testing.T) {
	d := newNode(nil)
	d.SetDesktopNotify(true, nil)
	sink := &fakeSink{}
	d.notifier = sink

	params, _ := json.Marshal(push.Notification{Title: "repo", Body: "Permission: Bash"})
	if _, err := d.handlePushDesktop(context.Background(), params); err != nil {
		t.Fatalf("handlePushDesktop: %v", err)
	}
	if len(sink.got) != 1 || sink.got[0].Title != "repo" || sink.got[0].Body != "Permission: Bash" {
		t.Fatalf("rendered = %+v, want one notification with title=repo and body=Permission: Bash", sink.got)
	}
}

func TestPushDesktopNoopWhenDisabled(t *testing.T) {
	d := newNode(nil)
	sink := &fakeSink{}
	d.notifier = sink
	// SetDesktopNotify not called -> disabled by default.

	params, _ := json.Marshal(push.Notification{Title: "t", Body: "b"})
	if _, err := d.handlePushDesktop(context.Background(), params); err != nil {
		t.Fatalf("handlePushDesktop: %v", err)
	}
	if len(sink.got) != 0 {
		t.Fatalf("rendered %d notifications, want 0 (opt-in off)", len(sink.got))
	}
}

func TestPushDesktopSuppressedWhenSessionFocused(t *testing.T) {
	paneID := "%7"
	sessID := "default:" + paneID // registry key assigned by ReconcileSessions
	sink := &fakeSink{}
	d := desktopNodeWithSession(t, paneID, true, sink)

	params, _ := json.Marshal(push.Notification{
		Title: "repo", Body: "Permission: Bash",
		Data: map[string]string{"session_id": sessID},
	})
	if _, err := d.handlePushDesktop(context.Background(), params); err != nil {
		t.Fatalf("handlePushDesktop: %v", err)
	}
	if len(sink.got) != 0 {
		t.Fatalf("rendered %d notifications, want 0 (session already focused)", len(sink.got))
	}
}

func TestPushDesktopRendersWhenSessionNotFocused(t *testing.T) {
	paneID := "%7"
	sessID := "default:" + paneID
	sink := &fakeSink{}
	d := desktopNodeWithSession(t, paneID, false, sink)

	params, _ := json.Marshal(push.Notification{
		Title: "repo", Body: "Permission: Bash",
		Data: map[string]string{"session_id": sessID},
	})
	if _, err := d.handlePushDesktop(context.Background(), params); err != nil {
		t.Fatalf("handlePushDesktop: %v", err)
	}
	if len(sink.got) != 1 {
		t.Fatalf("rendered %d notifications, want 1 (session not focused)", len(sink.got))
	}
}

// A broadcast for a session this node doesn't own can't be focused here, so it
// renders even though focusedFn would say "focused" for a local pane.
func TestPushDesktopRendersForeignSession(t *testing.T) {
	sink := &fakeSink{}
	d := desktopNodeWithSession(t, "%7", true, sink)

	params, _ := json.Marshal(push.Notification{
		Title: "repo", Body: "b",
		Data: map[string]string{"session_id": session.CompositeID("nodeB", "xyz")},
	})
	if _, err := d.handlePushDesktop(context.Background(), params); err != nil {
		t.Fatalf("handlePushDesktop: %v", err)
	}
	if len(sink.got) != 1 {
		t.Fatalf("rendered %d notifications, want 1 (foreign session never focused here)", len(sink.got))
	}
}
