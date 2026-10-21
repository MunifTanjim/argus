package main

import (
	"errors"
	"io"
	"log/slog"
	"net"
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
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/logbuf"
)

// TestMain lets the test binary act as argus, so managed-server tests run
// without a go build.
func TestMain(m *testing.M) {
	switch {
	case os.Getenv("ARGUS_TEST_IGNORE_STDIN") == "1":
		if pidFile := os.Getenv("ARGUS_TEST_GRANDCHILD_PIDFILE"); pidFile != "" {
			gc := exec.Command("sleep", "3600")
			if err := gc.Start(); err != nil {
				os.Exit(1)
			}
			_ = os.WriteFile(pidFile, []byte(strconv.Itoa(gc.Process.Pid)), 0o600)
		}
		time.Sleep(time.Hour)
	case os.Getenv("ARGUS_TEST_LATE_LINE") == "1":
		_, _ = io.Copy(io.Discard, os.Stdin)
		gc := exec.Command("sh", "-c", "sleep 0.3; echo late-line")
		gc.Stdout = os.Stdout
		if err := gc.Start(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case os.Getenv("ARGUS_TEST_RUN_MAIN") == "1":
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// sandboxChild runs managed servers as the test binary, with temp dirs and no
// ARGUS_* settings from the developer's environment.
func sandboxChild(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "ARGUS_") {
			t.Setenv(k, "")
		}
	}
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, filepath.Join(root, k))
	}
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ARGUS_TEST_RUN_MAIN", "1")
}

func testSpec(t *testing.T) spawnSpec {
	return spawnSpec{socket: shortSocket(t), configArgs: []string{"--no-config"}}
}

func TestSpawnArgsKeepTokenOutOfArgv(t *testing.T) {
	s := spawnSpec{socket: "/s", gatewayURL: "wss://g", token: "secret"}
	if slices.ContainsFunc(s.args(), func(a string) bool { return strings.Contains(a, "secret") }) {
		t.Fatalf("token in argv: %v", s.args())
	}
	env := s.env([]string{"PATH=/bin", "ARGUS_TOKEN=old"})
	if !slices.Contains(env, "ARGUS_TOKEN=secret") || slices.Contains(env, "ARGUS_TOKEN=old") {
		t.Fatalf("env = %v, want only ARGUS_TOKEN=secret", env)
	}
}

func TestSpawnArgsOverrideConfigFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "mode: gateway\ntoken: filetok\ntunnel:\n  provider: zrok\ngateway:\n  url: wss://file\n  listen-addr: 127.0.0.1:1\n"
	if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARGUS_CONFIG", "")
	t.Setenv("ARGUS_MODE", "gateway")
	t.Setenv("ARGUS_GATEWAY_URL", "wss://env")
	t.Setenv("ARGUS_TUNNEL_PROVIDER", "ngrok")
	cfgArgs := []string{"--config", file}
	cases := []struct {
		name             string
		spec             spawnSpec
		envToken         string
		mode, url, token string
		listenAddr       string
	}{
		{"isolated", spawnSpec{}, "", "node", "", "", "127.0.0.1:1"},
		{"connected", spawnSpec{gatewayURL: "wss://g", token: "tok"}, "tok", "node", "wss://g", "tok", "127.0.0.1:1"},
		{"connected no token", spawnSpec{gatewayURL: "wss://g"}, "", "node", "wss://g", "", "127.0.0.1:1"},
		{"gateway role", spawnSpec{role: roleGateway, listenAddr: "127.0.0.1:9", token: "tok"}, "tok", "gateway", "", "tok", "127.0.0.1:9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ARGUS_TOKEN", tc.envToken)
			tc.spec.socket = "/tmp/x.sock"
			tc.spec.configArgs = cfgArgs
			cmd, rest, err := newRootCmd("test").Find(tc.spec.args())
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.ParseFlags(rest); err != nil {
				t.Fatalf("parse %v: %v", rest, err)
			}
			cfg, _, err := loadConfig(cmd)
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if cfg.Mode != tc.mode || cfg.Gateway.URL != tc.url || cfg.Token != tc.token || cfg.Gateway.ListenAddr != tc.listenAddr || cfg.Socket != "/tmp/x.sock" {
				t.Errorf("cfg = mode %q url %q token %q listen %q socket %q", cfg.Mode, cfg.Gateway.URL, cfg.Token, cfg.Gateway.ListenAddr, cfg.Socket)
			}
			if cfg.Tunnel.Provider != "" {
				t.Errorf("tunnel provider = %q, want none", cfg.Tunnel.Provider)
			}
			if err := validateMode(cfg); err != nil {
				t.Errorf("validateMode: %v", err)
			}
		})
	}
}

