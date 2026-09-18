package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestScanOnceReconcilesSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session" {
			_, _ = w.Write([]byte(`{"data":[{"id":"ses_1","projectID":"proj","agent":"build","title":"t","time":{"created":1,"updated":2},"location":{"directory":"/repo/foo"}}],"cursor":{"previous":"","next":""}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	reg := registry.New()
	d := &discoverer{reg: reg}
	c := newClient(serviceInfo{URL: srv.URL})
	d.dial = func() (*client, bool) { return c, true }
	d.panes = func(context.Context) map[string]paneInfo {
		return map[string]paneInfo{
			"/repo/foo": {
				server:      session.TmuxServerDefault,
				paneID:      "pane_1",
				sessionName: "main",
				windowIndex: 0,
				currentPath: "/repo/foo",
			},
		}
	}

	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	sessions := reg.Snapshot()
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.Agent != "opencode" || s.AgentSessionID != "ses_1" {
		t.Fatalf("bad session: %+v", s)
	}
	if s.Cwd != "/repo/foo" || s.TranscriptPath != "ses_1" {
		t.Fatalf("cwd/transcriptPath: %q %q", s.Cwd, s.TranscriptPath)
	}
	if s.Frontend != session.FrontendTmux {
		t.Fatalf("frontend = %q, want FrontendTmux", s.Frontend)
	}
	if s.Tmux.PaneID != "pane_1" {
		t.Fatalf("Tmux.PaneID = %q, want pane_1", s.Tmux.PaneID)
	}
}

func TestScanOnceSkipsSessionsWithoutPane(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session" {
			_, _ = w.Write([]byte(`{"data":[{"id":"ses_1","projectID":"proj","agent":"build","title":"t","time":{"created":1,"updated":2},"location":{"directory":"/repo/foo"}}],"cursor":{"previous":"","next":""}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	reg := registry.New()
	d := &discoverer{reg: reg}
	c := newClient(serviceInfo{URL: srv.URL})
	d.dial = func() (*client, bool) { return c, true }
	d.panes = func(context.Context) map[string]paneInfo {
		return map[string]paneInfo{}
	}

	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if got := len(reg.Snapshot()); got != 0 {
		t.Fatalf("want 0 sessions (no pane), got %d", got)
	}
}

func TestScanOnceServiceDown(t *testing.T) {
	reg := registry.New()
	d := &discoverer{reg: reg}
	d.dial = func() (*client, bool) { return nil, false }
	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if len(reg.Snapshot()) != 0 {
		t.Fatal("expected no sessions when service is down")
	}
}
