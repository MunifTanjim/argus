package push

import (
	"encoding/json"
	"testing"
)

// TestEncodePayloadStampsID ensures every delivered payload carries the given id
// alongside the user-facing fields, so the client can dedup replayed deliveries.
func TestEncodePayloadStampsID(t *testing.T) {
	n := Notification{Title: "argus", Body: "hi", Data: map[string]string{"session_id": "s1"}}
	body, err := encodePayload(n, "abc123")
	if err != nil {
		t.Fatalf("encodePayload: %v", err)
	}
	var got struct {
		ID    string            `json:"id"`
		Title string            `json:"title"`
		Body  string            `json:"body"`
		Data  map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != "abc123" {
		t.Errorf("id = %q, want abc123", got.ID)
	}
	if got.Title != "argus" || got.Body != "hi" || got.Data["session_id"] != "s1" {
		t.Errorf("payload fields lost: %+v", got)
	}
}

// TestMessageIDUnique ensures ids are non-empty and differ across calls, so a
// genuine resend is never mistaken for a replay.
func TestMessageIDUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := messageID()
		if id == "" {
			t.Fatal("empty message id")
		}
		if seen[id] {
			t.Fatalf("duplicate message id: %q", id)
		}
		seen[id] = true
	}
}
