package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gateway"
)

func wsURL(u string) string { return "ws" + strings.TrimPrefix(u, "http") }

// shortSocket returns a unique socket path under /tmp. The default temp dir on
// macOS is too long for a unix socket's sun_path limit (~104 bytes).
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "argus")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// The gateway branch dials over WebSocket and never auto-starts a local node.
func TestConnectGatewayBranch(t *testing.T) {
	hsrv := gateway.NewServer(gateway.New(0), nil, nil) // allow all
	ts := httptest.NewServer(hsrv.Handler())
	defer ts.Close()

	c, err := connect(context.Background(), wsURL(ts.URL), "", "/should/not/be/touched.sock")
	if err != nil {
		t.Fatalf("connect gateway: %v", err)
	}
	defer c.Close()
	var out []any
	if err := c.Call(api.MethodSessionsRefresh, nil, &out); err != nil {
		t.Fatalf("refresh over gateway: %v", err)
	}
}

// connectLocalSpawn starts an embedded node when none is running; cancelling
// ctx stops it and the socket becomes unconnectable.
func TestConnectLocalSpawn(t *testing.T) {
	sock := shortSocket(t)
	ctx, cancel := context.WithCancel(context.Background())

	c, err := connectLocalSpawn(ctx, "", sock)
	if err != nil {
		cancel()
		t.Fatalf("connectLocalSpawn should start a node: %v", err)
	}
	// The embedded node serves: a refresh round-trips without error.
	var out []any
	if err := c.Call(api.MethodSessionsRefresh, nil, &out); err != nil {
		c.Close()
		cancel()
		t.Fatalf("embedded node should answer: %v", err)
	}
	c.Close()

	// Cancelling ctx stops the node; the socket becomes unconnectable.
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := api.Dial(sock); err != nil {
			break // node gone
		}
		if time.Now().After(deadline) {
			t.Fatal("embedded node did not stop after ctx cancel")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A connected spawn enrolls the embedded node on the gateway and points the TUI
// client at the gateway, so the returned client sees the fleet (this machine
// included via the uplink), not just the local socket.
func TestConnectLocalSpawnWithGatewayEnrolls(t *testing.T) {
	hsrv := gateway.NewServer(gateway.New(0), nil, nil) // allow all
	ts := httptest.NewServer(hsrv.Handler())
	defer ts.Close()

	sock := shortSocket(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := connectLocalSpawnWithGateway(ctx, wsURL(ts.URL), "", sock)
	if err != nil {
		t.Fatalf("connectLocalSpawnWithGateway: %v", err)
	}
	defer c.Close()

	// The returned client talks to the gateway, so once the uplink establishes the
	// embedded node shows up in its server.info — proving the TUI sees the fleet.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var info api.ServerInfo
		if c.Call(api.MethodServerInfo, nil, &info) == nil && len(info.Nodes) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("embedded connected node never enrolled / not visible to the TUI client")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A connected spawn against an unreachable/rejecting gateway fails synchronously
// (surfacing the error) instead of dropping the user into the TUI with a silent,
// forever-retrying background uplink.
func TestConnectLocalSpawnWithGatewayReportsBadGateway(t *testing.T) {
	auth := func(tok string) bool { return tok == "right" }
	hsrv := gateway.NewServer(gateway.New(0), auth, auth)
	ts := httptest.NewServer(hsrv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Wrong token: the gateway rejects the upgrade, so the probe must fail.
	if _, err := connectLocalSpawnWithGateway(ctx, wsURL(ts.URL), "WRONG", shortSocket(t)); err == nil {
		t.Fatal("expected an error for a rejected gateway token")
	}

	// Unreachable host: dial failure must also surface.
	if _, err := connectLocalSpawnWithGateway(ctx, "ws://127.0.0.1:1", "right", shortSocket(t)); err == nil {
		t.Fatal("expected an error for an unreachable gateway")
	}
}

func TestNodeAbsent(t *testing.T) {
	if !nodeAbsent(syscall.ENOENT) || !nodeAbsent(syscall.ECONNREFUSED) {
		t.Fatal("ENOENT/ECONNREFUSED should count as node-absent")
	}
	if nodeAbsent(errors.New("some other error")) {
		t.Fatal("unrelated errors should not count as node-absent")
	}
	// A real dial to a missing socket classifies as absent.
	if _, err := api.Dial(shortSocket(t)); err == nil || !nodeAbsent(err) {
		t.Fatalf("missing socket should be node-absent, got %v", err)
	}
}
