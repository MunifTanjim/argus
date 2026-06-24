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
