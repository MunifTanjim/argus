package e2etest

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// awaitClientSessionEvent reads the client's event stream until a session.event
// arrives, skipping unrelated notifications (node.event, trust beacons, ...).
func awaitClientSessionEvent(t *testing.T, events <-chan api.Notification) registry.Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case n := <-events:
			if n.Method != api.MethodSessionEvent {
				continue
			}
			var ev registry.Event
			if err := json.Unmarshal(n.Params, &ev); err != nil {
				t.Fatalf("decode session.event: %v", err)
			}
			return ev
		case <-deadline:
			t.Fatal("no session.event reached the client")
		}
	}
}

// A session's whole lifecycle must reach a gateway-relayed client: the snapshot
// on channel open, the SessionEnd hook marking it dead, and the removal a later
// scan publishes. The relay carries these sealed with nothing to trigger them
// client-side, so a break anywhere leaves the client's list silently frozen.
func TestSessionLifecycleEventsReachE2EClient(t *testing.T) {
	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n := node.New()
	n.SetIdentity("itest-node", "itest-node")
	n.SetVersion("itest")
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	n.SetIdentityKey(kp)
	n.SetE2EE(true)
	go n.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	// Seed before the client connects so the snapshot itself is under test.
	reg := n.Registry()
	reg.ReconcileSessions(claudecode.Agent, []registry.DiscoveredSession{{
		HasPane:        true,
		Server:         session.TmuxServerArgus,
		PaneID:         "%1",
		AgentSessionID: "agent-session-1",
	}})

	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	waitFor(t, "node adoption", func() bool {
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		for _, nd := range r.Nodes {
			if nd.ID == "itest-node" && nd.IdentityPubKey != "" && nd.Online {
				return true
			}
		}
		return false
	})
	poll.Close()

	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}
	c, err := client.NewReconnectingE2EClient(ctx, dial)
	if err != nil {
		t.Fatalf("NewReconnectingE2EClient: %v", err)
	}
	defer c.Close()

	added := awaitClientSessionEvent(t, c.Events())
	if added.Type != registry.EventAdded {
		t.Fatalf("snapshot event type = %q, want added", added.Type)
	}
	// The client stamps origin onto every relayed session; without it the TUI
	// can't route a later call back to the node that owns the session.
	if added.Session.NodeID != "itest-node" {
		t.Fatalf("snapshot session node = %q, want itest-node", added.Session.NodeID)
	}
	if !strings.Contains(added.Session.ID, "%1") {
		t.Fatalf("snapshot session id = %q, want the pane's session", added.Session.ID)
	}

	claudecode.ProcessHook(reg, adapter.HookEvent{
		Agent:      claudecode.Agent,
		Event:      "SessionEnd",
		TmuxPane:   "%1",
		TmuxSocket: "argus",
		Payload:    json.RawMessage(`{"session_id":"agent-session-1","reason":"other"}`),
	})
	// SessionEnd drops the session outright (ApplyHook removes on a dead status),
	// so the client sees a removal carrying the dead status — not an update.
	dead := awaitClientSessionEvent(t, c.Events())
	if dead.Type != registry.EventRemoved || dead.Session.Status != session.StatusDead {
		t.Fatalf("SessionEnd event = %q/%q, want removed/dead", dead.Type, dead.Session.Status)
	}
	if dead.Session.NodeID != "itest-node" {
		t.Fatalf("SessionEnd session node = %q, want itest-node", dead.Session.NodeID)
	}

	// The stream survives the removal: a later scan re-adds and re-removes.
	reg.ReconcileSessions(claudecode.Agent, []registry.DiscoveredSession{{
		HasPane: true, Server: session.TmuxServerArgus, PaneID: "%2",
	}})
	if ev := awaitClientSessionEvent(t, c.Events()); ev.Type != registry.EventAdded {
		t.Fatalf("rescan event type = %q, want added", ev.Type)
	}
	reg.ReconcileSessions(claudecode.Agent, nil)
	if ev := awaitClientSessionEvent(t, c.Events()); ev.Type != registry.EventRemoved {
		t.Fatalf("post-scan event type = %q, want removed", ev.Type)
	}
}
