package node

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// delivererFunc is a push.Deliverer backed by a function, for node package tests.
type delivererFunc func(context.Context, string, []byte, string, string) error

func (f delivererFunc) Deliver(ctx context.Context, ep string, b []byte, ttl, u string) error {
	return f(ctx, ep, b, ttl, u)
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestNodeHandlePushRegisterAndTest(t *testing.T) {
	d := New()
	d.SetPushStore(push.NewStore(t.TempDir()))
	var delivered []byte
	d.SetPushDeliverer(delivererFunc(func(_ context.Context, _ string, body []byte, _, _ string) error {
		delivered = body
		return nil
	}))

	// Generate a real P256 UA keypair and auth secret for Web Push encryption.
	curve := ecdh.P256()
	uaPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ua key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	testP256dh := enc.EncodeToString(uaPriv.PublicKey().Bytes())
	testAuth := enc.EncodeToString(authSecret)

	dispatch := d.server.DispatchFunc()

	reg := mustMarshal(api.PushRegisterParams{DeviceID: "dev1", Endpoint: "https://p/ep", P256dh: testP256dh, Auth: testAuth})
	if _, err := dispatch(context.Background(), api.MethodPushRegister, reg); err != nil {
		t.Fatalf("register: %v", err)
	}
	ref := mustMarshal(api.PushDeviceRef{DeviceID: "dev1"})
	if _, err := dispatch(context.Background(), api.MethodPushTest, ref); err != nil {
		t.Fatalf("test: %v", err)
	}
	if len(delivered) == 0 {
		t.Fatal("push.test did not deliver an encrypted body")
	}
}

func TestNodeStartPushDeliversMobileOnAwaitingInput(t *testing.T) {
	// Generate a real P256 UA keypair and auth secret for Web Push encryption.
	curve := ecdh.P256()
	uaPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ua key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	testP256dh := enc.EncodeToString(uaPriv.PublicKey().Bytes())
	testAuth := enc.EncodeToString(authSecret)

	d := New()
	d.SetPushStore(push.NewStore(t.TempDir()))
	got := make(chan []byte, 1)
	d.SetPushDeliverer(delivererFunc(func(_ context.Context, _ string, body []byte, _, _ string) error {
		got <- body
		return nil
	}))

	dispatch := d.server.DispatchFunc()
	reg := mustMarshal(api.PushRegisterParams{DeviceID: "dev1", Endpoint: "https://p/ep", P256dh: testP256dh, Auth: testAuth})
	if _, err := dispatch(context.Background(), api.MethodPushRegister, reg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.StartPush(ctx, 0) // delay 0 => mobile fires immediately

	// Allow the goroutine to subscribe to the registry before publishing events.
	time.Sleep(20 * time.Millisecond)

	// Drive a session into awaiting-input through the registry.
	d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", AgentSessionID: "s1", Status: session.StatusWorking})
	d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", AgentSessionID: "s1", Status: session.StatusAwaitingInput})

	select {
	case body := <-got:
		if len(body) == 0 {
			t.Fatal("empty ciphertext delivered")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no mobile push delivered")
	}
}

func TestNodeStartPushDelivererRefreshedAfterStart(t *testing.T) {
	// Prove that a deliverer set AFTER StartPush begins is still picked up:
	// this is the reconnect scenario where runUplink refreshes the deliverer
	// via SetPushDeliverer while the Watch loop is already running.
	curve := ecdh.P256()
	uaPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ua key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	testP256dh := enc.EncodeToString(uaPriv.PublicKey().Bytes())
	testAuth := enc.EncodeToString(authSecret)

	d := New()
	d.SetPushStore(push.NewStore(t.TempDir()))
	// No deliverer set yet — simulates node starting before uplink connects.

	dispatch := d.server.DispatchFunc()
	reg := mustMarshal(api.PushRegisterParams{DeviceID: "dev1", Endpoint: "https://p/ep", P256dh: testP256dh, Auth: testAuth})
	if _, err := dispatch(context.Background(), api.MethodPushRegister, reg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.StartPush(ctx, 0)

	// Allow Watch to subscribe before we set the deliverer and publish events.
	time.Sleep(20 * time.Millisecond)

	// Simulate uplink connecting: set deliverer AFTER Watch is already running.
	got := make(chan []byte, 1)
	d.SetPushDeliverer(delivererFunc(func(_ context.Context, _ string, body []byte, _, _ string) error {
		got <- body
		return nil
	}))

	// Drive session into awaiting-input.
	d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", AgentSessionID: "s2", Status: session.StatusWorking})
	d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", AgentSessionID: "s2", Status: session.StatusAwaitingInput})

	select {
	case body := <-got:
		if len(body) == 0 {
			t.Fatal("empty ciphertext delivered")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no mobile push delivered after deliverer refresh")
	}
}
