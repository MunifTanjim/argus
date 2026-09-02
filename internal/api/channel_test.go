package api

import (
	"encoding/json"
	"testing"
)

func TestPlainChannelRoundTrip(t *testing.T) {
	c := NewPlainChannel("c1")
	params := json.RawMessage(`{"session_id":"s1"}`)
	id := json.RawMessage(`1`)
	frame, err := c.SealRequestFrame(&id, "sessions.tasks", "node-a", params)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	f, err := ParseRelayFrame(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := c.OpenParams(f)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(got) != string(params) {
		t.Fatalf("round-trip mismatch: got %s want %s", got, params)
	}
}
