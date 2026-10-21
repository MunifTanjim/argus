package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// A managed server restarts in place: same process, new binary, still owned by
// the stdin pipe, and still writing to the same log buffer.
func TestManagedServerRestartsInPlace(t *testing.T) {
	if testing.Short() {
		t.Skip("builds argus twice")
	}
	sandboxChild(t)
	t.Setenv("ARGUS_TEST_RUN_MAIN", "")
	root := shortTempDir(t)
	bin := filepath.Join(root, "argus")
	buildArgus(t, bin, "1.0.0")
	managedBin = func() (string, error) { return bin, nil }
	t.Cleanup(func() { managedBin = os.Executable })

	spec := spawnSpec{socket: filepath.Join(root, "s.sock"), configArgs: []string{"--no-config"}}
	srv, err := startManagedReady(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.stop(managedStopTimeout)
	before, err := probeServerInfo(spec.socket)
	if err != nil || before.Version != "1.0.0" {
		t.Fatalf("before: %+v, %v", before, err)
	}

	buildArgus(t, bin+".new", "2.0.0")
	if err := os.Rename(bin+".new", bin); err != nil {
		t.Fatal(err)
	}
	c, err := api.Dial(spec.socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Call(api.MethodServerRestart, nil, nil); err != nil {
		t.Fatalf("server.restart: %v", err)
	}
	c.Close()
	after, err := waitForRestart(context.Background(), spec.socket, before.StartedAt, 30*time.Second)
	if err != nil || after.Version != "2.0.0" {
		t.Fatalf("after: %+v, %v\n%s", after, err, strings.Join(srv.logs.Lines(), "\n"))
	}
	select {
	case <-srv.done:
		err := srv.err
		t.Fatalf("the managed process exited on restart: %v", err)
	default:
	}
	starts := 0
	for _, l := range srv.logs.Lines() {
		if strings.Contains(l, "starting argus node") {
			starts++
		}
	}
	if starts != 2 {
		t.Errorf("want startup lines from both binaries in one buffer, got %d:\n%s", starts, strings.Join(srv.logs.Lines(), "\n"))
	}

	srv.stop(managedStopTimeout)
	if err := srv.wait(); err != nil {
		t.Fatalf("after a restart, EOF must still stop the server cleanly: %v", err)
	}
}

func TestDetachedChildRestartRotatesLog(t *testing.T) {
	if testing.Short() {
		t.Skip("builds argus twice")
	}
	sandboxChild(t)
	t.Setenv("ARGUS_TEST_RUN_MAIN", "")
	root := shortTempDir(t)
	bin := filepath.Join(root, "argus")
	buildArgus(t, bin, "1.0.0")
	sock := filepath.Join(root, "s.sock")
	logPath := filepath.Join(root, "n.log")

	cmd := exec.Command(bin, "start", "--argus-managed", "--argus-log-file="+logPath, "--no-config", "--mode", "node", "--socket", sock)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { stopDetached(t, cmd, done, sock) })

	var before api.ServerInfo
	waitFor(t, "the 1.0.0 child to serve", 20*time.Second, func() bool {
		var err error
		before, err = probeServerInfo(sock)
		return err == nil
	}, func() string { b, _ := os.ReadFile(logPath); return string(b) })

	buildArgus(t, bin+".new", "2.0.0")
	if err := os.Rename(bin+".new", bin); err != nil {
		t.Fatal(err)
	}
	c, err := api.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Call(api.MethodServerRestart, nil, nil); err != nil {
		t.Fatalf("server.restart: %v", err)
	}
	c.Close()
	after, err := waitForRestart(context.Background(), sock, before.StartedAt, 30*time.Second)
	if err != nil || after.Version != "2.0.0" {
		t.Fatalf("after: %+v, %v", after, err)
	}
	prev, _ := os.ReadFile(logPath + ".1")
	cur, _ := os.ReadFile(logPath)
	if !strings.Contains(string(prev), "starting argus node") || !strings.Contains(string(prev), "1.0.0") {
		t.Errorf("%s.1 = %q, want the 1.0.0 startup line", logPath, prev)
	}
	if !strings.Contains(string(cur), "starting argus node") || !strings.Contains(string(cur), "2.0.0") || strings.Contains(string(cur), "1.0.0") {
		t.Errorf("%s = %q, want only the 2.0.0 startup line", logPath, cur)
	}
}
