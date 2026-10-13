package main

import (
	"context"
	"os"
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
	srv, err := startManagedReady(spec)
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
		t.Fatalf("after: %+v, %v\n%s", after, err, strings.Join(srv.Logs().Lines(), "\n"))
	}
	select {
	case err := <-srv.Exited():
		t.Fatalf("the managed process exited on restart: %v", err)
	default:
	}
	starts := 0
	for _, l := range srv.Logs().Lines() {
		if strings.Contains(l, "starting argus node") {
			starts++
		}
	}
	if starts != 2 {
		t.Errorf("want startup lines from both binaries in one buffer, got %d:\n%s", starts, strings.Join(srv.Logs().Lines(), "\n"))
	}

	srv.stop(managedStopTimeout)
	if err := <-srv.Exited(); err != nil {
		t.Fatalf("after a restart, EOF must still stop the server cleanly: %v", err)
	}
}
