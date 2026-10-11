package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestSummaryForBuildsCard(t *testing.T) {
	s := ocSession{ID: "ses_1", Title: "Fix login\nsecond line"}
	s.Model.ID = "glm-5.3-flash"
	s.Tokens = ocTokens{Input: 100, Output: 20, Reasoning: 5}
	s.Time.Updated = 9
	sum := summaryFor(s)
	if sum == nil {
		t.Fatal("summaryFor returned nil")
	}
	if sum.Task != "Fix login" {
		t.Fatalf("Task = %q, want first line", sum.Task)
	}
	if sum.ModelName != "glm-5.3-flash" {
		t.Fatalf("ModelName = %q", sum.ModelName)
	}
	if sum.Tokens != 125 {
		t.Fatalf("Tokens = %d, want 125", sum.Tokens)
	}
	if sum.LastActivity == "" {
		t.Fatal("LastActivity empty")
	}
	if summaryFor(ocSession{}) != nil {
		t.Fatal("empty session should yield nil summary")
	}
}

func TestUpsertHydratesSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session/ses_1" {
			_, _ = w.Write([]byte(`{"data":{"id":"ses_1","title":"Do the thing","model":{"id":"glm"},"tokens":{"input":10,"output":5},"time":{"updated":9},"location":{"directory":"/repo"}}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	reg := registry.New()
	d := newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))

	d.upsert("ses_1", session.StatusWorking, nil)
	snap := reg.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	sum := snap[0].Summary
	if sum == nil || sum.Task != "Do the thing" || sum.ModelName != "glm" || sum.Tokens != 15 {
		t.Fatalf("summary not hydrated: %+v", sum)
	}
}

func TestSessionUpdatedRefreshesTitleWithoutStatusChange(t *testing.T) {
	reg := registry.New()
	d := newTestDiscoverer(reg, nil)
	seedSession(t, reg, "ses_1")

	d.applyEvent(sseFrame{Type: "session.execution.succeeded", Data: []byte(`{"sessionID":"ses_1"}`)})
	d.applyEvent(sseFrame{
		Type: "session.updated",
		Data: []byte(`{"info":{"id":"ses_1","title":"Renamed","model":{"id":"glm"},"tokens":{"input":3,"output":1},"time":{"updated":9},"location":{"directory":"/repo"}}}`),
	})

	s, ok := reg.Get(sessionKeyFor(reg, "ses_1"))
	if !ok {
		t.Fatal("session missing")
	}
	if s.Status != session.StatusAwaitingInput {
		t.Fatalf("status = %q, must stay awaiting_input", s.Status)
	}
	if s.Name != "Renamed" {
		t.Fatalf("Name = %q, want Renamed", s.Name)
	}
	if s.Summary == nil || s.Summary.Task != "Renamed" || s.Summary.ModelName != "glm" {
		t.Fatalf("summary not refreshed: %+v", s.Summary)
	}
}

func TestScanOnceRefreshesKnownTitles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{}}`))
		case "/api/session":
			_, _ = w.Write([]byte(`{"data":[{"id":"ses_1","title":"New title","model":{"id":"glm"},"tokens":{"input":7,"output":1},"time":{"updated":9},"location":{"directory":"/repo"}}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	reg := registry.New()
	d := newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))

	// Pre-tracked, so seedIdle skips it and only the refresh path can update it.
	d.mu.Lock()
	d.presence["ses_1"] = &presenceEntry{lastActivity: time.Now(), hydrated: true}
	d.mu.Unlock()
	reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: "ses_1", Status: session.StatusAwaitingInput, Name: "Old"})

	if err := d.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	s, ok := reg.Get(sessionKeyFor(reg, "ses_1"))
	if !ok {
		t.Fatal("session missing")
	}
	if s.Name != "New title" {
		t.Fatalf("Name = %q, want New title", s.Name)
	}
	if s.Summary == nil || s.Summary.Task != "New title" {
		t.Fatalf("summary not refreshed: %+v", s.Summary)
	}
}
