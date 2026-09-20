package opencode

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListHistoryProjectsGroupsByDirectory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session" {
			_, _ = w.Write([]byte(`{"data":[
				{"id":"a","projectID":"p","title":"t1","time":{"created":1,"updated":9},"location":{"directory":"/repo/x"}},
				{"id":"b","projectID":"p","title":"t2","time":{"created":2,"updated":8},"location":{"directory":"/repo/x"}},
				{"id":"c","projectID":"p","title":"t3","time":{"created":3,"updated":7},"location":{"directory":"/repo/y"}}
			],"cursor":{"previous":"","next":""}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	historyDial = func() (*client, bool) { return newClient(serviceInfo{URL: srv.URL}), true }
	t.Cleanup(func() { historyDial = nil })

	projects, err := listHistoryProjects()
	if err != nil {
		t.Fatalf("listHistoryProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(projects))
	}
}

func TestListHistorySessionsReturnsAllWhenLimitZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session" {
			_, _ = w.Write([]byte(`{"data":[
				{"id":"a","projectID":"p","title":"t1","model":{"id":"glm-5.3-flash"},"tokens":{"input":100,"output":20,"reasoning":5,"cache":{"read":9999,"write":1}},"time":{"created":1,"updated":9},"location":{"directory":"/repo/x"}},
				{"id":"b","projectID":"p","title":"t2","time":{"created":2,"updated":8},"location":{"directory":"/repo/x"}},
				{"id":"c","projectID":"p","title":"t3","time":{"created":3,"updated":7},"location":{"directory":"/repo/y"}}
			],"cursor":{"previous":"","next":""}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	historyDial = func() (*client, bool) { return newClient(serviceInfo{URL: srv.URL}), true }
	t.Cleanup(func() { historyDial = nil })

	// The node calls adapters with limit 0 to mean "all"; it paginates the merge.
	page, err := listHistorySessions("/repo/x", 0, 0)
	if err != nil {
		t.Fatalf("listHistorySessions: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("want 2 sessions for /repo/x, got %d", len(page.Items))
	}
	if page.HasMore {
		t.Errorf("HasMore = true, want false")
	}
	if page.Items[0].SessionID != "a" {
		t.Errorf("newest-first order broken: first = %q, want a", page.Items[0].SessionID)
	}
	if page.Items[0].ModelName != "glm-5.3-flash" {
		t.Errorf("ModelName = %q, want glm-5.3-flash", page.Items[0].ModelName)
	}
	if page.Items[0].Tokens != 125 {
		t.Errorf("Tokens = %d, want 125 (input+output+reasoning, cache excluded)", page.Items[0].Tokens)
	}
}
