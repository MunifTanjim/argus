package e2etest

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
)

func wsURL(httpURL, route string) string {
	return "ws://" + strings.TrimPrefix(httpURL, "http://") + route
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestE2EBlindGatewayRoundTrip(t *testing.T) {
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

	var roster api.NodesListResult
	if err := c.Call(api.MethodNodesList, nil, &roster); err != nil {
		t.Fatalf("nodes.list: %v", err)
	}
	if len(roster.Nodes) != 1 || roster.Nodes[0].ID != "itest-node" {
		t.Fatalf("roster = %+v", roster.Nodes)
	}

	// agents.list is sealed by the client, relayed opaquely by the gateway,
	// decrypted by the node, and the sealed response is opened by the client.
	// An empty result is fine; success proves the whole encrypted path composes.
	var agents api.AgentsListResult
	if err := c.Call(api.MethodAgentsList, nil, &agents); err != nil {
		t.Fatalf("agents.list over e2e: %v", err)
	}
}

func TestNodeIdentifyOverSealedChannel(t *testing.T) {
	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n := node.New()
	n.SetIdentity("itest-identify", "itest-identify")
	n.SetVersion("itest")
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	n.SetIdentityKey(kp)
	n.SetE2EE(true)
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
			if nd.ID == "itest-identify" && nd.IdentityPubKey != "" && nd.Online {
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

	_, known := c.AuthTipKnownForTest(kp.Public)
	if !known {
		t.Fatal("node.identify over the sealed channel did not populate authTip: responder may not route node.identify")
	}
}
