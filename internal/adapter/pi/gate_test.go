package pi

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/config"
)

var testToken = strings.Repeat("a", 64)

func mcpOpts() adapter.InstallOptions {
	return adapter.InstallOptions{Socket: "/s", MCPURL: "http://127.0.0.1:1/mcp", MCPToken: testToken}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func useCacheDir(t *testing.T) {
	t.Helper()
	old := config.CacheDir
	config.CacheDir = t.TempDir()
	t.Cleanup(func() { config.CacheDir = old })
}

func useMCP(t *testing.T, opts adapter.InstallOptions) {
	t.Helper()
	New().(adapter.MCPUser).UseMCP(opts)
	t.Cleanup(func() { New().(adapter.MCPUser).UseMCP(adapter.InstallOptions{}) })
}

func TestStampGateRoundTrip(t *testing.T) {
	for _, st := range []gateStamp{{socket: "/s", mcpURL: "http://127.0.0.1:1/mcp/pi", mcpToken: testToken}, {}} {
		if got, ours := stampedGate(stampGate(st)); !ours || got != st {
			t.Fatalf("stampedGate(stampGate(%+v)) = %+v, %v", st, got, ours)
		}
	}
	if got, ours := stampedGate(gateSource); !ours || got != (gateStamp{}) {
		t.Fatalf("embedded source = %+v, %v", got, ours)
	}
}

func TestReconcileStampsMCP(t *testing.T) {
	path := writeInstalledGate(t, gateSource)
	if _, err := New().Reconcile(mcpOpts()); err != nil {
		t.Fatal(err)
	}
	st, _ := stampedGate(mustRead(t, path))
	if st != (gateStamp{socket: "/s", mcpURL: "http://127.0.0.1:1/mcp/pi", mcpToken: testToken}) {
		t.Fatalf("stamp = %+v", st)
	}
	if m := fileMode(t, path); m != 0o600 {
		t.Fatalf("mode = %v, want 0600", m)
	}
}

func TestReconcileMCPOffEmptiesStamp(t *testing.T) {
	adapter.NoLiveMCPEndpointForTest(t)
	path := writeInstalledGate(t, gateSource)
	a := New()
	if _, err := a.Reconcile(mcpOpts()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reconcile(adapter.InstallOptions{MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	if st, _ := stampedGate(mustRead(t, path)); st != (gateStamp{socket: "/s"}) {
		t.Fatalf("stamp = %+v, want the socket only", st)
	}
}

func TestReconcileMCPOffKeepsLiveStamp(t *testing.T) {
	t.Cleanup(adapter.SetMCPDialerForTest(func(string, time.Duration) (net.Conn, error) {
		c, s := net.Pipe()
		s.Close()
		return c, nil
	}))
	path := writeInstalledGate(t, gateSource)
	a := New()
	if _, err := a.Reconcile(mcpOpts()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reconcile(adapter.InstallOptions{MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	if st, _ := stampedGate(mustRead(t, path)); st.mcpToken != testToken {
		t.Fatalf("stamp = %+v, want the MCP stamp kept", st)
	}
}

func TestReconcileRestrictsGateWithToken(t *testing.T) {
	path := writeInstalledGate(t, stampGate(gateStamp{socket: "/s"}))
	if m := fileMode(t, path); m != 0o644 {
		t.Fatalf("setup mode = %v", m)
	}
	if _, err := New().Reconcile(mcpOpts()); err != nil {
		t.Fatal(err)
	}
	if m := fileMode(t, path); m != 0o600 {
		t.Fatalf("mode = %v, want 0600", m)
	}
}

func TestStagedGateCarriesMCPWithoutGlobalGate(t *testing.T) {
	useCacheDir(t)
	writeInstalledGate(t, "// mine")
	useMCP(t, mcpOpts())
	path, err := stageGate()
	if err != nil {
		t.Fatal(err)
	}
	st, ours := stampedGate(mustRead(t, path))
	if !ours || st != (gateStamp{mcpURL: "http://127.0.0.1:1/mcp/pi", mcpToken: testToken}) {
		t.Fatalf("staged stamp = %+v, %v", st, ours)
	}
	if m := fileMode(t, path); m != 0o600 {
		t.Fatalf("mode = %v, want 0600", m)
	}
	if filepath.Dir(path) != config.GetCachePath("pi") {
		t.Fatalf("staged at %s", path)
	}
}

func TestStagedGateEmptyWhenMCPOff(t *testing.T) {
	useCacheDir(t)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(t.TempDir(), "agent"))
	useMCP(t, adapter.InstallOptions{MCPOff: true, MCPURL: "http://127.0.0.1:1/mcp", MCPToken: testToken})
	path, err := stageGate()
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := stampedGate(mustRead(t, path)); st != (gateStamp{}) {
		t.Fatalf("staged stamp = %+v, want empty", st)
	}
}

func TestUseMCPUpdatesTheGlobalGate(t *testing.T) {
	path := writeInstalledGate(t, gateMarker+"\nconst SOCKET_PATH = \"/s\";\n// older body\n")
	useMCP(t, mcpOpts())
	st, _ := stampedGate(mustRead(t, path))
	if st.mcpToken != testToken || st.mcpURL != "http://127.0.0.1:1/mcp/pi" {
		t.Fatalf("global stamp = %+v, want the MCP values", st)
	}
}

func TestUseMCPLeavesAMissingPiHomeAlone(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent", "agent")
	t.Setenv("PI_CODING_AGENT_DIR", home)
	useMCP(t, mcpOpts())
	if _, err := os.Stat(filepath.Dir(home)); !os.IsNotExist(err) {
		t.Fatalf("pi home parent created: %v", err)
	}
}

func TestManagedEnvCarriesMCPURL(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(t.TempDir(), "agent"))
	useMCP(t, mcpOpts())
	env := managedEnv()
	want := map[string]bool{"ARGUS_MANAGED=1": true, "ARGUS_MCP_URL=http://127.0.0.1:1/mcp/pi": true}
	for _, kv := range env {
		delete(want, kv)
	}
	if len(want) > 0 {
		t.Fatalf("env = %v, missing %v", env, want)
	}
}

func TestUninstallRemovesStagedGates(t *testing.T) {
	useCacheDir(t)
	writeInstalledGate(t, gateSource)
	useMCP(t, mcpOpts())
	staged, err := stageGate()
	if err != nil {
		t.Fatal(err)
	}
	if err := New().Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged gate kept: %v", err)
	}
}
