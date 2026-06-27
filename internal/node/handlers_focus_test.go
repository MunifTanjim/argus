package node

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// focusTestNode builds a node with one local session and a recording revealFn.
func focusTestNode(t *testing.T, sessID, paneID string) (*Node, *string) {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{
		session.TmuxServerDefault: tmux.New("focus-test"),
	})
	d.SetIdentity("nodeA", "nodeA")
	d.reg.ReconcileDiscovered("claude-code", session.TmuxServerDefault, []registry.DiscoveredPane{
		{Tool: "claude-code", Server: session.TmuxServerDefault, PaneID: paneID, ClaudeSessionID: sessID},
	})
	// ReconcileDiscovered sets ID = "default:<paneID>".
	revealed := new(string)
	d.revealFn = func(_ context.Context, _ *tmux.Client, pane string) error {
		*revealed = pane
		return nil
	}
	return d, revealed
}

func TestFocusRevealsLocalSession(t *testing.T) {
	paneID := "%7"
	sessID := "default:" + paneID // registry key assigned by ReconcileDiscovered
	d, revealed := focusTestNode(t, "abc", paneID)
	params, _ := json.Marshal(api.SessionRef{SessionID: sessID})
	if _, err := d.handleSessionFocus(context.Background(), params); err != nil {
		t.Fatalf("focus: %v", err)
	}
	if *revealed != paneID {
		t.Fatalf("revealed pane = %q, want %q", *revealed, paneID)
	}
}

func TestFocusStripsOwnNodePrefix(t *testing.T) {
	paneID := "%7"
	sessID := "default:" + paneID // registry key assigned by ReconcileDiscovered
	d, revealed := focusTestNode(t, "abc", paneID)
	// Gateway-broadcast notifications carry the composite id "nodeA:<sessID>".
	params, _ := json.Marshal(api.SessionRef{SessionID: session.CompositeID("nodeA", sessID)})
	if _, err := d.handleSessionFocus(context.Background(), params); err != nil {
		t.Fatalf("focus: %v", err)
	}
	if *revealed != paneID {
		t.Fatalf("revealed pane = %q, want %q (own-prefix should be stripped)", *revealed, paneID)
	}
}

func TestFocusForeignSessionErrors(t *testing.T) {
	paneID := "%7"
	d, revealed := focusTestNode(t, "abc", paneID)
	// A session owned by another node: not local here -> error, no reveal.
	params, _ := json.Marshal(api.SessionRef{SessionID: session.CompositeID("nodeB", "xyz")})
	if _, err := d.handleSessionFocus(context.Background(), params); err == nil {
		t.Fatal("expected error for foreign session, got nil")
	}
	if *revealed != "" {
		t.Fatalf("revealed %q, want no reveal for foreign session", *revealed)
	}
}

func TestFocusBareIDNotFound(t *testing.T) {
	d, revealed := focusTestNode(t, "abc", "%7")
	// A bare id with no colon: SplitCompositeID returns ok=false, id used as-is;
	// it isn't a registry key, so resolve errors and nothing is revealed.
	params, _ := json.Marshal(api.SessionRef{SessionID: "nocolon"})
	if _, err := d.handleSessionFocus(context.Background(), params); err == nil {
		t.Fatal("expected error for unknown bare id, got nil")
	}
	if *revealed != "" {
		t.Fatalf("revealed %q, want no reveal for unknown bare id", *revealed)
	}
}
