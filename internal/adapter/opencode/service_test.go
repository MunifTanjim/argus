package opencode

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientListSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		if u != "opencode" || p != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/session" {
			_, _ = w.Write([]byte(`{"data":[{"id":"ses_1","projectID":"proj","agent":"build","title":"t","time":{"created":1,"updated":2},"location":{"directory":"/repo"}}],"cursor":{"previous":"","next":""}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newClient(serviceInfo{URL: srv.URL, Password: "secret"})
	got, err := c.listSessions(context.Background())
	if err != nil {
		t.Fatalf("listSessions: %v", err)
	}
	if len(got) != 1 || got[0].ID != "ses_1" || got[0].Location.Directory != "/repo" {
		t.Fatalf("unexpected sessions: %+v", got)
	}
}

func TestClientRespondPermission(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			gotPath = r.URL.Path
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			_, _ = w.Write([]byte("true"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newClient(serviceInfo{URL: srv.URL, Password: ""})
	if err := c.respondPermission(context.Background(), "ses_1", "perm_9", "reject", "no"); err != nil {
		t.Fatalf("respondPermission: %v", err)
	}
	if gotPath != "/api/session/ses_1/permission/perm_9/reply" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotBody != `{"decision":"reject","message":"no"}` {
		t.Fatalf("body = %s", gotBody)
	}
}
