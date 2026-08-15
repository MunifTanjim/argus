package e2etest

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
)

// TestPlaintextGatewayRoundTrip proves the unencrypted mode still composes end
// to end: a node with e2ee off uplinks to a real gateway, and a plaintext client
// round-trips nodes.list plus a node-addressed call over an identity-cipher relay
// channel (no Noise). This is the path the relay cutover broke and the
// unified-relay wiring restored.
func TestPlaintextGatewayRoundTrip(t *testing.T) {
	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n := node.New()
	n.SetIdentity("itest-plain-node", "itest-plain-node")
	n.SetVersion("itest")
	go n.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

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
			if nd.ID == "itest-plain-node" && nd.Online {
				return true
			}
		}
		return false
	})
	poll.Close()

	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}
	c, err := client.NewReconnectingPlainClient(ctx, dial)
	if err != nil {
		t.Fatalf("NewReconnectingPlainClient: %v", err)
	}
	defer c.Close()

	var roster api.NodesListResult
	if err := c.Call(api.MethodNodesList, nil, &roster); err != nil {
		t.Fatalf("nodes.list: %v", err)
	}
	if len(roster.Nodes) != 1 || roster.Nodes[0].ID != "itest-plain-node" {
		t.Fatalf("roster = %+v", roster.Nodes)
	}

	var agents api.AgentsListResult
	if err := c.Call(api.MethodAgentsList, nil, &agents); err != nil {
		t.Fatalf("agents.list over plaintext relay: %v", err)
	}
}
