package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

func sandboxEnv(root string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ARGUS_") {
			env = append(env, kv)
		}
	}
	return append(env,
		"HOME="+root, "SHELL=/bin/sh",
		"TMUX_TMPDIR="+filepath.Join(root, "tmux"),
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"XDG_RUNTIME_DIR="+filepath.Join(root, "run"),
		"CODEX_HOME="+filepath.Join(root, "codex"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(root, "claude"),
	)
}

func childPIDs(t *testing.T, parent int) []int {
	t.Helper()
	out, _ := exec.Command("pgrep", "-P", strconv.Itoa(parent)).Output()
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if p, err := strconv.Atoi(f); err == nil {
			pids = append(pids, p)
		}
	}
	return pids
}

func gatewayNodeCount(addr string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := dialGatewayClient(ctx, "ws://"+addr, "tok")
	if err != nil {
		return -1
	}
	defer c.Close()
	var info api.ServerInfo
	if err := c.Call(api.MethodServerInfo, nil, &info); err != nil {
		return -1
	}
	return len(info.Nodes)
}

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool, logs func() string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s\n%s", what, logs())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// argus start in combined mode runs the gateway and the node as two children,
// and each one restarts in place without an effect on the other.
func TestSupervisorRestartsEachChildAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("builds argus")
	}
	root := shortTempDir(t)
	bin := filepath.Join(root, "argus")
	buildArgus(t, bin, "1.0.0")
	sock := filepath.Join(root, "s.sock")
	addr := freeAddr(t)

	sup := exec.Command(bin, "start", "--no-config", "--socket", sock, "--listen-addr", addr, "--log-format", "json")
	sup.Env = append(sandboxEnv(root), "ARGUS_TOKEN=tok")
	var out syncBuffer
	sup.Stderr = &out
	if err := sup.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- sup.Wait() }()
	t.Cleanup(func() { _ = sup.Process.Kill() })

	gwSock := gatewaySocketPath(sock)
	nodeBefore, err := waitForRestart(context.Background(), sock, "", 15*time.Second)
	if err != nil {
		t.Fatalf("node did not start: %v\n%s", err, out.String())
	}
	gwBefore, err := probeServerInfo(gwSock)
	if err != nil {
		t.Fatalf("gateway did not start: %v\n%s", err, out.String())
	}
	waitFor(t, "the node on the gateway", 10*time.Second, func() bool { return gatewayNodeCount(addr) == 1 }, out.String)
	pids := childPIDs(t, sup.Process.Pid)
	if len(pids) != 2 {
		t.Fatalf("want 2 children, got %v\n%s", pids, out.String())
	}
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l != "" && !strings.HasPrefix(l, "{") {
			t.Fatalf("--log-format json gave a non-JSON line: %q", l)
		}
	}

	restart := func(sock string) {
		c, err := api.Dial(sock)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.Call(api.MethodServerRestart, nil, nil); err != nil {
			t.Fatalf("server.restart on %s: %v", sock, err)
		}
	}

	restart(sock)
	if _, err := waitForRestart(context.Background(), sock, nodeBefore.StartedAt, 30*time.Second); err != nil {
		t.Fatalf("node restart: %v\n%s", err, out.String())
	}
	if gw, err := probeServerInfo(gwSock); err != nil || gw.StartedAt != gwBefore.StartedAt {
		t.Fatalf("the node restart touched the gateway: %+v, %v (before %+v)", gw, err, gwBefore)
	}
	waitFor(t, "the node back on the gateway", 10*time.Second, func() bool { return gatewayNodeCount(addr) == 1 }, out.String)

	nodeMid, _ := probeServerInfo(sock)
	restart(gwSock)
	if _, err := waitForRestart(context.Background(), gwSock, gwBefore.StartedAt, 30*time.Second); err != nil {
		t.Fatalf("gateway restart: %v\n%s", err, out.String())
	}
	if n, err := probeServerInfo(sock); err != nil || n.StartedAt != nodeMid.StartedAt {
		t.Fatalf("the gateway restart touched the node: %+v, %v (before %+v)", n, err, nodeMid)
	}
	waitFor(t, "the node to reconnect", 20*time.Second, func() bool { return gatewayNodeCount(addr) == 1 }, out.String)
	if got := childPIDs(t, sup.Process.Pid); !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(pids))) {
		t.Fatalf("children changed: %v, want %v", got, pids)
	}

	// systemd's KillMode=control-group signals the children too.
	for _, pid := range append(pids, sup.Process.Pid) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("supervisor exit = %v, want 0 on SIGTERM\n%s", err, out.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("supervisor did not exit on SIGTERM")
	}
	for _, s := range []string{sock, gwSock} {
		if _, err := api.Dial(s); err == nil {
			t.Fatalf("%s still serves after the supervisor exited", s)
		}
	}
}

