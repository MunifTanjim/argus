package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/tui"
)

func wsURL(u string) string { return "ws" + strings.TrimPrefix(u, "http") }

// shortTempDir returns a unique dir under /tmp. The default temp dir on macOS
// is too long for a unix socket's sun_path limit (~104 bytes).
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ar")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func shortSocket(t *testing.T) string {
	return filepath.Join(shortTempDir(t), "s.sock")
}

func waitForNodes(t *testing.T, c tui.Client) {
	t.Helper()
	waitFor(t, "a node visible to the TUI client", 5*time.Second, func() bool {
		var info api.ServerInfo
		return c.Call(api.MethodServerInfo, nil, &info) == nil && len(info.Nodes) > 0
	}, func() string { return "" })
}

func TestConnectLocalSpawn(t *testing.T) {
	sandboxChild(t)
	spec := testSpec(t)
	c, logs, err := connectLocalSpawn(context.Background(), &config.Config{}, spec)
	if err != nil {
		t.Fatalf("connectLocalSpawn should start a node: %v", err)
	}
	t.Cleanup(func() { stopSockets(t, spec.socket) })
	defer c.Close()
	var out []any
	if err := c.Call(api.MethodSessionsRefresh, nil, &out); err != nil {
		t.Fatalf("managed node should answer: %v", err)
	}
	if want := []string{serverLogPath(spec.socket, roleNode)}; !slices.Equal(logs, want) {
		t.Fatalf("logs = %q, want %q", logs, want)
	}
	if b, _ := os.ReadFile(logs[0]); !strings.Contains(string(b), "starting argus node") {
		t.Fatalf("log = %q", b)
	}
}

// A connected spawn enrolls the managed node on the gateway and points the TUI
// client at the gateway, so the client sees the fleet.
func TestConnectLocalSpawnWithGatewayEnrolls(t *testing.T) {
	sandboxChild(t)
	hsrv := gateway.NewServer(gateway.New(0), nil, nil)
	ts := httptest.NewServer(hsrv.Handler())
	defer ts.Close()

	spec := testSpec(t)
	c, _, err := connectLocalSpawnWithGateway(context.Background(), &config.Config{}, spec, wsURL(ts.URL), "", nil)
	if err != nil {
		t.Fatalf("connectLocalSpawnWithGateway: %v", err)
	}
	t.Cleanup(func() { stopSockets(t, spec.socket) })
	defer c.Close()
	waitForNodes(t, c)
}

// A connected spawn against a rejecting or unreachable gateway fails before the
// child starts.
func TestConnectLocalSpawnWithGatewayReportsBadGateway(t *testing.T) {
	auth := func(tok string) bool { return tok == "right" }
	hsrv := gateway.NewServer(gateway.New(0), auth, auth)
	ts := httptest.NewServer(hsrv.Handler())
	defer ts.Close()
	managedBin = func() (string, error) { return "", errors.New("the child must not start") }
	t.Cleanup(func() { managedBin = os.Executable })

	if _, _, err := connectLocalSpawnWithGateway(context.Background(), &config.Config{}, testSpec(t), wsURL(ts.URL), "WRONG", nil); err == nil || strings.Contains(err.Error(), "must not start") {
		t.Fatalf("want the probe error for a rejected token, got %v", err)
	}
	if _, _, err := connectLocalSpawnWithGateway(context.Background(), &config.Config{}, testSpec(t), "ws://127.0.0.1:1", "right", nil); err == nil || strings.Contains(err.Error(), "must not start") {
		t.Fatalf("want the probe error for an unreachable gateway, got %v", err)
	}
}

// The local gateway choice runs the gateway and the node as two processes:
// the gateway socket answers, the node socket answers, and the client sees
// the node through the gateway.
func TestConnectLocalGatewayRunsTwoProcesses(t *testing.T) {
	sandboxChild(t)
	cfg := &config.Config{}
	cfg.Gateway.ListenAddr = "127.0.0.1:0"
	cfg.Token = "tok"
	spec := testSpec(t)
	c, logs, _, err := connectLocalGateway(context.Background(), cfg, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopSockets(t, spec.socket, gatewaySocketPath(spec.socket)) })
	defer c.Close()
	want := []string{serverLogPath(spec.socket, roleGateway), serverLogPath(spec.socket, roleNode)}
	if !slices.Equal(logs, want) {
		t.Fatalf("logs = %q, want %q", logs, want)
	}
	if _, err := probeServerInfo(gatewaySocketPath(spec.socket)); err != nil {
		t.Fatalf("gateway socket: %v", err)
	}
	waitForNodes(t, c)
	for i, line := range []string{"gateway listening", "starting argus node"} {
		if b, _ := os.ReadFile(logs[i]); !strings.Contains(string(b), line) {
			t.Errorf("%s = %q, want %q", logs[i], b, line)
		}
	}
}
