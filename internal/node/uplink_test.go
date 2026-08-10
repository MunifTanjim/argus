package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func wsURL(u string) string { return "ws" + strings.TrimPrefix(u, "http") }

func waitFor(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", desc)
}

// TestUplinkDispatchNarrow verifies that uplinkDispatch allows node.identify but
// rejects all other methods with CodeMethodNotFound.
func TestUplinkDispatchNarrow(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.SetIdentity("test-node", "test-box")

	dispatch := d.uplinkDispatch()
	ctx := context.Background()

	// node.identify should be allowed (delegated); the node returns its identity.
	result, err := dispatch(ctx, api.MethodNodeIdentify, nil)
	if err != nil {
		t.Fatalf("node.identify should succeed: %v", err)
	}
	id, ok := result.(api.IdentifyResult)
	if !ok {
		t.Fatalf("expected IdentifyResult, got %T", result)
	}
	if id.ID != "test-node" {
		t.Errorf("expected ID=test-node, got %q", id.ID)
	}

	// Any other method must return CodeMethodNotFound.
	for _, method := range []string{api.MethodSessionsList, "sessions.refresh", "lock.status", "terminal.open"} {
		_, err := dispatch(ctx, method, nil)
		var rpcErr *api.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != api.CodeMethodNotFound {
			t.Errorf("method %q: want CodeMethodNotFound, got err=%v", method, err)
		}
	}
}

// TestUplinkNoSessionEventPush verifies that after the uplink connects the node
// does NOT push session.event notifications when its registry changes.
func TestUplinkNoSessionEventPush(t *testing.T) {
	var sessionEventCount atomic.Int32
	connected := make(chan struct{})

	// Fake gateway server: answers node.identify and records any session.event.
	// Closes connected when the WebSocket handshake succeeds (uplink established).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := api.AcceptWS(w, r)
		if err != nil {
			return
		}
		select {
		case <-connected:
		default:
			close(connected)
		}
		peer := api.NewPeer(conn, api.PeerOptions{
			Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
				if method == api.MethodNodeIdentify {
					return api.IdentifyResult{ID: "fake-gw"}, nil
				}
				return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
			},
			OnNotify: func(n api.Notification) {
				if n.Method == api.MethodSessionEvent {
					sessionEventCount.Add(1)
				}
			},
		})
		<-peer.Done()
	}))
	defer ts.Close()

	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.SetIdentity("test-node", "test-box")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.ConnectGateway(ctx, wsURL(ts.URL), "", nil)

	// Wait for the WebSocket handshake to complete before mutating the registry —
	// a fixed sleep passes trivially if the connection is not yet up.
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for node to connect to fake gateway")
	}

	// Mutate the node's registry several times to trigger events.
	for i := 0; i < 3; i++ {
		d.reg.ReconcileSessions("claude", []registry.DiscoveredSession{
			{AgentSessionID: fmt.Sprintf("sess-%d", i)},
		})
	}

	// Allow any in-flight notifications time to arrive.
	time.Sleep(100 * time.Millisecond)

	if n := sessionEventCount.Load(); n > 0 {
		t.Errorf("node pushed %d session.event(s) over the uplink; expected 0", n)
	}
}

// End-to-end: a node dials the gateway and is visible to clients via server.info.
// The gateway is blind to sessions; this test verifies node enrollment only.
func TestNodeUplinkEndToEnd(t *testing.T) {
	agg := gateway.New(time.Second)
	hsrv := gateway.NewServer(agg,
		func(tok string) bool { return tok == "dtok" },
		func(tok string) bool { return tok == "ctok" },
	)
	ts := httptest.NewServer(hsrv.Handler())
	defer ts.Close()

	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.SetIdentity("home", "home-box")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.ConnectGateway(ctx, wsURL(ts.URL)+"/node", "dtok", nil)

	// Node token is required.
	if _, err := api.DialWS(ctx, wsURL(ts.URL)+"/client", "wrong", nil); err == nil {
		t.Fatal("client with wrong token should be rejected")
	}

	c, err := api.DialWS(ctx, wsURL(ts.URL)+"/client", "ctok", nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer c.Close()

	// The node must appear in server.info once the uplink establishes.
	waitFor(t, "node visible in server.info", func() bool {
		var info api.ServerInfo
		if err := c.Call(api.MethodServerInfo, nil, &info); err != nil {
			return false
		}
		for _, n := range info.Nodes {
			if n.ID == "home" && strings.Contains(n.Label, "home-box") {
				return true
			}
		}
		return false
	})
}
