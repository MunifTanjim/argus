package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// Route strips the node prefix from a composite session id and forwards the call
// to the owning node with the node-local id, preserving other params.
func TestRouteRewritesCompositeSessionID(t *testing.T) {
	a := New(time.Second)
	src := newFakeSource("home", "home-box")
	a.AddSource(src)

	composite := session.CompositeID("home", "default:%1")
	params, _ := json.Marshal(map[string]any{"session_id": composite, "text": "hi"})
	if _, err := a.Route(context.Background(), api.MethodSessionInput, params); err != nil {
		t.Fatal(err)
	}

	rec, ok := src.lastCall()
	if !ok {
		t.Fatal("call was not forwarded to the source")
	}
	if rec.method != api.MethodSessionInput {
		t.Errorf("method = %q, want %q", rec.method, api.MethodSessionInput)
	}
	var got struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(rec.params, &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "default:%1" {
		t.Errorf("session_id = %q, want node-local %q", got.SessionID, "default:%1")
	}
	if got.Text != "hi" {
		t.Errorf("other params must be preserved, text = %q", got.Text)
	}
}

// sessions.toolDetail must be session-routed (by composite id) like the other
// per-session control calls; a regression here would 404 the on-demand fetch.
func TestToolDetailIsClientRouted(t *testing.T) {
	for _, m := range clientRoutedMethods {
		if m == api.MethodSessionToolDetail {
			return
		}
	}
	t.Errorf("%s not in clientRoutedMethods", api.MethodSessionToolDetail)
}

// A session-routed toolDetail call reaches the owning node with the node-local id
// and its agent_id/tool_id preserved.
func TestRouteToolDetailPreservesParams(t *testing.T) {
	a := New(time.Second)
	src := newFakeSource("home", "home-box")
	a.AddSource(src)

	composite := session.CompositeID("home", "default:%1")
	params, _ := json.Marshal(api.ToolDetailParams{SessionID: composite, AgentID: "abc123", ToolID: "T1"})
	if _, err := a.Route(context.Background(), api.MethodSessionToolDetail, params); err != nil {
		t.Fatal(err)
	}
	rec, ok := src.lastCall()
	if !ok {
		t.Fatal("call was not forwarded")
	}
	var got api.ToolDetailParams
	if err := json.Unmarshal(rec.params, &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "default:%1" || got.AgentID != "abc123" || got.ToolID != "T1" {
		t.Errorf("forwarded params = %+v", got)
	}
}

// RouteToNode forwards by node id and re-composites the session id in the result
// so the client can address the newly created session.
func TestRouteToNodeRecomposesResultID(t *testing.T) {
	a := New(time.Second)
	src := newFakeSource("home", "home-box")
	src.callResp, _ = json.Marshal(map[string]any{"session_id": "argus:%5", "pane_id": "%5"})
	a.AddSource(src)

	res, err := a.RouteToNode(context.Background(), "home", api.MethodSessionSpawn,
		json.RawMessage(`{"node_id":"home","name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatal(err)
	}
	if want := session.CompositeID("home", "argus:%5"); got.SessionID != want {
		t.Errorf("result session_id = %q, want %q", got.SessionID, want)
	}
}
