package main

import (
	"errors"
	"io"
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
		{"gateway", spawnSpec{listenAddr: "127.0.0.1:9", token: "tok"}, "tok", "", "", "tok", "127.0.0.1:9"},
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
	srv, err := startManagedReady(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(srv.Logs().Lines(), func(l string) bool { return strings.Contains(l, "starting argus node") }) {
		t.Errorf("logs missing the startup line: %q", srv.Logs().Lines())
	}
	srv.stop(managedStopTimeout)
	if err := <-srv.Exited(); err != nil {
		t.Fatalf("exit after EOF = %v, want a clean exit", err)
	}
	if _, err := api.Dial(spec.socket); err == nil {
		t.Error("the server still serves after stop")
	}
}

func TestManagedStopKillsAfterTimeout(t *testing.T) {
	sandboxChild(t)
	t.Setenv("ARGUS_TEST_IGNORE_STDIN", "1")
	srv, err := startManaged(testSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	srv.stop(200 * time.Millisecond)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("stop took %v", time.Since(start))
	}
	var exit *exec.ExitError
	if err := <-srv.Exited(); !errors.As(err, &exit) || exit.Exited() {
		t.Fatalf("exit = %v, want killed by a signal", err)
	}
}

func TestManagedStopKillsProcessGroup(t *testing.T) {
	sandboxChild(t)
	t.Setenv("ARGUS_TEST_IGNORE_STDIN", "1")
	pidFile := filepath.Join(t.TempDir(), "gc.pid")
	t.Setenv("ARGUS_TEST_GRANDCHILD_PIDFILE", pidFile)
	srv, err := startManaged(testSpec(t))
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

func TestStartManagedReportsEarlyExit(t *testing.T) {
	sandboxChild(t)
	spec := testSpec(t)
	spec.configArgs = []string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}
	start := time.Now()
	_, err := startManagedReady(spec)
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
	if _, err := startManagedReady(spec); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v, want already running", err)
	}
}

func TestManagedServerReportsUnexpectedExit(t *testing.T) {
	sandboxChild(t)
	spec := testSpec(t)
	srv, err := startManagedReady(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.stop(managedStopTimeout)
	_ = srv.cmd.Process.Kill()
	select {
	case err := <-srv.Exited():
		if err == nil {
			t.Fatal("a killed server must report an error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no exit status")
	}
}

func TestManagedServerNilSafe(t *testing.T) {
	var srv *managedServer
	if srv.Logs() != nil || srv.Exited() != nil {
		t.Fatal("a nil server has no logs and no exit channel")
	}
	srv.stop(time.Second)
}

func TestManagedServerSurvivesClosedOutput(t *testing.T) {
	sandboxChild(t)
	spec := spawnSpec{socket: shortSocket(t), gatewayURL: "ws://127.0.0.1:1", token: "tok", configArgs: []string{"--no-config"}}
	srv, err := startManaged(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.stop(managedStopTimeout)

	// The startup lines and the failing uplink write to the closed pipe before
	// the socket is ready.
	srv.out.Close()
	if err := srv.waitReady(spec.socket); err != nil {
		t.Fatal(err)
	}
	srv.stdin.Close()
	select {
	case err := <-srv.Exited():
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
	setupManagedChild(parent, func() { close(stopped) })
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
