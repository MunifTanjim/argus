package opencode

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestRespondPostsPermission(t *testing.T) {
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

	reg := registry.New()
	d := &discoverer{reg: reg, pendPerm: map[string]string{"ses_1": "perm_9"}}
	c := newClient(serviceInfo{URL: srv.URL})
	d.dial = func() (*client, bool) { return c, true }
	activeDiscoverer = d

	err := ocAdapter{}.Respond(context.Background(), session.Session{AgentSessionID: "ses_1"}, api.RespondParams{Behavior: "deny", Reason: "no"})
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if gotPath != "/session/ses_1/permissions/perm_9" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody != `{"response":"reject"}` {
		t.Fatalf("body = %q", gotBody)
	}
}