func TestResolveListenAddr(t *testing.T) {
	addr, dial, err := resolveListenAddr("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(addr, ":0") || dial.Port == 0 || addr != dial.String() {
		t.Fatalf("addr %q dial %v: want a concrete port", addr, dial)
	}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if _, _, err := resolveListenAddr(busy.Addr().String()); err == nil {
		t.Fatal("a port in use must fail")
	}
	fixed := freeAddr(t)
	if addr, _, err := resolveListenAddr(fixed); err != nil || addr != fixed {
		t.Fatalf("fixed port: %q, %v; want %q unchanged", addr, err, fixed)
	}
}

func TestManagedServerStopsOnEOF(t *testing.T) {
	sandboxChild(t)
	spec := testSpec(t)
	srv, err := startManagedReady(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(srv.logs.Lines(), func(l string) bool { return strings.Contains(l, "starting argus node") }) {
		t.Errorf("logs missing the startup line: %q", srv.logs.Lines())
	}
	srv.stop(managedStopTimeout)
	if err := srv.wait(); err != nil {
		t.Fatalf("exit after EOF = %v, want a clean exit", err)
	}
	if _, err := api.Dial(spec.socket); err == nil {
		t.Error("the server still serves after stop")
	}
}

func testManagedStopsOnServerStop(t *testing.T, spec spawnSpec) {
	t.Helper()
	srv, err := startManagedReady(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.stop(managedStopTimeout) })
	c, err := api.Dial(spec.readySocket())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call(api.MethodServerStop, nil, nil); err != nil {
		t.Fatalf("server.stop = %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exit after server.stop = %v, want a clean exit", err)
		}
	case <-time.After(managedStopTimeout):
		t.Fatal("server did not exit after server.stop")
	}
	if _, err := api.Dial(spec.readySocket()); err == nil {
		t.Error("the server still serves after server.stop")
	}
}

func TestManagedServerStopsOnServerStop(t *testing.T) {
	sandboxChild(t)
	testManagedStopsOnServerStop(t, testSpec(t))
}

