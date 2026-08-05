package e2elive

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// containerEnv creates the per-process directories on the host side of the bind
// mount and returns the environment naming them by their container paths.
func containerEnv(hostDir string) ([]string, error) {
	for _, sub := range []string{"config", "state", "cache", "run"} {
		if err := os.MkdirAll(filepath.Join(hostDir, sub), 0o700); err != nil {
			return nil, err
		}
	}
	return []string{
		"HOME=" + containerHome,
		"XDG_CONFIG_HOME=" + containerHome + "/config",
		"XDG_STATE_HOME=" + containerHome + "/state",
		"XDG_CACHE_HOME=" + containerHome + "/cache",
		"XDG_RUNTIME_DIR=" + containerHome + "/run",
	}, nil
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

// waitFor polls until cond holds. The deadline is generous because a container
// start is far slower than a process start.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
