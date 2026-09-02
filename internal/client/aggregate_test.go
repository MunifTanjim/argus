package client

import (
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestWithOriginStampsCompositeAndNode(t *testing.T) {
	s := session.Session{ID: "local1", Offline: true}
	got := withOrigin(s, "n1", "n1-box")
	if got.ID != session.CompositeID("n1", "local1") {
		t.Errorf("ID = %q, want composite", got.ID)
	}
	if got.NodeID != "n1" || got.NodeLabel != "n1-box" {
		t.Errorf("node stamp = %q/%q", got.NodeID, got.NodeLabel)
	}
	if got.Offline {
		t.Error("Offline should be cleared (session is currently reported)")
	}
}

func TestRewriteSessionIDReplacesOnlyThatField(t *testing.T) {
	in := json.RawMessage(`{"session_id":"n1:local1","text":"hi","submit":true}`)
	out, err := rewriteSessionID(in, "local1")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["session_id"] != "local1" {
		t.Errorf("session_id = %v, want local1", m["session_id"])
	}
	if m["text"] != "hi" || m["submit"] != true {
		t.Errorf("other fields not preserved: %v", m)
	}
}

func TestParamFieldExtractors(t *testing.T) {
	p := json.RawMessage(`{"session_id":"n1:s","node_id":"n2","sub_id":"sub-9","term_id":"t-3"}`)
	if s, _ := sessionIDFromParams(p); s != "n1:s" {
		t.Errorf("session_id = %q", s)
	}
	if s, _ := nodeIDFromParams(p); s != "n2" {
		t.Errorf("node_id = %q", s)
	}
	if s, _ := subIDFromParams(p); s != "sub-9" {
		t.Errorf("sub_id = %q", s)
	}
	if s, _ := termIDFromParams(p); s != "t-3" {
		t.Errorf("term_id = %q", s)
	}
}

func TestMethodSetsMirrorGateway(t *testing.T) {
	if !sessionAddressed[api.MethodSessionInput] || !sessionAddressed[api.MethodTranscriptSubscribe] ||
		!sessionAddressed[api.MethodTerminalOpen] || !sessionAddressed[api.MethodSessionFocus] {
		t.Error("sessionAddressed missing an expected method")
	}
	if !nodeAddressed[api.MethodSessionSpawn] || !nodeAddressed[api.MethodSessionsHistorySessions] {
		t.Error("nodeAddressed missing an expected method")
	}
	if !terminalHandleAddressed[api.MethodTerminalInput] || !terminalHandleAddressed[api.MethodTerminalClose] {
		t.Error("terminalHandleAddressed missing an expected method")
	}
	if !compositeResultMethods[api.MethodSessionSpawn] || !compositeResultMethods[api.MethodSessionResume] {
		t.Error("compositeResultMethods missing spawn/resume")
	}
}
