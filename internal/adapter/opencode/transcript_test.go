package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestStreamingTranscriptRefreshDetectsInMessageUpdate(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/ses_1/message" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if callCount.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"data":[` +
				`{"type":"user","id":"m1","time":{"created":1},"text":"run it"},` +
				`{"type":"assistant","id":"m2","time":{"created":2},"content":[` +
				`{"type":"tool","id":"t1","name":"bash","state":{"status":"running","input":{"command":"ls"},"content":[]}}` +
				`]}],"cursor":{"previous":"","next":""}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":[` +
				`{"type":"user","id":"m1","time":{"created":1},"text":"run it"},` +
				`{"type":"assistant","id":"m2","time":{"created":2},"content":[` +
				`{"type":"tool","id":"t1","name":"bash","state":{"status":"completed","input":{"command":"ls"},"content":[{"type":"text","text":"file.go"}]}}` +
				`]}],"cursor":{"previous":"","next":""}}`))
		}
	}))
	defer srv.Close()

	c := newClient(serviceInfo{URL: srv.URL})
	st := &streamingTranscript{
		sessionID: "ses_1",
		sig:       -1,
		readView: func(id string) (transcript.TranscriptView, error) {
			msgs, err := c.readMessages(context.Background(), id)
			if err != nil {
				return transcript.TranscriptView{}, err
			}
			return foldMessages(msgs), nil
		},
	}

	chunks1, err := st.Refresh()
	if err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if len(chunks1) != 2 || len(chunks1[1].Items) != 1 {
		t.Fatalf("first Refresh: want 2 chunks with 1 tool item, got %+v", chunks1)
	}
	if chunks1[1].Items[0].Result != "" {
		t.Fatalf("first Refresh: expected empty tool result, got %q", chunks1[1].Items[0].Result)
	}

	chunks2, err := st.Refresh()
	if err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if len(chunks2) != 2 || len(chunks2[1].Items) != 1 {
		t.Fatalf("second Refresh: want 2 chunks with 1 tool item, got %+v", chunks2)
	}
	if chunks2[1].Items[0].Result != "file.go" {
		t.Fatalf("second Refresh: expected tool result %q, got %q", "file.go", chunks2[1].Items[0].Result)
	}
}

func TestFindToolDetailReadsChildSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/ses_parent/message":
			_, _ = w.Write([]byte(`{"data":[` +
				`{"type":"assistant","id":"m1","time":{"created":1},"content":[` +
				`{"type":"tool","id":"sub_1","name":"subagent","state":{"status":"completed","input":{"agent":"explore"},"metadata":{"sessionID":"ses_child"}}}` +
				`]}],"cursor":{"previous":"","next":""}}`))
		case "/api/session/ses_child/message":
			_, _ = w.Write([]byte(`{"data":[` +
				`{"type":"assistant","id":"cm1","time":{"created":1},"content":[` +
				`{"type":"tool","id":"w_1","name":"write","state":{"status":"completed","input":{"filePath":"/tmp/x"},"content":[{"type":"text","text":"Wrote file successfully."}]}}` +
				`]}],"cursor":{"previous":"","next":""}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	serviceDial = func() (*client, bool) { return newClient(serviceInfo{URL: srv.URL}), true }
	t.Cleanup(func() { serviceDial = nil })

	// The child's tool id lives in the child session; the drill-in passes agentID.
	td, found, err := findToolDetail("ses_parent", "ses_child", "w_1")
	if err != nil || !found {
		t.Fatalf("findToolDetail: found=%v err=%v", found, err)
	}
	if td.Result != "Wrote file successfully." || td.ToolInput != `{"filePath":"/tmp/x"}` {
		t.Fatalf("child tool detail = %+v", td)
	}

	// Without the child agentID it reads the parent, which has no such tool id.
	if _, found, _ := findToolDetail("ses_parent", "", "w_1"); found {
		t.Fatal("child tool id must not resolve against the parent transcript")
	}
}
