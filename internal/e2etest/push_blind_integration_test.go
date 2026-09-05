package e2etest

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// TestBlindPushNodeDeliversEncryptedPayload proves the e2ee push spec:
// a node encrypts and delivers a push through the gateway (a pure router)
// without the gateway ever seeing the notification cleartext.
func TestBlindPushNodeDeliversEncryptedPayload(t *testing.T) {
	var (
		mu          sync.Mutex
		gotAuth     string
		gotBody     []byte
		gotDelivery = make(chan struct{}, 1)
	)
	fakeEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotBody = body
		mu.Unlock()
		select {
		case gotDelivery <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer fakeEndpoint.Close()

	// VAPID key persisted in a temp dir.
	vapid, err := push.LoadOrCreateVAPID(filepath.Join(t.TempDir(), "vapid_key.pem"))
	if err != nil {
		t.Fatalf("vapid: %v", err)
	}

	// Gateway: holds VAPID key, relays opaque push.deliver bodies.
	agg := gateway.New(time.Second)
	gwsrv := gateway.NewServer(agg, nil, nil)
	gwsrv.SetVAPIDPublicKey(vapid.PublicKey())
	gwsrv.SetPushDeliverer(push.NewGatewayDeliverer(vapid))
	ts := httptest.NewServer(gwsrv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Node with e2ee identity and a push subscription store.
	d := node.New()
	d.SetIdentity("push-itest", "push-itest")
	d.SetVersion("itest")
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("identity keypair: %v", err)
	}
	d.SetIdentityKey(kp)
	d.SetE2EE(true)
	d.SetPushStore(push.NewStore(t.TempDir()))

	// delay=0: mobile dispatcher fires on the awaiting-input transition immediately.
	go d.StartPush(ctx, 0)

	// Run the node on a short-path socket so push.register is callable via the API.
	sd := sockDir(t)
	sockPath := filepath.Join(sd, "p.sock")
	go func() { _ = d.Run(ctx, sockPath) }()
	waitFor(t, "node socket ready", func() bool {
		_, err := os.Stat(sockPath)
		return err == nil
	})

	// Real UA P256 keypair + auth secret for Web Push encryption.
	uaPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ua keypair: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding

	// Seed the subscription via the node's local socket (the production path).
	nc, err := api.Dial(sockPath)
	if err != nil {
		t.Fatalf("dial node socket: %v", err)
	}
	if err := nc.Call(api.MethodPushRegister, api.PushRegisterParams{
		DeviceID: "ua-dev-1",
		Endpoint: fakeEndpoint.URL,
		P256dh:   enc.EncodeToString(uaPriv.PublicKey().Bytes()),
		Auth:     enc.EncodeToString(authSecret),
	}, nil); err != nil {
		t.Fatalf("push.register: %v", err)
	}
	nc.Close()

	// Connect the node to the blind gateway; runUplink sets the uplinkDeliverer
	// because d.pushStore != nil.
	go d.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	// Wait for the node in the roster — uplink is established and deliverer is set.
	waitFor(t, "node rostered", func() bool {
		pc, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
		if err != nil {
			return false
		}
		poll := api.NewClient(pc)
		defer poll.Close()
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		for _, n := range r.Nodes {
			if n.ID == "push-itest" && n.Online {
				return true
			}
		}
		return false
	})

	// Drive the session into awaiting-input; StartPush's Watch fires the dispatcher.
	d.Registry().ApplyHook(registry.HookUpdate{Agent: "claude", AgentSessionID: "s1", Status: session.StatusWorking})
	d.Registry().ApplyHook(registry.HookUpdate{Agent: "claude", AgentSessionID: "s1", Status: session.StatusAwaitingInput})

	// The fake endpoint must receive a POST within the deadline.
	select {
	case <-gotDelivery:
	case <-time.After(5 * time.Second):
		t.Fatal("no push delivered to fake endpoint within 5s")
	}

	mu.Lock()
	auth, body := gotAuth, gotBody
	mu.Unlock()

	// Must carry a VAPID Authorization header (gateway signed it).
	if !strings.HasPrefix(auth, "vapid ") {
		t.Fatalf("expected VAPID Authorization header, got: %q", auth)
	}
	// Body must be non-empty aes128gcm ciphertext.
	if len(body) == 0 {
		t.Fatal("push body is empty")
	}
	// The gateway never saw the cleartext; the body must be opaque.
	if strings.Contains(string(body), "Needs your attention") || strings.Contains(string(body), "claude") {
		t.Fatal("push body contains notification cleartext — gateway is not blind")
	}
}
