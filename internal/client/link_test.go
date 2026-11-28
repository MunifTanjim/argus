package client

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

func connectLinkClient(t *testing.T) (*fakeMultiGateway, *fakeNode, *E2EClient) {
	t.Helper()
	n := newLifecycleNode(t, "n1", "argus:%1")
	g, clientConn := newFakeMultiGateway(t, n)
	t.Cleanup(func() { g.peer.Close() })
	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	awaitSessionEventFor(t, c, "n1")
	return g, n, c
}

// Dropping a node's channel must tell the gateway, so it can release the relay.
func TestLoseNodeClosesLink(t *testing.T) {
	g, _, c := connectLinkClient(t)
	c.mu.Lock()
	nc := c.byNode["n1"]
	c.mu.Unlock()
	if nc == nil || nc.out == nil || nc.via != "gateway" {
		t.Fatalf("channel link not set: %+v", nc)
	}
	chanID := nc.ch.Load().ID()

	c.loseNode("n1", false)

	select {
	case got := <-g.relayClosed:
		if got != chanID {
			t.Fatalf("relay.close for %q, want %q", got, chanID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gateway never saw relay.close")
	}
}

// A frame arriving on a peer other than the channel's own link is ignored.
func TestFrameFromWrongLinkDropped(t *testing.T) {
	g, n, c := connectLinkClient(t)
	frame, err := n.ch.SealNotificationFrame(api.EncodingJSON, api.MethodSessionEvent, api.RouteHeader{}, snapshotNote("argus:%9").params)
	if err != nil {
		t.Fatal(err)
	}
	f, err := api.ParseRelayFrame(api.EncodingJSON, frame)
	if err != nil {
		t.Fatal(err)
	}

	c.onRelayFrame(g.peer, f) // not the channel's link
	select {
	case ev := <-c.Events():
		t.Fatalf("frame from wrong link delivered: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}

	c.onRelayFrame(c.peer, f)
	select {
	case <-c.Events():
	case <-time.After(5 * time.Second):
		t.Fatal("frame from the channel's own link not delivered")
	}
}

// A handshake that times out must not leave the channel registered or the relay open.
func TestHandshakeTimeoutClosesLink(t *testing.T) {
	SetHandshakeTimeoutForTest(100 * time.Millisecond)
	t.Cleanup(func() { SetHandshakeTimeoutForTest(10 * time.Second) })

	n := newLifecycleNode(t, "n1", "argus:%1")
	n.beforeMsg2 = func() { time.Sleep(500 * time.Millisecond) }
	g, clientConn := newFakeMultiGateway(t, n)
	t.Cleanup(func() { g.peer.Close() })
	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.Connect()

	select {
	case got := <-g.relayClosed:
		if got != "c1" {
			t.Fatalf("relay.close for %q, want c1", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gateway never saw relay.close after handshake timeout")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.byChanID) != 0 || len(c.byNode) != 0 {
		t.Fatalf("channel left registered: byChanID=%d byNode=%d", len(c.byChanID), len(c.byNode))
	}
}
