package node

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func wsURL(u string) string { return "ws" + strings.TrimPrefix(u, "http") }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for condition")
}

// End-to-end: a node dials the gateway, the gateway aggregates it under a composite id,
// a client connected to the gateway sees the session and routes control calls back to
// the originating node.
func TestNodeUplinkEndToEnd(t *testing.T) {
	agg := gateway.New(time.Second)
	hsrv := gateway.NewServer(agg,
		func(tok string) bool { return tok == "dtok" },
		func(tok string) bool { return tok == "ctok" },
	)
	ts := httptest.NewServer(hsrv.Handler())
	defer ts.Close()

	// Node with no tmux clients: control calls surface a node-side error, which
	// proves the call was routed all the way to the node.
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.SetIdentity("home", "home-box")
	d.reg.ApplyHook(registry.HookUpdate{
		Tool: "claude-code", Server: session.TmuxServerDefault, PaneID: "%1",
		ClaudeSessionID: "abc", Status: session.StatusWorking,
	})

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

	list := func() []session.Session {
		var l []session.Session
		if err := c.Call(api.MethodSessionsList, nil, &l); err != nil {
			t.Fatalf("list: %v", err)
		}
		return l
	}
	waitFor(t, func() bool { return len(list()) == 1 })

	s := list()[0]
	if s.NodeID != "home" || s.NodeLabel != "home-box" {
		t.Fatalf("origin not stamped: %+v", s)
	}
	if !strings.HasPrefix(s.ID, "home:") {
		t.Fatalf("want composite id prefixed home:, got %q", s.ID)
	}
	composite := s.ID

	// Routed control call reaches the node: capture fails there (no tmux client),
	// and that node-side error propagates back to the client.
	if err := c.Call(api.MethodSessionCapture, api.SessionRef{SessionID: composite}, &api.CaptureResult{}); err == nil {
		t.Fatal("expected capture to fail at the node")
	} else if !strings.Contains(err.Error(), "tmux client") {
		t.Fatalf("want node-side tmux error, got %v", err)
	}

	// Routed read that succeeds at the node: empty transcript, no error.
	if err := c.Call(api.MethodSessionTranscriptView, api.SessionRef{SessionID: composite}, nil); err != nil {
		t.Fatalf("transcriptView should route and succeed: %v", err)
	}

	// Live event: a new session on the node streams through to the client.
	events := c.Events()
	d.reg.ApplyHook(registry.HookUpdate{
		Tool: "claude-code", Server: session.TmuxServerDefault, PaneID: "%2",
		ClaudeSessionID: "def", Status: session.StatusWorking,
	})
	deadline := time.After(3 * time.Second)
	for {
		select {
		case n := <-events:
			var ev registry.Event
			if json.Unmarshal(n.Params, &ev) != nil {
				continue
			}
			if ev.Session.NodeID == "home" && strings.Contains(ev.Session.ID, "%2") {
				return // success
			}
		case <-deadline:
			t.Fatal("client never received the live %2 session event")
		}
	}
}
