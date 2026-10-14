package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// renameSession PATCHes the service's session.update route with the new title.
func TestRenameSessionUpdatesTitle(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := newTestDiscoverer(registry.New(), newClient(serviceInfo{URL: srv.URL}))
	if err := d.renameSession(context.Background(), "ses_1", "New name"); err != nil {
		t.Fatalf("renameSession: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/session/ses_1" {
		t.Errorf("request = %s %s, want PATCH /api/session/ses_1", gotMethod, gotPath)
	}
	if gotBody["title"] != "New name" {
		t.Errorf("body = %v, want the new title", gotBody)
	}
}

func TestRenameSessionSurfacesServiceErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	d := newTestDiscoverer(registry.New(), newClient(serviceInfo{URL: srv.URL}))
	if err := d.renameSession(context.Background(), "missing", "x"); err == nil {
		t.Error("want an error for a failed title update")
	}

	d = newTestDiscoverer(registry.New(), nil)
	if err := d.renameSession(context.Background(), "ses_1", "x"); err == nil {
		t.Error("want an error when the service is unavailable")
	}
}

func TestAdapterRenameSessionNeedsAnAgentSessionID(t *testing.T) {
	a := &ocAdapter{}
	if _, err := a.RenameSession(context.Background(), session.Session{}, "x"); err == nil {
		t.Error("want an error without a discoverer")
	}
	a.disc = newTestDiscoverer(registry.New(), nil)
	if _, err := a.RenameSession(context.Background(), session.Session{AgentSessionID: ""}, "x"); err == nil {
		t.Error("want an error without an agent session id")
	}
}
