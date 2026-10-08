package opencode

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
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

func TestClientListSessionsFollowsCursor(t *testing.T) {
	var gotQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"data":[{"id":"ses_1","location":{"directory":"/a"}}],"cursor":{"previous":null,"next":"C2"}}`))
		case "C2":
			_, _ = w.Write([]byte(`{"data":[{"id":"ses_2","location":{"directory":"/b"}}],"cursor":{"previous":"C1","next":null}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := newClient(serviceInfo{URL: srv.URL})
	got, err := c.listSessions(context.Background())
	if err != nil {
		t.Fatalf("listSessions: %v", err)
	}
	if len(got) != 2 || got[0].ID != "ses_1" || got[1].ID != "ses_2" {
		t.Fatalf("want both pages [ses_1 ses_2], got %+v", got)
	}
	if len(gotQueries) != 2 || gotQueries[0] != "" || gotQueries[1] != "cursor=C2" {
		t.Fatalf("queries = %v", gotQueries)
	}
}

func TestReadMessagesPaginatesAscending(t *testing.T) {
	var gotQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/ses_1/message" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"data":[
				{"type":"user","id":"m1","time":{"created":1},"text":"first"},
				{"type":"assistant","id":"m2","time":{"created":2},"content":[{"type":"text","id":"p1","text":"a"}]}
			],"cursor":{"previous":null,"next":"C2"}}`))
		case "C2":
			_, _ = w.Write([]byte(`{"data":[
				{"type":"user","id":"m3","time":{"created":3},"text":"third"},
				{"type":"assistant","id":"m4","time":{"created":4},"content":[{"type":"text","id":"p2","text":"b"}]}
			],"cursor":{"previous":"C1","next":null}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := newClient(serviceInfo{URL: srv.URL})
	msgs, err := c.readMessages(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("readMessages: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages across both pages, got %d", len(msgs))
	}

	es := foldMessages(msgs, false)
	// user, text, footer (closed by the next user), user, text.
	wantIDs := []string{"m1", "p1", "m2.end", "m3", "p2"}
	if len(es) != len(wantIDs) {
		t.Fatalf("want %d entries, got %d: %+v", len(wantIDs), len(es), es)
	}
	for i, want := range wantIDs {
		if es[i].ID != want {
			t.Fatalf("entry %d = %q, want %q (chronological order)", i, es[i].ID, want)
		}
	}
	if es[0].Kind != transcript.EntryUser || es[1].Kind != transcript.EntryText || es[2].Kind != transcript.EntryTurnEnd {
		t.Fatalf("unexpected entry kinds: %+v", es)
	}
	if len(gotQueries) != 2 || gotQueries[0] != "order=asc" || gotQueries[1] != "cursor=C2" {
		t.Fatalf("queries = %v (want oldest-first first page, cursor-only second)", gotQueries)
	}
}

func TestClientListActive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session/active" {
			_, _ = w.Write([]byte(`{"data":{"ses_1":{"type":"running"},"ses_2":{"type":"running"}}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newClient(serviceInfo{URL: srv.URL})
	got, err := c.listActive(context.Background())
	if err != nil || !got["ses_1"] || !got["ses_2"] || len(got) != 2 {
		t.Fatalf("listActive: %v %v", got, err)
	}
}

func TestClientGetSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session/ses_1" {
			_, _ = w.Write([]byte(`{"data":{"id":"ses_1","title":"T","location":{"directory":"/repo"},"time":{"updated":9}}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newClient(serviceInfo{URL: srv.URL})
	s, err := c.getSession(context.Background(), "ses_1")
	if err != nil || s.ID != "ses_1" || s.Title != "T" || s.Location.Directory != "/repo" {
		t.Fatalf("getSession: %+v %v", s, err)
	}
}

func TestClientRespondPermissionAllowOmitsMessage(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			_, _ = w.Write([]byte("true"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newClient(serviceInfo{URL: srv.URL})
	if err := c.respondPermission(context.Background(), "ses_1", "perm_9", "once", ""); err != nil {
		t.Fatalf("respondPermission: %v", err)
	}
	if gotBody != `{"decision":"once"}` {
		t.Fatalf("allow body = %s, want no message key", gotBody)
	}
}

func TestClientReplyForm(t *testing.T) {
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
	c := newClient(serviceInfo{URL: srv.URL})
	if err := c.replyForm(context.Background(), "ses_1", "frm_1", map[string]any{"q0": "v0"}); err != nil {
		t.Fatalf("replyForm: %v", err)
	}
	if gotPath != "/api/session/ses_1/form/frm_1/reply" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotBody != `{"answer":{"q0":"v0"}}` {
		t.Fatalf("body = %s", gotBody)
	}
}

func TestClientSendPrompt(t *testing.T) {
	var gotPath, gotBody, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			gotMethod = r.Method
			gotPath = r.URL.Path
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			_, _ = w.Write([]byte(`{"data":{}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newClient(serviceInfo{URL: srv.URL})
	if err := c.sendPrompt(context.Background(), "ses_1", "hello"); err != nil {
		t.Fatalf("sendPrompt: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/session/ses_1/prompt" {
		t.Fatalf("method=%s path=%s", gotMethod, gotPath)
	}
	if gotBody != `{"text":"hello"}` {
		t.Fatalf("body = %s", gotBody)
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
