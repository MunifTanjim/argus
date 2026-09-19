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
