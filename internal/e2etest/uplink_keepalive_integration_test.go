package e2etest

import (
	"context"
	"net"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/session"
)

// shortKeepalive compresses the gateway's node heartbeat so a test covers many
// keepalive cycles in about a second. The reply timeout stays generous relative to
// the interval: the bug under test refuses pings outright, so it reproduces no
// matter how long the timeout is, and a tight one would just make the test flaky
// on a loaded machine.
func shortKeepalive(t *testing.T) {
	t.Helper()
	gateway.SetNodeKeepaliveForTest(50*time.Millisecond, 2*time.Second, 2)
	t.Cleanup(gateway.ResetNodeKeepaliveForTest)
}

// gatewayWithNode stands up a real gateway plus one real uplinked node and waits
// for adoption.
func gatewayWithNode(t *testing.T, ctx context.Context) (*gateway.Aggregator, *httptest.Server) {
	t.Helper()
	agg := gateway.New(30 * time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	n := node.New()
	n.SetIdentity("ka-node", "ka-node")
	n.SetVersion("itest")
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	n.SetIdentityKey(kp)
	n.SetE2EE(true)
	go n.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	waitFor(t, "node adoption", func() bool {
		for _, nd := range agg.Roster() {
			if nd.ID == "ka-node" && nd.IdentityPubKey != "" && nd.Online {
				return true
			}
		}
		return false
	})
	return agg, ts
}

// Regression: the gateway heartbeats node uplinks, and the node's blind uplink
// dispatch serves only node.identify and ping. When ping was refused by that dispatch, the
// gateway scored every heartbeat as a miss and tore the uplink down on a fixed
// ~30s cycle. An idle uplink must survive indefinitely.
func TestNodeUplinkSurvivesGatewayKeepalive(t *testing.T) {
	shortKeepalive(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agg, _ := gatewayWithNode(t, ctx)

	var drops atomic.Int64
	events, cancelSub := agg.SubscribeRoster()
	defer cancelSub()
	go func() {
		for ev := range events {
			if ev.Type == api.NodeEventOffline {
				drops.Add(1)
			}
		}
	}()

	// 1s at a 50ms heartbeat is ~20 cycles; the bug tore the link down every 2.
	time.Sleep(time.Second)

	if d := drops.Load(); d != 0 {
		t.Fatalf("idle uplink torn down %d time(s) by the gateway's own keepalive", d)
	}
}

// Regression: the uplink churn above silently killed the client's E2E channel. The
// client kept its stale channel (it only re-handshakes on a fresh gateway
// connection, and its gateway link stayed healthy), so every node-addressed call
// blocked for the full 30s call timeout and then returned an empty result with no
// error — permanently, while the roster still reported the node online.
func TestE2EChannelSurvivesKeepaliveWindow(t *testing.T) {
	shortKeepalive(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, ts := gatewayWithNode(t, ctx)

	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}
	c, err := client.NewReconnectingE2EClient(ctx, dial)
	if err != nil {
		t.Fatalf("e2e client: %v", err)
	}
	defer c.Close()

	callSessions := func(when string) {
		t.Helper()
		start := time.Now()
		var ss []session.Session
		if err := c.Call(api.MethodSessionsList, nil, &ss); err != nil {
			t.Fatalf("%s: sessions.list: %v", when, err)
		}
		// A dead channel manifests as a full-timeout stall, not an error, so assert
		// on latency: the call must be answered by the node, not by the timeout.
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("%s: sessions.list took %s — the E2E channel is dead and the call stalled to its timeout", when, d)
		}
	}

	callSessions("before the keepalive window")
	time.Sleep(time.Second) // ~20 heartbeat cycles
	callSessions("after the keepalive window")
}