func TestSupervisorKilledStopsChildren(t *testing.T) {
	if testing.Short() {
		t.Skip("builds argus")
	}
	root := shortTempDir(t)
	bin := filepath.Join(root, "argus")
	buildArgus(t, bin, "1.0.0")
	sock := filepath.Join(root, "s.sock")
	gwSock := gatewaySocketPath(sock)

	sup := exec.Command(bin, "start", "--no-config", "--socket", sock, "--listen-addr", freeAddr(t), "--log-format", "json")
	sup.Env = append(sandboxEnv(root), "ARGUS_TOKEN=tok")
	var out syncBuffer
	sup.Stderr = &out
	if err := sup.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- sup.Wait() }()
	t.Cleanup(func() { _ = sup.Process.Kill() })

	waitFor(t, "both sockets", 15*time.Second, func() bool {
		_, e1 := probeServerInfo(sock)
		_, e2 := probeServerInfo(gwSock)
		return e1 == nil && e2 == nil
	}, out.String)
	pids := childPIDs(t, sup.Process.Pid)
	if len(pids) != 2 {
		t.Fatalf("want 2 children, got %v\n%s", pids, out.String())
	}
	t.Cleanup(func() {
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	_ = sup.Process.Kill()
	<-exited
	waitFor(t, "the children to exit", 10*time.Second, func() bool {
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil {
				return false
			}
		}
		return true
	}, out.String)
	for _, s := range []string{sock, gwSock} {
		if _, err := api.Dial(s); err == nil {
			t.Fatalf("%s still serves after the supervisor was killed", s)
		}
	}
}

func TestSupervisorExitsCleanlyOnServerStop(t *testing.T) {
	if testing.Short() {
		t.Skip("builds argus")
	}
	root := shortTempDir(t)
	bin := filepath.Join(root, "argus")
	buildArgus(t, bin, "1.0.0")
	sock := filepath.Join(root, "s.sock")
	gwSock := gatewaySocketPath(sock)

	sup := exec.Command(bin, "start", "--no-config", "--socket", sock, "--listen-addr", freeAddr(t), "--log-format", "json")
	sup.Env = append(sandboxEnv(root), "ARGUS_TOKEN=tok")
	var out syncBuffer
	sup.Stderr = &out
	if err := sup.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- sup.Wait() }()
	t.Cleanup(func() { _ = sup.Process.Kill() })

	waitFor(t, "both sockets", 15*time.Second, func() bool {
		_, e1 := probeServerInfo(sock)
		_, e2 := probeServerInfo(gwSock)
		return e1 == nil && e2 == nil
	}, out.String)

	c, err := api.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Call(api.MethodServerStop, nil, nil); err != nil {
		t.Fatal(err)
	}
	c.Close()

	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("supervisor exit = %v, want status 0\n%s", err, out.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("supervisor did not exit after server.stop\n%s", out.String())
	}
	for _, s := range []string{sock, gwSock} {
		if _, err := api.Dial(s); err == nil {
			t.Fatalf("%s still serves after the supervisor exited", s)
		}
	}
}

// A combined child from an older TUI runs the new binary after an in-place
// upgrade. It must supervise two children and still stop on stdin EOF.
func TestManagedCombinedChildSupervises(t *testing.T) {
	sandboxChild(t)
	sock := shortSocket(t)
	addr := freeAddr(t)
	cmd := exec.Command(os.Args[0], "start", "--argus-managed", "--socket", sock, "--tunnel=", "--no-config", "--mode=", "--gateway=", "--listen-addr="+addr)
	cmd.Env = append(os.Environ(), "ARGUS_TOKEN=tok")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitFor(t, "both sockets", 15*time.Second, func() bool {
		_, e1 := probeServerInfo(sock)
		_, e2 := probeServerInfo(gatewaySocketPath(sock))
		return e1 == nil && e2 == nil
	}, out.String)
	if n := len(childPIDs(t, cmd.Process.Pid)); n != 2 {
		t.Fatalf("want 2 children, got %d\n%s", n, out.String())
	}
	stdin.Close()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the combined child did not stop on EOF")
	}
	if _, err := api.Dial(sock); err == nil {
		t.Fatal("the node still serves after EOF")
	}
}
