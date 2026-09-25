package client

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestWithOriginCompositesWorkspaceID(t *testing.T) {
	s := session.Session{ID: "default:%1", WorkspaceID: "abc123"}
	out := withOrigin(s, "home", "Home")

	if want := session.CompositeID("home", "abc123"); out.WorkspaceID != want {
		t.Errorf("WorkspaceID = %q, want %q", out.WorkspaceID, want)
	}
	if want := session.CompositeID("home", "default:%1"); out.ID != want {
		t.Errorf("ID = %q, want %q", out.ID, want)
	}
}

func TestWithOriginLeavesEmptyWorkspaceID(t *testing.T) {
	out := withOrigin(session.Session{ID: "x"}, "home", "Home")
	if out.WorkspaceID != "" {
		t.Errorf("WorkspaceID = %q, want empty", out.WorkspaceID)
	}
}
