package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
)

func TestLocalNodeRunningAbsent(t *testing.T) {
	// A socket path that does not exist → not running, no error.
	socket := filepath.Join(t.TempDir(), "missing.sock")
	running, err := localNodeRunning(socket)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if running {
		t.Fatal("running = true, want false for a missing socket")
	}
}

// TestOneShotUsesSSH: a client built by dialClient reaches a node through the
// ssh link p2p.ssh configures for it, end to end: ssh (a stand-in that runs
// its remote command locally), `argus bridge`, and the node's local socket.
func TestOneShotUsesSSH(t *testing.T) {
	sandboxChild(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ts := httptest.NewServer(gateway.NewServer(gateway.New(time.Second), nil, nil).Handler())
	defer ts.Close()
	gwURL := "ws://" + strings.TrimPrefix(ts.URL, "http://")

	sock := shortSocket(t)
	n := node.New()
	n.SetIdentity("oneshot-node", "oneshot-node")
	n.SetVersion("itest")
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = n.Run(ctx, sock)
	}()
	defer func() { cancel(); <-runDone }()
	go n.ConnectGateway(ctx, gwURL+"/node", "", nil)

	// ssh drops its options and destination and runs the remote command, which
	// finds argus (this test binary) on PATH.
	bin := shortTempDir(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(bin, "argus")); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do a=$1; shift; [ \"$a\" = -- ] && break; done\nshift\nexec \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ARGUS_SOCKET", sock)

	cfg := &config.Config{}
	cfg.P2P.Enabled = true
	cfg.P2P.SSH = map[string]string{"oneshot-node": "ssh://oneshot-host"}
	cfg.P2P.SSHCommand = "argus bridge"
	waitFor(t, "node rostered", 10*time.Second, func() bool {
		roster, err := fetchRoster(ctx, &config.Config{Gateway: config.GatewayConfig{URL: gwURL}})
		return err == nil && len(roster) == 1 && roster[0].Online
	}, func() string { return "" })

	c, err := dialClient(ctx, cfg, gwURL, "", "", nil)
	if err != nil {
		t.Fatalf("dialClient: %v", err)
	}
	defer c.Close()
	vias := c.(interface{ NodeVias() map[string]string })
	waitFor(t, "node on ssh", 10*time.Second, func() bool {
		return vias.NodeVias()["oneshot-node"] == "ssh"
	}, func() string { return fmt.Sprint(vias.NodeVias()) })
	var agents api.AgentsListResult
	if err := c.Call(api.MethodAgentsList, nil, &agents); err != nil {
		t.Fatalf("agents.list over ssh: %v", err)
	}
}
