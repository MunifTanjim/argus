package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

func runStop(t *testing.T, socket string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCmd("test")
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"stop", "--no-config", "--socket", socket})
	var err error
	stderr := captureStderr(t, func() { err = cmd.Execute() })
	if err != nil {
		err = fmt.Errorf("%w: %s", err, stderr)
	}
	return out.String(), err
}

func TestStopNothingRunning(t *testing.T) {
	sock := shortSocket(t)
	if err := saveLaunchState(sock, launchState{Mode: launchModeIsolated}); err != nil {
		t.Fatal(err)
	}
	out, err := runStop(t, sock)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "argus is not running") {
		t.Fatalf("output = %q", out)
	}
	if st, err := loadLaunchState(sock); st != nil || err != nil {
		t.Fatalf("state file left behind: %v, %v", st, err)
	}
}

func TestStopStopsManagedNode(t *testing.T) {
	sandboxChild(t)
	spec := testSpec(t)
	srv, err := startManagedReady(spec, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.stop(managedStopTimeout) })
	out, err := runStop(t, spec.socket)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "stopped node") {
		t.Fatalf("output = %q", out)
	}
	if err := srv.wait(); err != nil {
		t.Fatalf("exit = %v, want a clean exit", err)
	}
	if running, _ := localNodeRunning(spec.socket); running {
		t.Fatal("node socket still served")
	}
}

func TestStopStopsGatewayAndNode(t *testing.T) {
	sandboxChild(t)
	addr, _, err := resolveListenAddr("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw := testSpec(t)
	gw.role = roleGateway
	gw.listenAddr = addr
	gw.token = "tok"
	n := spawnSpec{socket: gw.socket, gatewayURL: "ws://" + addr, token: "tok", configArgs: []string{"--no-config"}}
	g, err := startManagedGroup([]spawnSpec{gw, n}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.stop(managedStopTimeout) })
	out, err := runStop(t, gw.socket)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "stopped node") || !strings.Contains(out, "stopped gateway") {
		t.Fatalf("output = %q", out)
	}
	for _, sock := range []string{gw.socket, gatewaySocketPath(gw.socket)} {
		if running, _ := localNodeRunning(sock); running {
			t.Errorf("%s still served", sock)
		}
	}
}

func TestStopReportsOldServer(t *testing.T) {
	sock := shortSocket(t)
	serveOldDaemon(t, sock)
	_, err := runStop(t, sock)
	if err == nil || !strings.Contains(err.Error(), "argus stop: ") || !strings.Contains(err.Error(), "does not support stop") {
		t.Fatalf("err = %v, want does not support stop", err)
	}
}

func TestStopCountsVanishedGatewayAsStopped(t *testing.T) {
	sock := shortSocket(t)
	gwSock := gatewaySocketPath(sock)

	gl, err := api.ListenUnix(gwSock, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := gl.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()

	nl, err := api.ListenUnix(sock, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	srv := api.NewServer()
	srv.Handle(api.MethodServerStop, func(context.Context, json.RawMessage) (any, error) {
		go func() {
			time.Sleep(20 * time.Millisecond)
			nl.Close()
		}()
		return nil, nil
	})
	go func() { _ = srv.Serve(nl) }()
	t.Cleanup(func() {
		nl.Close()
		gl.Close()
		os.Remove(gwSock)
	})

	// The supervisor stops the gateway once the node exits cleanly.
	go func() {
		for {
			if running, _ := localNodeRunning(sock); !running {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		gl.Close()
		os.Remove(gwSock)
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			c.Close()
		}
	}()

	out, err := runStop(t, sock)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "stopped gateway") {
		t.Fatalf("output = %q", out)
	}
}
