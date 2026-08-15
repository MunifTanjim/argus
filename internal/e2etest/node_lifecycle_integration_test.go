package e2etest

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// startNodeWithSession brings up a real node uplinked to the gateway, holding one
// discovered session.
func startNodeWithSession(t *testing.T, ctx context.Context, tsURL, id, pane string) *node.Node {
	t.Helper()
	n := node.New()
	n.SetIdentity(id, id)
	n.SetVersion("itest")
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	n.SetIdentityKey(kp)
	n.SetE2EE(true)
	go n.ConnectGateway(ctx, wsURL(tsURL, "/node"), "", nil)
	n.Registry().ReconcileSessions(claudecode.Agent, []registry.DiscoveredSession{{
		HasPane: true, Server: session.TmuxServerArgus, PaneID: pane, AgentSessionID: id + "-sess",
	}})
	return n
}

// waitRostered blocks until the gateway roster lists id as online with its
// identity key — the point from which a client could open a channel to it.
func waitRostered(t *testing.T, ctx context.Context, tsURL, id string) {
	t.Helper()
	pollConn, err := api.DialWSConn(ctx, wsURL(tsURL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	defer poll.Close()
	waitFor(t, "roster has "+id, func() bool {
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		for _, nd := range r.Nodes {
			if nd.ID == id && nd.IdentityPubKey != "" && nd.Online {
				return true
			}
		}
		return false
	})
}

// awaitSessionEventFrom reads the client stream until a session.event for nodeID
// arrives.
func awaitSessionEventFrom(t *testing.T, events <-chan api.Notification, nodeID string) registry.Event {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case n := <-events:
			if n.Method != api.MethodSessionEvent {
				continue
			}
			var ev registry.Event
			if json.Unmarshal(n.Params, &ev) != nil || ev.Session.NodeID != nodeID {
				continue
			}
			return ev
		case <-deadline:
			t.Fatalf("no session.event from node %q reached the client", nodeID)
		}
	}
}

// startGateway runs a real gateway with a short offline grace so a dead node is
// reported as offline quickly.
func startGateway(t *testing.T) *httptest.Server {
	t.Helper()
	agg := gateway.New(time.Second)
	ts := httptest.NewServer(gateway.NewServer(agg, nil, nil).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// connectClient dials a real E2E client at the gateway.
func connectClient(t *testing.T, ctx context.Context, tsURL string) *client.ReconnectingE2EClient {
	t.Helper()
	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(tsURL, "/client"), "", nil)
	}
	c, err := client.NewReconnectingE2EClient(ctx, dial)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// A node joining after the client connected must become visible without a client
// restart: the client owns one channel per node, so it has to adopt the newcomer
// off the gateway's roster notification.
func TestLateJoiningNodeReachesConnectedClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := startGateway(t)

	startNodeWithSession(t, ctx, ts.URL, "node-early", "%1")
	waitRostered(t, ctx, ts.URL, "node-early")

	c := connectClient(t, ctx, ts.URL)
	awaitSessionEventFrom(t, c.Events(), "node-early")

	startNodeWithSession(t, ctx, ts.URL, "node-late", "%2")
	if ev := awaitSessionEventFrom(t, c.Events(), "node-late"); ev.Type != registry.EventAdded {
		t.Fatalf("late node event = %q, want added", ev.Type)
	}

	var list []session.Session
	if err := c.Call(api.MethodSessionsList, nil, &list); err != nil {
		t.Fatalf("sessions.list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("sessions.list returned %d sessions, want 2 (both nodes)", len(list))
	}
}

// A node that drops must grey its sessions on the client at once. Nothing else
// can report them: the node that owned them is what went away.
func TestOfflineNodeGreysItsSessionsOnClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := startGateway(t)

	nodeCtx, killNode := context.WithCancel(ctx)
	startNodeWithSession(t, nodeCtx, ts.URL, "node-dies", "%1")
	waitRostered(t, ctx, ts.URL, "node-dies")

	c := connectClient(t, ctx, ts.URL)
	if ev := awaitSessionEventFrom(t, c.Events(), "node-dies"); ev.Session.Offline {
		t.Fatalf("live session already offline: %+v", ev.Session)
	}

	killNode()
	ev := awaitSessionEventFrom(t, c.Events(), "node-dies")
	if ev.Type != registry.EventUpdated || !ev.Session.Offline {
		t.Fatalf("event after the node died = %q offline=%v, want updated and offline",
			ev.Type, ev.Session.Offline)
	}
}
