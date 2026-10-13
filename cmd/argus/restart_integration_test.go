package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func buildArgus(t *testing.T, out, version string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", out, ".")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
}

func TestRestartInPlaceKeepsPID(t *testing.T) {
	if testing.Short() {
		t.Skip("builds argus twice")
	}
	root := shortTempDir(t)
	bin := filepath.Join(root, "argus")
	buildArgus(t, bin, "1.0.0")
	sock := filepath.Join(root, "s.sock")

	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ARGUS_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"HOME="+root, "SHELL=/bin/sh",
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"XDG_RUNTIME_DIR="+filepath.Join(root, "run"),
	)
	daemon := exec.Command(bin, "start", "--no-config", "--mode", "node", "--socket", sock)
	var daemonOut syncBuffer
	daemon.Env = env
	daemon.Stderr = &daemonOut
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = daemon.Wait(); close(exited) }()
	t.Cleanup(func() { _ = daemon.Process.Kill(); <-exited })

	before, err := waitForRestart(context.Background(), sock, "", 15*time.Second)
	if err != nil || before.Version != "1.0.0" {
		t.Fatalf("daemon did not start: %+v, %v\n%s", before, err, daemonOut.String())
	}

	buildArgus(t, bin+".new", "2.0.0")
	if err := os.Rename(bin+".new", bin); err != nil {
		t.Fatal(err)
	}
	up := exec.Command(bin, "upgrade", "--no-config", "--no-download", "--socket", sock)
	up.Env = env
	out, err := up.CombinedOutput()
	if err != nil {
		t.Fatalf("argus upgrade --no-download: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Restarted argus 2.0.0!") {
		t.Fatalf("output:\n%s", out)
	}
	after, err := probeServerInfo(sock)
	if err != nil || after.Version != "2.0.0" || after.StartedAt == before.StartedAt {
		t.Fatalf("after restart: %+v, %v (before %+v)\n%s", after, err, before, daemonOut.String())
	}
	select {
	case <-exited:
		t.Fatalf("the daemon process exited; the restart must keep the PID\n%s", daemonOut.String())
	case <-time.After(time.Second):
	}
	again, err := probeServerInfo(sock)
	if err != nil || again.Version != "2.0.0" || again.StartedAt != after.StartedAt {
		t.Fatalf("original process is not serving the new version: %+v, %v (after %+v)\n%s", again, err, after, daemonOut.String())
	}
}
