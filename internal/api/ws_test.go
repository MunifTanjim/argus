package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsURL rewrites an http(s) test-server URL to its ws(s) equivalent.
func wsURL(httpURL string) string { return "ws" + strings.TrimPrefix(httpURL, "http") }

func echoServer() *Server {
	s := NewServer()
	s.Handle("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		var in struct{ Msg string }
		_ = json.Unmarshal(params, &in)
		return map[string]string{"got": in.Msg}, nil
	})
	return s
}

func TestWSCallAndNotify(t *testing.T) {
	s := echoServer()
	notifiers := make(chan Notifier, 1)
	s.OnConnect(func(n Notifier) func() { notifiers <- n; return nil })

	ts := httptest.NewServer(s.WSHandler(nil))
	defer ts.Close()

	c, err := DialWS(context.Background(), wsURL(ts.URL), "", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	var out struct{ Got string }
	if err := c.Call("echo", map[string]string{"Msg": "hi"}, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Got != "hi" {
		t.Fatalf("want hi, got %q", out.Got)
	}

	// Server-initiated notification reaches the client.
	var n Notifier
	select {
	case n = <-notifiers:
	case <-time.After(time.Second):
		t.Fatal("server never saw the connection")
	}
	if err := n.Notify("ping", map[string]int{"n": 7}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	select {
	case ev := <-c.Events():
		if ev.Method != "ping" {
			t.Fatalf("want ping, got %q", ev.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("client never received notification")
	}
}

func TestWSAuthRejected(t *testing.T) {
	authorize := func(token string) bool { return token == "secret" }
	ts := httptest.NewServer(echoServer().WSHandler(authorize))
	defer ts.Close()

	// Wrong token: handshake should fail.
	if c, err := DialWS(context.Background(), wsURL(ts.URL), "wrong", nil); err == nil {
		c.Close()
		t.Fatal("want handshake failure with wrong token")
	}

	// Correct token: handshake succeeds and a call works.
	c, err := DialWS(context.Background(), wsURL(ts.URL), "secret", nil)
	if err != nil {
		t.Fatalf("dial with good token: %v", err)
	}
	defer c.Close()
	var out struct{ Got string }
	if err := c.Call("echo", map[string]string{"Msg": "ok"}, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Got != "ok" {
		t.Fatalf("want ok, got %q", out.Got)
	}
}

func TestWSSOverTLS(t *testing.T) {
	ts := httptest.NewTLSServer(echoServer().WSHandler(nil))
	defer ts.Close()

	// ts.Client() trusts the test server's self-signed cert.
	c, err := DialWS(context.Background(), wsURL(ts.URL), "", ts.Client())
	if err != nil {
		t.Fatalf("wss dial: %v", err)
	}
	defer c.Close()

	if !strings.HasPrefix(wsURL(ts.URL), "wss://") {
		t.Fatalf("expected wss:// URL, got %q", wsURL(ts.URL))
	}
	var out struct{ Got string }
	if err := c.Call("echo", map[string]string{"Msg": "tls"}, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Got != "tls" {
		t.Fatalf("want tls, got %q", out.Got)
	}
}

// A symmetric Peer works over WSS too: the node-uplink shape where the dialing
// side also serves inbound requests.
func TestWSDialPeerServesInbound(t *testing.T) {
	// Server side: when a connection arrives, call a method back on the dialer.
	callback := make(chan string, 1)
	s := NewServer()
	s.OnConnect(func(n Notifier) func() {
		go func() {
			// n is a *Peer; it can issue requests to the dialer.
			p, ok := n.(*Peer)
			if !ok {
				return
			}
			var out struct{ Pong string }
			if err := p.Call("downstream", map[string]string{"x": "y"}, &out); err == nil {
				callback <- out.Pong
			}
		}()
		return nil
	})
	ts := httptest.NewServer(s.WSHandler(nil))
	defer ts.Close()

	p, err := DialWSPeer(context.Background(), wsURL(ts.URL), "", nil, PeerOptions{
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			return map[string]string{"pong": method}, nil
		},
	})
	if err != nil {
		t.Fatalf("dial peer: %v", err)
	}
	defer p.Close()

	select {
	case got := <-callback:
		if got != "downstream" {
			t.Fatalf("want downstream, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server could not call back to the dialing peer")
	}
}
