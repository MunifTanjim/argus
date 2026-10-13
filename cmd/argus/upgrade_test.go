package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestPlanUpgrade(t *testing.T) {
	cases := []struct {
		name                        string
		noRestart, noDownload, live bool
		want                        upgradeSteps
		err                         string
	}{
		{name: "default with daemon", live: true, want: upgradeSteps{download: true, restart: true}},
		{name: "default without daemon", want: upgradeSteps{download: true}},
		{name: "no-restart with daemon", noRestart: true, live: true, want: upgradeSteps{download: true}},
		{name: "no-download with daemon", noDownload: true, live: true, want: upgradeSteps{restart: true}},
		{name: "no-download without daemon", noDownload: true, err: "no running argus process"},
		{name: "both flags", noRestart: true, noDownload: true, live: true, err: "nothing to do"},
	}
	for _, c := range cases {
		got, err := planUpgrade(c.noRestart, c.noDownload, c.live)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: err %v, want %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
}

// serveOldDaemon mimics a daemon from before in-place upgrade.
func serveOldDaemon(t *testing.T, sock string) {
	t.Helper()
	l, err := api.ListenUnix(sock, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	srv := api.NewServer()
	srv.Handle(api.MethodServerInfo, func(context.Context, json.RawMessage) (any, error) {
		return api.ServerInfo{Version: "0.1.0"}, nil
	})
	go func() { _ = srv.Serve(l) }()
}

func oldDaemon(t *testing.T) (string, *api.Client) {
	t.Helper()
	sock := shortSocket(t)
	serveOldDaemon(t, sock)
	c, err := api.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return sock, c
}

func TestRunningDaemons(t *testing.T) {
	cases := []struct {
		name          string
		node, gateway bool
	}{
		{name: "none"},
		{name: "node only", node: true},
		{name: "gateway only", gateway: true},
		{name: "both", node: true, gateway: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodeSock := filepath.Join(shortTempDir(t), "argus.sock")
			var want []string
			if c.node {
				serveOldDaemon(t, nodeSock)
				want = append(want, nodeSock)
			}
			if c.gateway {
				serveOldDaemon(t, gatewaySocketPath(nodeSock))
				want = append(want, gatewaySocketPath(nodeSock))
			}
			daemons, err := runningDaemons(nodeSock)
			if err != nil {
				t.Fatalf("runningDaemons: %v", err)
			}
			var got []string
			for _, d := range daemons {
				got = append(got, d.socket)
				d.client.Close()
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func TestUpgradeDaemonFallsBackForOldDaemon(t *testing.T) {
	sock, c := oldDaemon(t)
	var inProcess bool
	upgradeInProcessFn = func(context.Context, *cobra.Command) error { inProcess = true; return nil }
	t.Cleanup(func() { upgradeInProcessFn = upgradeInProcess })
	err := upgradeDaemon(context.Background(), &cobra.Command{}, daemonConn{socket: sock, client: c}, upgradeSteps{download: true, restart: true}, "")
	if err != nil {
		t.Fatalf("upgradeDaemon: %v", err)
	}
	if !inProcess {
		t.Fatal("an old daemon must fall back to the in-process upgrade")
	}
}

func TestRestartOldDaemonFails(t *testing.T) {
	sock, c := oldDaemon(t)
	if err := restartDaemon(context.Background(), c, sock, ""); !errors.Is(err, errNoInPlaceRestart) {
		t.Fatalf("err = %v, want errNoInPlaceRestart", err)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() { os.Stderr = orig }()
	fn()
	w.Close()
	return <-done
}

func stubInProcess(t *testing.T) *bool {
	t.Helper()
	var called bool
	upgradeInProcessFn = func(context.Context, *cobra.Command) error { called = true; return nil }
	t.Cleanup(func() { upgradeInProcessFn = upgradeInProcess })
	return &called
}

func runUpgradeCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd("test")
	root.SetArgs(append([]string{"upgrade"}, args...))
	var err error
	out := captureStderr(t, func() { err = root.Execute() })
	return out, err
}

func TestUpgradeWithoutDaemonDownloadsInProcess(t *testing.T) {
	called := stubInProcess(t)
	sock := filepath.Join(shortTempDir(t), "argus.sock")
	if out, err := runUpgradeCLI(t, "--no-config", "--socket", sock); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if !*called {
		t.Fatal("upgrade without a daemon must download in-process")
	}
}

func TestUpgradeIgnoresInvalidConfig(t *testing.T) {
	called := stubInProcess(t)
	dir := shortTempDir(t)
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("tui:\n  mouse: maybe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runUpgradeCLI(t, "--config", cfg, "--socket", filepath.Join(dir, "argus.sock"))
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if !*called {
		t.Fatal("an invalid config must not block the upgrade")
	}
	if !strings.Contains(out, "Warning:") {
		t.Fatalf("missing config warning in %q", out)
	}
}

func TestUpgradeOutputPrefixesDaemonKind(t *testing.T) {
	stubInProcess(t)
	sock := filepath.Join(shortTempDir(t), "argus.sock")
	serveOldDaemon(t, sock)
	single, err := runUpgradeCLI(t, "--no-config", "--socket", sock)
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, single)
	}
	if !strings.HasPrefix(single, "Upgrading the running argus...\n") {
		t.Fatalf("single daemon output %q must not be prefixed", single)
	}
	serveOldDaemon(t, gatewaySocketPath(sock))
	both, err := runUpgradeCLI(t, "--no-config", "--socket", sock)
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, both)
	}
	for _, want := range []string{
		"node: Upgrading the running argus...\n",
		"node: The running argus cannot restart itself. Restart it to use the new version.\n",
		"gateway: Upgrading the running argus...\n",
	} {
		if !strings.Contains(both, want) {
			t.Fatalf("output %q lacks %q", both, want)
		}
	}
}

func TestWaitForRestartTimesOut(t *testing.T) {
	sock, _ := oldDaemon(t)
	_, err := waitForRestart(context.Background(), sock, "", 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not come back") {
		t.Fatalf("err = %v", err)
	}
}
