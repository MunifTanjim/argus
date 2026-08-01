package e2elive

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func isolatedEnv(dir string) ([]string, error) {
	subs := map[string]string{
		"config": filepath.Join(dir, "config"),
		"state":  filepath.Join(dir, "state"),
		"cache":  filepath.Join(dir, "cache"),
		"run":    filepath.Join(dir, "run"),
	}
	for _, p := range subs {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return nil, err
		}
	}
	return []string{
		"HOME=" + dir,
		"XDG_CONFIG_HOME=" + subs["config"],
		"XDG_STATE_HOME=" + subs["state"],
		"XDG_CACHE_HOME=" + subs["cache"],
		"XDG_RUNTIME_DIR=" + subs["run"],
		"PATH=" + os.Getenv("PATH"),
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

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
