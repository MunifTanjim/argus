package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/session"
)

// reconnectDialer returns an api.Dialer that stands up a fresh fake gateway+node
// (with a STABLE node key) on each dial, so the client can re-handshake after a
// reconnect. It also returns a func to fetch the most recent fake (to force a drop).
func reconnectDialer(t *testing.T) (api.Dialer, func() *fakeGatewayNode) {
	t.Helper()
	nodeKey, _ := e2e.GenerateKeyPair()
	var latest *fakeGatewayNode
	dial := func(ctx context.Context) (net.Conn, error) {
		gwConn, clientConn := net.Pipe()
		f := &fakeGatewayNode{nodeID: "n1", nodeKey: nodeKey}
		f.handle = func(_ string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
			return params, nil, nil // echo
		}
		f.peer = api.NewPeer(gwConn, api.PeerOptions{
			Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
				switch method {
				case api.MethodNodesList:
					return api.NodesListResult{Nodes: []api.NodeDescriptor{{
						ID: "n1", Label: "n1-box", Online: true,
						IdentityPubKey: base64.StdEncoding.EncodeToString(nodeKey.Public),
					}}}, nil
				case api.MethodRelayOpen:
					return api.RelayOpenResult{ChanID: "c1"}, nil
				}
				return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: method}
			},
			OnRelayFrame: f.onFrame,
		})
		latest = f
		return clientConn, nil
	}
	return dial, func() *fakeGatewayNode { return latest }
}

func TestReconnectingE2EClientReconnectsAndReHandshakes(t *testing.T) {
	dial, latest := reconnectDialer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := NewReconnectingE2EClient(ctx, dial)
	if err != nil {
		t.Fatalf("NewReconnectingE2EClient: %v", err)
	}
	defer c.Close()

	// First connection works.
	var out map[string]any
	if err := c.Call(api.MethodSessionInput, map[string]any{
		"session_id": session.CompositeID("n1", "s"), "text": "one",
	}, &out); err != nil {
		t.Fatalf("call before drop: %v", err)
	}
	if out["text"] != "one" {
		t.Fatalf("echo = %v", out)
	}

	// Drop the current connection; expect a States() false then true.
	latest().peer.Close()
	if got := waitState(t, c); got != false {
		t.Fatalf("first state = %v, want false (disconnected)", got)
	}
	if got := waitState(t, c); got != true {
		t.Fatalf("second state = %v, want true (reconnected)", got)
	}

	// After reconnect + re-handshake, calls work again over the new channel.
	out = nil
	if err := c.Call(api.MethodSessionInput, map[string]any{
		"session_id": session.CompositeID("n1", "s"), "text": "two",
	}, &out); err != nil {
		t.Fatalf("call after reconnect: %v", err)
	}
	if out["text"] != "two" {
		t.Fatalf("echo after reconnect = %v", out)
	}
}

func TestReconnectingE2EClientLockedStableStaticAcrossReconnect(t *testing.T) {
	dial, latest := reconnectDialer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	static, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	c, err := NewReconnectingE2EClientLocked(ctx, dial, nil, static, "")
	if err != nil {
		t.Fatalf("NewReconnectingE2EClientLocked: %v", err)
	}
	defer c.Close()

	// The reconnecting client must store the provided static.
	if !bytes.Equal(c.static.Public, static.Public) || !bytes.Equal(c.static.Private, static.Private) {
		t.Errorf("ReconnectingE2EClient.static does not match provided static")
	}
	// The first E2EClient must present the same static.
	cur := c.current()
	if !bytes.Equal(cur.static.Public, static.Public) || !bytes.Equal(cur.static.Private, static.Private) {
		t.Errorf("initial E2EClient.static does not match provided static")
	}

	// Force a reconnect and verify the static is preserved across the reconnect.
	latest().peer.Close()
	if got := waitState(t, c); got != false {
		t.Fatalf("first state = %v, want false (disconnected)", got)
	}
	if got := waitState(t, c); got != true {
		t.Fatalf("second state = %v, want true (reconnected)", got)
	}
	cur2 := c.current()
	if !bytes.Equal(cur2.static.Public, static.Public) || !bytes.Equal(cur2.static.Private, static.Private) {
		t.Errorf("E2EClient.static changed across reconnect")
	}
}

func waitState(t *testing.T, c *ReconnectingE2EClient) bool {
	t.Helper()
	select {
	case s := <-c.States():
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a state transition")
		return false
	}
}

func TestReconnectingE2EClientCallErrorsWhileDisconnected(t *testing.T) {
	// A dialer that fails after the first connect, so a drop leaves it disconnected.
	nodeKey, _ := e2e.GenerateKeyPair()
	first := true
	dial := func(ctx context.Context) (net.Conn, error) {
		if !first {
			return nil, context.DeadlineExceeded // stay disconnected
		}
		first = false
		gwConn, clientConn := net.Pipe()
		f := &fakeGatewayNode{nodeID: "n1", nodeKey: nodeKey}
		f.handle = func(_ string, p json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) { return p, nil, nil }
		f.peer = api.NewPeer(gwConn, api.PeerOptions{
			Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
				switch method {
				case api.MethodNodesList:
					return api.NodesListResult{Nodes: []api.NodeDescriptor{{ID: "n1", Online: true, IdentityPubKey: base64.StdEncoding.EncodeToString(nodeKey.Public)}}}, nil
				case api.MethodRelayOpen:
					return api.RelayOpenResult{ChanID: "c1"}, nil
				}
				return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: method}
			},
			OnRelayFrame: f.onFrame,
		})
		return clientConn, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := NewReconnectingE2EClient(ctx, dial)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	// Force the peer to drop; the redial fails, so we stay disconnected.
	c.current().Close()
	if got := waitState(t, c); got != false {
		t.Fatalf("state = %v, want false", got)
	}
	if err := c.Call(api.MethodSessionsList, nil, nil); err == nil {
		t.Error("Call while disconnected must error")
	}
}
