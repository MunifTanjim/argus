package e2elive

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestContainerEnvCreatesHostDirsAndContainerVars(t *testing.T) {
	dir := t.TempDir()
	env, err := containerEnv(dir)
	if err != nil {
		t.Fatalf("containerEnv: %v", err)
	}

	for _, sub := range []string{"config", "state", "cache", "run"} {
		p := filepath.Join(dir, sub)
		info, statErr := os.Stat(p)
		if statErr != nil || !info.IsDir() {
			t.Fatalf("expected dir %s: err=%v", p, statErr)
		}
	}

	want := map[string]string{
		"HOME":            containerHome,
		"XDG_CONFIG_HOME": containerHome + "/config",
		"XDG_STATE_HOME":  containerHome + "/state",
		"XDG_CACHE_HOME":  containerHome + "/cache",
		"XDG_RUNTIME_DIR": containerHome + "/run",
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
	sort.Strings(keys)
	if len(keys) != len(want) {
		t.Errorf("env has %d vars (%v), want exactly %d", len(keys), keys, len(want))
	}
}

func TestFreePortIsDialable(t *testing.T) {
	addr := freePort(t)
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("addr = %q, want 127.0.0.1:<port>", addr)
	}
}