func TestManagedGatewayStopsOnServerStop(t *testing.T) {
	sandboxChild(t)
	addr, _, err := resolveListenAddr("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	spec := testSpec(t)
	spec.role = roleGateway
	spec.listenAddr = addr
	spec.token = "test-token"
	testManagedStopsOnServerStop(t, spec)
}

func TestManagedStopKillsAfterTimeout(t *testing.T) {
	sandboxChild(t)
	t.Setenv("ARGUS_TEST_IGNORE_STDIN", "1")
	srv, err := startManaged(testSpec(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	srv.stop(200 * time.Millisecond)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("stop took %v", time.Since(start))
	}
	var exit *exec.ExitError
	if err := srv.wait(); !errors.As(err, &exit) || exit.Exited() {
		t.Fatalf("exit = %v, want killed by a signal", err)
	}
}

func TestManagedStopKillsProcessGroup(t *testing.T) {
	sandboxChild(t)
	t.Setenv("ARGUS_TEST_IGNORE_STDIN", "1")
	pidFile := filepath.Join(t.TempDir(), "gc.pid")
	t.Setenv("ARGUS_TEST_GRANDCHILD_PIDFILE", pidFile)
	srv, err := startManaged(testSpec(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	for deadline := time.Now().Add(5 * time.Second); pid == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			srv.stop(0)
			t.Fatal("grandchild did not start")
		}
		b, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(string(b))
	}
	srv.stop(200 * time.Millisecond)
	for deadline := time.Now().Add(3 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("grandchild still runs after stop")
		}
	}
}

func TestManagedStopWaitsForOutput(t *testing.T) {
	sandboxChild(t)
	t.Setenv("ARGUS_TEST_LATE_LINE", "1")
	var out syncBuffer
	srv, err := startManaged(testSpec(t), &out)
	if err != nil {
		t.Fatal(err)
	}
	srv.stop(managedStopTimeout)
	if !strings.Contains(out.String(), "late-line") {
		t.Fatalf("out = %q, want the line written after the child exited", out.String())
	}
}

func TestStartManagedReportsEarlyExit(t *testing.T) {
	sandboxChild(t)
	spec := testSpec(t)
	spec.configArgs = []string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}
	start := time.Now()
	_, err := startManagedReady(spec, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "server exited") || !strings.Contains(err.Error(), "missing.yaml") {
		t.Errorf("error must name the exit and the child's message: %v", err)
	}
	if strings.Contains(err.Error(), "\x1b[") {
		t.Errorf("error must not carry color codes: %q", err)
	}
	if time.Since(start) >= managedReadyTimeout {
		t.Error("an early exit must not wait for the ready timeout")
	}
}

func TestStartManagedRefusesServedSocket(t *testing.T) {
	spec := testSpec(t)
	ln, err := net.Listen("unix", spec.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := startManagedReady(spec, nil); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v, want already running", err)
	}
}

