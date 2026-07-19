package main

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
)

func cfgWith(url, token string) *config.Config {
	c := &config.Config{Token: token}
	c.Gateway.URL = url
	return c
}

func cfgWithMode(mode, url, token string) *config.Config {
	c := cfgWith(url, token)
	c.Mode = mode
	return c
}

func TestApplyLogLevelSetsConfiguredLevel(t *testing.T) {
	t.Cleanup(func() { config.LogLevel.Set(slog.LevelInfo) })

	if err := applyLogLevel(&config.Config{Log: config.LogConfig{Level: "debug"}}); err != nil {
		t.Fatalf("applyLogLevel: %v", err)
	}
	if got := config.LogLevel.Level(); got != slog.LevelDebug {
		t.Errorf("level = %v, want debug", got)
	}
	if err := applyLogLevel(&config.Config{Log: config.LogConfig{Level: "bogus"}}); err == nil {
		t.Error("expected error for invalid level")
	}
}

func TestRoleGates(t *testing.T) {
	cases := []struct {
		name             string
		mode, url, token string
		wantUplink       bool
		wantGateway      bool
		wantNode         bool
	}{
		// Inference path (--mode unset): unchanged behavior.
		{"connected node", "", "wss://h", "tok", true, false, true},
		{"connected no token", "", "wss://h", "", true, false, true},
		{"co-located gateway", "", "", "tok", false, true, true},
		{"local node", "", "", "", false, false, true},
		// Explicit --mode node: always runs a node, never serves a gateway.
		{"mode node", "node", "", "tok", false, false, true},
		{"mode node uplink", "node", "wss://h", "tok", true, false, true},
		// Explicit --mode gateway: standalone gateway, no local node.
		{"mode gateway", "gateway", "", "tok", false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := cfgWithMode(tc.mode, tc.url, tc.token)
			if got := uplinkMode(cfg); got != tc.wantUplink {
				t.Errorf("uplinkMode = %v, want %v", got, tc.wantUplink)
			}
			if got := serveGatewayMode(cfg); got != tc.wantGateway {
				t.Errorf("serveGatewayMode = %v, want %v", got, tc.wantGateway)
			}
			if got := runLocalNode(cfg); got != tc.wantNode {
				t.Errorf("runLocalNode = %v, want %v", got, tc.wantNode)
			}
		})
	}
}

func TestValidateMode(t *testing.T) {
	cases := []struct {
		name             string
		mode, url, token string
		wantErr          bool
	}{
		{"unset", "", "", "", false},
		{"unset with token infers co-located gateway", "", "", "tok", false},
		{"node", "node", "", "", false},
		{"node with uplink", "node", "wss://h", "", false},
		{"node with uplink and token", "node", "wss://h", "tok", false},
		{"node with token but no upstream", "node", "", "tok", true},
		{"gateway with token", "gateway", "", "tok", false},
		{"gateway without token", "gateway", "", "", true},
		{"gateway with upstream", "gateway", "wss://h", "tok", true},
		{"unknown mode", "relay", "", "tok", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMode(cfgWithMode(tc.mode, tc.url, tc.token))
			if (err != nil) != tc.wantErr {
				t.Errorf("validateMode err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// freeAddr returns an ephemeral loopback address. (Small reuse race, fine for a test.)
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// TestStartStandaloneGatewayServes drives `argus start --mode gateway` through
// runStart: the gateway serves (reporting no nodes) and cancelling ctx returns cleanly.
func TestStartStandaloneGatewayServes(t *testing.T) {
	t.Cleanup(func() { config.LogLevel.Set(slog.LevelInfo) })

	addr := freeAddr(t)
	cfg := cfgWithMode("gateway", "", "secret")
	cfg.Gateway.ListenAddr = addr
	cfg.Log.Level = "error" // keep the run quiet

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runStart(ctx, cancel, newStartCmd("test"), cfg, "test") }()

	// serveGateway starts its listener asynchronously; poll until /client accepts.
	url := "ws://" + addr + "/client"
	deadline := time.Now().Add(3 * time.Second)
	var client *api.Client
	for client == nil {
		if time.Now().After(deadline) {
			t.Fatalf("gateway did not come up on %s", addr)
		}
		select {
		case err := <-done:
			t.Fatalf("runStart returned before serving: %v", err)
		default:
		}
		conn, derr := api.DialWSConn(ctx, url, "secret", nil)
		if derr != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		client = api.NewClient(conn)
		defer conn.Close()
		defer client.Close()
	}

	var info api.ServerInfo
	if err := client.Call(api.MethodServerInfo, nil, &info); err != nil {
		t.Fatalf("server.info: %v", err)
	}
	if len(info.Nodes) != 0 {
		t.Errorf("standalone gateway must report no nodes, got %d", len(info.Nodes))
	}

	cancel() // simulate SIGINT/SIGTERM
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runStart returned %v, want nil on clean shutdown", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runStart did not return after ctx cancel")
	}
}
