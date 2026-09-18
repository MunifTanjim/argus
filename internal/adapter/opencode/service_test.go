package opencode

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientListSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		if u != "opencode" || p != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/session" {
			_, _ = w.Write([]byte(`[{"id":"ses_1","directory":"/repo","title":"t","time":{"created":1,"updated":2}}]`))
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
	if len(got) != 1 || got[0].ID != "ses_1" || got[0].Directory != "/repo" {
		t.Fatalf("unexpected sessions: %+v", got)
	}
}

func TestClientRespondPermission(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/session/ses_1/permissions/") {
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			_, _ = w.Write([]byte("true"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newClient(serviceInfo{URL: srv.URL, Password: ""})
	if err := c.respondPermission(context.Background(), "ses_1", "perm_9", "reject"); err != nil {
		t.Fatalf("respondPermission: %v", err)
	}
	if gotBody != `{"response":"reject"}` {
		t.Fatalf("body = %s", gotBody)
	}
}
