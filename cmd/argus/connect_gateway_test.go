package main

import (
	"context"
	"net"
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
)

// connectLocalGateway starts a managed gateway and points the TUI client at it
// over loopback, so the client sees the fleet.
func TestConnectLocalGatewayServesFleet(t *testing.T) {
	sandboxChild(t)
	cfg := &config.Config{}
	cfg.Token = "secret"
	cfg.Gateway.ListenAddr = "127.0.0.1:0"

	c, srv, _, err := connectLocalGateway(context.Background(), cfg, testSpec(t))
	if err != nil {
		t.Fatalf("connectLocalGateway: %v", err)
	}
	defer srv.stop(managedStopTimeout)
	defer c.Close()
	waitForNodes(t, c)
}

// A listen-addr in use fails before the TUI takes the screen.
func TestConnectLocalGatewayBindError(t *testing.T) {
	sandboxChild(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer occupied.Close()
	cfg := &config.Config{}
	cfg.Token = "secret"
	cfg.Gateway.ListenAddr = occupied.Addr().String()

	if _, _, _, err := connectLocalGateway(context.Background(), cfg, testSpec(t)); err == nil {
		t.Fatal("expected a bind error for an occupied listen-addr")
	}
}
