package node

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// A listen address that cannot be bound yet (tailscaled has not assigned it)
// is bound once it can be, without stopping the node.
func TestListenP2PRetriesBind(t *testing.T) {
	base, max := p2pListenRetryBase, p2pListenRetryMax
	p2pListenRetryBase, p2pListenRetryMax = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { p2pListenRetryBase, p2pListenRetryMax = base, max })

	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := blocker.Addr().String()
	d := newE2ETestNode(t)
	d.SetE2EE(true)
	d.SetTailnet(&fakeTailnet{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.ListenP2P(ctx, []string{addr})

	blocker.Close()
	waitFor(t, func() bool {
		resp, err := http.Get("http://" + addr + "/p2p")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusServiceUnavailable // served: no uplink yet
	})
}

// The announced endpoints are recomputed before every uplink dial.
func TestUplinkRecomputesEndpoints(t *testing.T) {
	d := newE2ETestNode(t)
	var calls atomic.Int32
	d.SetP2PEndpointSource(func(context.Context) []string {
		if calls.Add(1) == 1 {
			return nil // tailscaled not up yet
		}
		return []string{"ws://100.64.0.1:7777/p2p"}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.ConnectGateway(ctx, "ws://127.0.0.1:1/node", "tok", nil) }()
	defer func() { cancel(); <-done }()

	waitFor(t, func() bool { return calls.Load() >= 2 })
	r, _ := d.handleNodeIdentify(context.Background(), nil)
	if got := r.(api.IdentifyResult).P2PEndpoints; len(got) != 1 || got[0] != "ws://100.64.0.1:7777/p2p" {
		t.Fatalf("p2p endpoints = %v, want the recomputed one", got)
	}
}