func TestManagedServerReportsUnexpectedExit(t *testing.T) {
	sandboxChild(t)
	spec := testSpec(t)
	srv, err := startManagedReady(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.stop(managedStopTimeout)
	_ = srv.cmd.Process.Kill()
	select {
	case <-srv.done:
		err := srv.err
		if err == nil {
			t.Fatal("a killed server must report an error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no exit status")
	}
}

func TestManagedServerNilSafe(t *testing.T) {
	var srv *managedServer
	srv.stop(time.Second)
}

func TestManagedServerSurvivesClosedOutput(t *testing.T) {
	sandboxChild(t)
	spec := spawnSpec{socket: shortSocket(t), gatewayURL: "ws://127.0.0.1:1", token: "tok", configArgs: []string{"--no-config"}}
	srv, err := startManaged(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.stop(managedStopTimeout)

	// The startup lines and the failing uplink write to the closed pipe before
	// the socket is ready.
	srv.out.Close()
	if err := srv.waitReady(spec); err != nil {
		t.Fatal(err)
	}
	srv.stdin.Close()
	select {
	case <-srv.done:
		err := srv.err
		if err != nil {
			t.Fatalf("exit = %v, want a clean exit", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit")
	}
}

// TestSetupManagedChild runs in a subprocess, because setupManagedChild changes
// process-wide signal handling.
func TestSetupManagedChild(t *testing.T) {
	if os.Getenv("ARGUS_TEST_SETUP_MANAGED_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSetupManagedChild$")
		cmd.Env = append(os.Environ(), "ARGUS_TEST_SETUP_MANAGED_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	t.Setenv("ARGUS_TOKEN", "tok")
	parent, w := io.Pipe()
	stopped := make(chan struct{})
	setupManagedChild(parent, func() { close(stopped) }, true)
	w.Close()
	<-stopped
	if v, ok := os.LookupEnv("ARGUS_TOKEN"); ok {
		t.Fatalf("ARGUS_TOKEN still in env: %q", v)
	}
	var stderr strings.Builder
	cmd := exec.Command("sh", "-c", "yes | head -1 >/dev/null")
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "Broken pipe") {
		t.Fatalf("child inherited ignored SIGPIPE: %s", stderr.String())
	}
}

func TestSetupManagedChildWithoutWatch(t *testing.T) {
	if os.Getenv("ARGUS_TEST_SETUP_MANAGED_CHILD_NOWATCH") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSetupManagedChildWithoutWatch$")
		cmd.Env = append(os.Environ(), "ARGUS_TEST_SETUP_MANAGED_CHILD_NOWATCH=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	t.Setenv("ARGUS_TOKEN", "tok")
	stopped := make(chan struct{})
	setupManagedChild(strings.NewReader(""), func() { close(stopped) }, false)
	select {
	case <-stopped:
		t.Fatal("stdin EOF stopped a child that does not watch it")
	case <-time.After(200 * time.Millisecond):
	}
	if v, ok := os.LookupEnv("ARGUS_TOKEN"); ok {
		t.Fatalf("ARGUS_TOKEN still in env: %q", v)
	}
}

func startDetached(t *testing.T, logPath string, extra ...string) (*exec.Cmd, chan error) {
	t.Helper()
	args := append([]string{"start", "--argus-managed", "--argus-log-file=" + logPath}, extra...)
	cmd := exec.Command(os.Args[0], args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return cmd, done
}

func stopDetached(t *testing.T, cmd *exec.Cmd, done chan error, socket string) {
	t.Helper()
	if c, err := api.Dial(socket); err == nil {
		_ = c.Call(api.MethodServerStop, nil, nil)
		c.Close()
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func TestDetachedChildLogsToFile(t *testing.T) {
	sandboxChild(t)
	root := shortTempDir(t)
	sock := filepath.Join(root, "s.sock")
	logPath := filepath.Join(root, "logs", "n.log")
	cmd, done := startDetached(t, logPath, "--socket", sock, "--no-config")
	t.Cleanup(func() { stopDetached(t, cmd, done, sock) })

	waitFor(t, "the detached child to serve", 10*time.Second, func() bool {
		_, err := probeServerInfo(sock)
		return err == nil
	}, func() string { b, _ := os.ReadFile(logPath); return string(b) })
	time.Sleep(500 * time.Millisecond)
	if _, err := probeServerInfo(sock); err != nil {
		t.Fatalf("the child stopped after stdin EOF: %v", err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(b), "starting argus node") {
		t.Fatalf("log = %q, %v", b, err)
	}
}

func TestDetachedChildConfigErrorReachesLog(t *testing.T) {
	sandboxChild(t)
	root := shortTempDir(t)
	cfgPath := filepath.Join(root, "bad.yaml")
	if err := os.WriteFile(cfgPath, []byte("log: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "n.log")
	cmd, done := startDetached(t, logPath, "--socket", filepath.Join(root, "s.sock"), "--config", cfgPath)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want a non-zero exit")
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child did not exit")
	}
	b, _ := os.ReadFile(logPath)
	if !strings.Contains(string(b), "bad.yaml") {
		t.Fatalf("log = %q, want the config error", b)
	}
}

// TestSetupLoggerDropsForcedColorInManagedChild runs in a subprocess, because
// it changes the environment and the global logger.
func TestSetupLoggerDropsForcedColorInManagedChild(t *testing.T) {
	if os.Getenv("ARGUS_TEST_SETUP_LOGGER") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSetupLoggerDropsForcedColorInManagedChild$")
		cmd.Env = append(os.Environ(), "ARGUS_TEST_SETUP_LOGGER=1", "CLICOLOR_FORCE=1", "NO_COLOR=")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if out, err := cmd.Output(); err != nil {
			t.Fatalf("%v\n%s%s", err, out, stderr.String())
		}
		if !strings.Contains(stderr.String(), "\x1b[") {
			t.Fatalf("log output lost its color: %q", stderr.String())
		}
		return
	}
	if !slices.Contains(startEnv, "CLICOLOR_FORCE=1") {
		t.Fatal("a restart must keep CLICOLOR_FORCE")
	}
	cfg := &config.Config{}
	cfg.Log.Level = "info"
	if err := setupLogger(cfg, os.Stderr, true); err != nil {
		t.Fatal(err)
	}
	slog.Info("colored")
	if v, ok := os.LookupEnv("CLICOLOR_FORCE"); ok {
		t.Fatalf("CLICOLOR_FORCE still in env: %q", v)
	}
}

type writeRecorder struct{ writes []string }

func (w *writeRecorder) Write(p []byte) (int, error) {
	w.writes = append(w.writes, string(p))
	return len(p), nil
}

func TestCopyLinesWritesWholeLines(t *testing.T) {
	var rec writeRecorder
	copyLines(&rec, strings.NewReader("one\ntwo\nlast"))
	if want := []string{"one\n", "two\n", "last\n"}; !slices.Equal(rec.writes, want) {
		t.Fatalf("writes = %q, want %q", rec.writes, want)
	}
}

func gatewayTestSpec(t *testing.T) spawnSpec {
	t.Helper()
	return spawnSpec{role: roleGateway, socket: shortSocket(t), listenAddr: freeAddr(t), token: "tok", configArgs: []string{"--no-config"}}
}

func TestSpawnArgsRoles(t *testing.T) {
	gw := spawnSpec{role: roleGateway, socket: "/s", listenAddr: "127.0.0.1:9", token: "t", extraArgs: []string{"--tunnel=zrok"}}
	a := gw.args()
	for _, want := range []string{"--mode=gateway", "--gateway=", "--listen-addr=127.0.0.1:9", "--tunnel=zrok"} {
		if !slices.Contains(a, want) {
			t.Fatalf("gateway args %v lack %q", a, want)
		}
	}
	if slices.Index(a, "--tunnel=") > slices.Index(a, "--tunnel=zrok") {
		t.Fatalf("extra args must come last so that they win: %v", a)
	}
	if got := gw.readySocket(); got != gatewaySocketPath("/s") {
		t.Fatalf("gateway readySocket = %q", got)
	}
	n := spawnSpec{socket: "/s", gatewayURL: "ws://g"}
	if !slices.Contains(n.args(), "--mode=node") || !slices.Contains(n.args(), "--gateway=ws://g") {
		t.Fatalf("node args = %v", n.args())
	}
	if got := n.readySocket(); got != "/s" {
		t.Fatalf("node readySocket = %q", got)
	}
}

// A gateway binds its unix socket before its TCP listener, so the socket alone
// does not mean that a node can uplink yet.
func TestSpawnEnvAddsExtraEnv(t *testing.T) {
	s := spawnSpec{extraEnv: []string{"CLICOLOR_FORCE=1"}}
	if env := s.env([]string{"PATH=/bin"}); !slices.Contains(env, "CLICOLOR_FORCE=1") {
		t.Fatalf("env = %v", env)
	}
}

func TestManagedGroupStartsGatewayThenNode(t *testing.T) {
	sandboxChild(t)
	gw := gatewayTestSpec(t)
	n := spawnSpec{socket: gw.socket, gatewayURL: "ws://" + gw.listenAddr, token: "tok", configArgs: []string{"--no-config"}}
	g, err := startManagedGroup([]spawnSpec{gw, n}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer g.stop(managedStopTimeout)
	for _, sock := range []string{gatewaySocketPath(gw.socket), n.socket} {
		if _, err := probeServerInfo(sock); err != nil {
			t.Fatalf("%s: %v", sock, err)
		}
	}
	waitForLog(t, g.Logs(), "gateway uplink established")
	waitForLog(t, g.Logs(), "gateway listening")
}

func TestManagedGroupStopsStartedChildrenOnFailure(t *testing.T) {
	sandboxChild(t)
	gw := gatewayTestSpec(t)
	bad := spawnSpec{socket: gw.socket, gatewayURL: "ws://" + gw.listenAddr, configArgs: []string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}}
	if _, err := startManagedGroup([]spawnSpec{gw, bad}, nil); err == nil {
		t.Fatal("want a startup error")
	}
	if _, err := api.Dial(gatewaySocketPath(gw.socket)); err == nil {
		t.Fatal("the gateway still serves after the node failed to start")
	}
}

func TestManagedGroupReportsAnyUnexpectedExit(t *testing.T) {
	sandboxChild(t)
	gw := gatewayTestSpec(t)
	n := spawnSpec{socket: gw.socket, gatewayURL: "ws://" + gw.listenAddr, token: "tok", configArgs: []string{"--no-config"}}
	g, err := startManagedGroup([]spawnSpec{gw, n}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer g.stop(managedStopTimeout)
	_ = g.servers[0].cmd.Process.Kill()
	select {
	case err := <-g.Exited():
		if err == nil {
			t.Fatal("a killed gateway reported a clean exit")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the gateway exit did not reach the group")
	}
}

func TestManagedGroupStopIsNotAnExit(t *testing.T) {
	sandboxChild(t)
	g, err := startManagedGroup([]spawnSpec{testSpec(t)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.stop(managedStopTimeout)
	select {
	case err := <-g.Exited():
		t.Fatalf("a requested stop reported an exit: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestManagedGroupStopsNodeFirst(t *testing.T) {
	sandboxChild(t)
	gw := gatewayTestSpec(t)
	n := spawnSpec{socket: gw.socket, gatewayURL: "ws://" + gw.listenAddr, token: "tok", configArgs: []string{"--no-config"}}
	g, err := startManagedGroup([]spawnSpec{gw, n}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForLog(t, g.Logs(), "gateway uplink established")
	g.stop(managedStopTimeout)
	for _, l := range g.Logs().Lines() {
		if strings.Contains(l, "gateway uplink closed") {
			t.Fatalf("the gateway stopped before the node:\n%s", strings.Join(g.Logs().Lines(), "\n"))
		}
	}
}

func TestManagedGroupCopiesLinesToOut(t *testing.T) {
	sandboxChild(t)
	var out syncBuffer
	g, err := startManagedGroup([]spawnSpec{testSpec(t)}, &out)
	if err != nil {
		t.Fatal(err)
	}
	g.stop(managedStopTimeout)
	if !strings.Contains(out.String(), "starting argus node") {
		t.Fatalf("out = %q", out.String())
	}
	if g.Logs() != nil {
		t.Fatal("a group with out must not keep the lines")
	}
}

func TestManagedGroupRefusesServedGatewaySocket(t *testing.T) {
	sandboxChild(t)
	gw := gatewayTestSpec(t)
	if err := os.MkdirAll(filepath.Dir(gw.socket), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", gatewaySocketPath(gw.socket))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := startManagedGroup([]spawnSpec{gw}, nil); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v, want already running", err)
	}
}

func TestManagedGroupNilSafe(t *testing.T) {
	var g *managedGroup
	if g.Logs() != nil || g.Exited() != nil {
		t.Fatal("nil group must return nil")
	}
	g.stop(time.Second)
}

func waitForLog(t *testing.T, b *logbuf.Buffer, substr string) {
	t.Helper()
	waitFor(t, "a log line with "+strconv.Quote(substr), 10*time.Second, func() bool {
		return slices.ContainsFunc(b.Lines(), func(l string) bool { return strings.Contains(l, substr) })
	}, func() string { return strings.Join(b.Lines(), "\n") })
}

func TestTUISpecForcesPrettyColorLogs(t *testing.T) {
	cmd := newRootCmd("test")
	cfg := &config.Config{}
	cfg.Log.Format = "json"
	cfg.Log.Level = "debug"
	s := tuiSpec(cmd, cfg)
	a := s.args()
	if !slices.Contains(a, "--log-format=pretty") || !slices.Contains(a, "--log-level=debug") {
		t.Fatalf("args = %v", a)
	}
	if !slices.Contains(s.env(nil), "CLICOLOR_FORCE=1") {
		t.Fatalf("env = %v", s.env(nil))
	}
}

func TestSpawnArgsHaveNoCombinedForm(t *testing.T) {
	s := spawnSpec{socket: "/s", listenAddr: "127.0.0.1:9"}
	if slices.Contains(s.args(), "--mode=") {
		t.Fatalf("a node spec gave the combined form: %v", s.args())
	}
}
