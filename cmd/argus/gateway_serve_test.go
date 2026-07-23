package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/logbuf"
	"github.com/MunifTanjim/argus/internal/logger"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/tunnel"
)

// serveGateway must enforce the node/client token: a wrong token is rejected at
// the WebSocket upgrade, the right one is accepted.
func TestServeGatewayEnforcesToken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := logger.NewBufferLogger(logbuf.New(50))
	httpSrv := serveGateway(ctx, gatewayServeOpts{
		node:     node.New(),
		token:    "secret",
		listener: ln,
		log:      log,
		version:  "test",
	})
	t.Cleanup(func() {
		sctx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = httpSrv.Shutdown(sctx)
	})

	url := "ws://" + ln.Addr().String() + "/client"
	if _, err := api.DialWSConn(ctx, url, "wrong", nil); err == nil {
		t.Fatal("gateway must reject a wrong token")
	}
	conn, err := api.DialWSConn(ctx, url, "secret", nil)
	if err != nil {
		t.Fatalf("gateway must accept the right token: %v", err)
	}
	conn.Close()
}

// A standalone gateway (nil node) must serve /client without crashing and report an
// empty view until remote nodes dial in.
func TestServeGatewayStandaloneNoNode(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpSrv := serveGateway(ctx, gatewayServeOpts{
		node:     nil, // standalone: no local node
		token:    "secret",
		listener: ln,
		log:      logger.NewBufferLogger(logbuf.New(50)),
		version:  "test",
	})
	t.Cleanup(func() {
		sctx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = httpSrv.Shutdown(sctx)
	})

	conn, err := api.DialWSConn(ctx, "ws://"+ln.Addr().String()+"/client", "secret", nil)
	if err != nil {
		t.Fatalf("dial /client: %v", err)
	}
	defer conn.Close()

	client := api.NewClient(conn)
	defer client.Close()

	var info api.ServerInfo
	if err := client.Call(api.MethodServerInfo, nil, &info); err != nil {
		t.Fatalf("server.info: %v", err)
	}
	if len(info.Nodes) != 0 {
		t.Errorf("standalone gateway must report no nodes, got %d", len(info.Nodes))
	}
	// Note: session listing is client-side aggregation over per-node E2E channels in
	// the blind gateway; the gateway itself serves no sessions.list. server.info (an
	// empty roster) is the right node-less smoke check here.
}

// failingTunnel's Command errors immediately, so Supervisor.Run returns without a
// restart loop — exercising serveGateway's tunnel-death path.
type failingTunnel struct{}

func (failingTunnel) Name() string { return "fake" }
func (failingTunnel) Command(string) (tunnel.CommandSpec, error) {
	return tunnel.CommandSpec{}, errors.New("boom")
}
func (failingTunnel) ExtractURL(string) (string, bool) { return "", false }

// A listener that dies after startup must invoke onFatal so `argus start` can exit
// non-zero (and the embedded gateway can tear its stack down).
func TestServeGatewayOnFatalOnListenerDeath(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fatal := make(chan struct{}, 1)
	serveGateway(ctx, gatewayServeOpts{
		node:     node.New(),
		listener: ln,
		log:      logger.NewBufferLogger(logbuf.New(50)),
		onFatal:  func() { fatal <- struct{}{} },
		version:  "test",
	})
	ln.Close() // kills the accept loop with a non-ErrServerClosed error

	select {
	case <-fatal:
	case <-time.After(2 * time.Second):
		t.Fatal("onFatal not called after the listener died")
	}
}

// A tunnel that fails to come up must invoke onFatal (a requested tunnel that can't
// serve is fatal), not just log.
func TestServeGatewayOnFatalOnTunnelFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fatal := make(chan struct{}, 1)
	httpSrv := serveGateway(ctx, gatewayServeOpts{
		node:         node.New(),
		listener:     ln,
		log:          logger.NewBufferLogger(logbuf.New(50)),
		onFatal:      func() { fatal <- struct{}{} },
		version:      "test",
		tunnel:       failingTunnel{},
		tunnelOrigin: "http://127.0.0.1:0",
	})
	t.Cleanup(func() {
		sctx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = httpSrv.Shutdown(sctx)
	})

	select {
	case <-fatal:
	case <-time.After(2 * time.Second):
		t.Fatal("onFatal not called after the tunnel failed")
	}
}
