package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"testing"
)

func TestRelaySenderEncryptsAndDeliversOpaqueBody(t *testing.T) {
	// Generate a fresh UA subscription keypair (no pre-existing test constants).
	curve := ecdh.P256()
	uaPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ua key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatal(err)
	}
	p256dh := b64.EncodeToString(uaPriv.PublicKey().Bytes())
	auth := b64.EncodeToString(authSecret)

	tgt := Target{Endpoint: "https://push.example/ep", P256dh: p256dh, Auth: auth}
	var gotEndpoint string
	var gotBody []byte
	fake := delivererFunc(func(_ context.Context, endpoint string, body []byte, _, _ string) error {
		gotEndpoint, gotBody = endpoint, body
		return nil
	})
	s := NewRelaySender(fake)
	n := Notification{Title: "secret-title", Body: "secret-body", Data: map[string]string{"session_id": "s1"}}
	if err := s.Send(context.Background(), tgt, n); err != nil {
		t.Fatal(err)
	}
	if gotEndpoint != tgt.Endpoint {
		t.Errorf("endpoint = %q", gotEndpoint)
	}
	if bytes.Contains(gotBody, []byte("secret-title")) || bytes.Contains(gotBody, []byte("secret-body")) {
		t.Fatal("delivered body leaked cleartext notification text")
	}
	if len(gotBody) == 0 {
		t.Fatal("empty ciphertext")
	}
}

type delivererFunc func(context.Context, string, []byte, string, string) error

func (f delivererFunc) Deliver(ctx context.Context, ep string, b []byte, ttl, u string) error {
	return f(ctx, ep, b, ttl, u)
}
