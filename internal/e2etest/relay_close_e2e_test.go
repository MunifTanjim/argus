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
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// A client that disconnects must not leave its channel (and the event stream and
// transcript pollers riding it) running on the node.
func TestClientDisconnectClosesNodeChannel(t *testing.T) {
	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n := newE2ENode(t, "rc-node", "Node")
	n.Registry().ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%1",
		AgentSessionID: "rc-session", Status: session.StatusIdle,
	})
	go n.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	waitFor(t, "node online", func() bool {
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		for _, nd := range r.Nodes {
			if nd.ID == "rc-node" && nd.IdentityPubKey != "" && nd.Online {
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
	var sessions []session.Session
	if err := c.Call(api.MethodSessionsList, nil, &sessions); err != nil {
		t.Fatalf("sessions.list: %v", err)
	}
	waitFor(t, "node channel open", func() bool { return n.RelayChannelCountForTest() == 1 })

	c.Close()
	waitFor(t, "node channel closed after client disconnect", func() bool { return n.RelayChannelCountForTest() == 0 })
}
