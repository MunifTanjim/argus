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
