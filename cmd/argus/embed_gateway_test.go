package main

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
)

// connectLocalGateway serves a co-located gateway and points the TUI client at it
// over loopback, so the returned client sees the fleet (the in-process node
// included) via server.info.
func TestConnectLocalGatewayServesFleet(t *testing.T) {
	sandboxHookDirs(t)
	sock := shortSocket(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := &config.Config{}
	cfg.Token = "secret"
	cfg.Gateway.ListenAddr = "127.0.0.1:0" // ephemeral port; loopback dial uses the bound addr

	c, _, err := connectLocalGateway(ctx, cfg, sock)
	if err != nil {
		t.Fatalf("connectLocalGateway: %v", err)
	}
	defer c.Close()

	deadline := time.Now().Add(3 * time.Second)
	for {
		var info api.ServerInfo
		if c.Call(api.MethodServerInfo, nil, &info) == nil && len(info.Nodes) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("embedded gateway node never visible to the TUI client")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A listen-addr already in use fails synchronously (before the TUI screen),
// instead of leaving the user in a corrupted alt-screen.
func TestConnectLocalGatewayBindError(t *testing.T) {
	sandboxHookDirs(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer occupied.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := &config.Config{}
	cfg.Token = "secret"
	cfg.Gateway.ListenAddr = occupied.Addr().String()

	if _, _, err := connectLocalGateway(ctx, cfg, shortSocket(t)); err == nil {
		t.Fatal("expected a bind error for an occupied listen-addr")
	}
}
