package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

// TestBuildRequestSetsDeliveryHeaders ensures every POST carries TTL and a high
// Urgency, so the embedded-FCM proxy sends high-priority FCM that Doze won't batch.
func TestBuildRequestSetsDeliveryHeaders(t *testing.T) {
	u := NewUnifiedPushSender(nil)
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatalf("auth secret: %v", err)
	}
	cases := []struct {
		name   string
		target Target
	}{
		{"plain endpoint", Target{Endpoint: "https://push.example/x"}},
		{"web push", Target{
			Endpoint: "https://push.example/x",
			P256dh:   b64.EncodeToString(key.PublicKey().Bytes()),
			Auth:     b64.EncodeToString(authSecret),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := u.buildRequest(context.Background(), tc.target, []byte(`{"id":"x"}`))
			if err != nil {
				t.Fatalf("buildRequest: %v", err)
			}
			if got := req.Header.Get("TTL"); got != unifiedPushTTL {
				t.Errorf("TTL = %q, want %q", got, unifiedPushTTL)
			}
			if got := req.Header.Get("Urgency"); got != unifiedPushUrgency {
				t.Errorf("Urgency = %q, want %q", got, unifiedPushUrgency)
			}
		})
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

func TestPostEncryptedSetsHeadersAndPostsBody(t *testing.T) {
	var gotBody []byte
	var gotCE, gotTTL, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotCE = r.Header.Get("Content-Encoding")
		gotTTL = r.Header.Get("TTL")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	v, err := LoadOrCreateVAPID(filepath.Join(t.TempDir(), "vapid.pem"))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("opaque-ciphertext")
	if err := PostEncrypted(context.Background(), srv.Client(), v, srv.URL, body, "", ""); err != nil {
		t.Fatalf("PostEncrypted: %v", err)
	}
	if string(gotBody) != "opaque-ciphertext" {
		t.Errorf("body = %q, want opaque-ciphertext", gotBody)
	}
	if gotCE != "aes128gcm" {
		t.Errorf("Content-Encoding = %q", gotCE)
	}
	if gotTTL != unifiedPushTTL {
		t.Errorf("TTL = %q", gotTTL)
	}
	if !strings.HasPrefix(gotAuth, "vapid t=") {
		t.Errorf("Authorization = %q, want vapid header", gotAuth)
	}
}

func TestPostEncryptedGoneOn410(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()
	err := PostEncrypted(context.Background(), srv.Client(), nil, srv.URL, []byte("x"), "", "")
	if !errors.Is(err, ErrGone) {
		t.Fatalf("err = %v, want ErrGone", err)
	}
}
