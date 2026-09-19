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

func TestRespondPostsFormReply(t *testing.T) {
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
	d := &discoverer{
		reg:      reg,
		pendPerm: map[string]string{},
		pendForm: map[string]*pendingForm{"ses_1": {
			formID: "frm_1",
			fields: []pendingField{{key: "q0", question: "Q?", valueByLabel: map[string]string{"Yes": "y"}}},
		}},
	}
	c := newClient(serviceInfo{URL: srv.URL})
	d.dial = func() (*client, bool) { return c, true }
	a := &ocAdapter{disc: d}

	err := a.Respond(context.Background(), session.Session{AgentSessionID: "ses_1"}, api.RespondParams{Answers: map[string]any{"Q?": "Yes"}})
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if gotPath != "/api/session/ses_1/form/frm_1/reply" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody != `{"answer":{"q0":"y"}}` {
		t.Fatalf("body = %q", gotBody)
	}
	d.mu.Lock()
	_, still := d.pendForm["ses_1"]
	d.mu.Unlock()
	if still {
		t.Fatal("form reply should clear pendForm")
	}
}

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
	a := &ocAdapter{disc: d}

	err := a.Respond(context.Background(), session.Session{AgentSessionID: "ses_1"}, api.RespondParams{Behavior: "deny", Reason: "no"})
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if gotPath != "/api/session/ses_1/permission/perm_9/reply" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody != `{"decision":"reject","message":"no"}` {
		t.Fatalf("body = %q", gotBody)
	}
}
