package e2elive

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestIsolatedEnvCreatesDirsAndVars(t *testing.T) {
	dir := t.TempDir()
	env, err := isolatedEnv(dir)
	if err != nil {
		t.Fatalf("isolatedEnv: %v", err)
	}

	for _, sub := range []string{"config", "state", "cache", "run"} {
		p := filepath.Join(dir, sub)
		info, statErr := os.Stat(p)
		if statErr != nil || !info.IsDir() {
			t.Fatalf("expected dir %s: err=%v", p, statErr)
		}
	}

	want := map[string]string{
		"HOME":            dir,
		"XDG_CONFIG_HOME": filepath.Join(dir, "config"),
		"XDG_STATE_HOME":  filepath.Join(dir, "state"),
		"XDG_CACHE_HOME":  filepath.Join(dir, "cache"),
		"XDG_RUNTIME_DIR": filepath.Join(dir, "run"),
	}
	got := map[string]string{}
	var keys []string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
		keys = append(keys, k)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env %s = %q, want %q", k, got[k], v)
		}
	}
	if got["PATH"] == "" {
		t.Errorf("PATH not inherited")
	}
	sort.Strings(keys)
	if len(keys) != 6 {
		t.Errorf("env has %d vars (%v), want exactly 6", len(keys), keys)
	}
}

func TestFreePortIsDialable(t *testing.T) {
	addr := freePort(t)
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("addr = %q, want 127.0.0.1:<port>", addr)
	}
}
