package codex

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/registry"
)

const testMCPURL = "http://127.0.0.1:7777/mcp"

func mcpOpts(token string) adapter.InstallOptions {
	return adapter.InstallOptions{ArgusBin: "/bin/argus", MCPURL: testMCPURL, MCPToken: token}
}

func codexConfig(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const argusTable = `[mcp_servers.argus]
url = "http://127.0.0.1:7777/mcp/codex"
http_headers = { Authorization = "Bearer tok1" }
default_tools_approval_mode = "approve"
`

const otherConfig = `# user config
model = "gpt-5" # inline comment

[mcp_servers.other]
command = "other-mcp"
args = ["--flag"]

  # comment before a sub-table
[mcp_servers.other.env]
KEY = "value"

[profiles.fast]
model = "gpt-5-mini"
`

func TestCodexInstallWritesArgusServer(t *testing.T) {
	path := codexConfig(t, "")
	if err := New().Install(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if got != argusTable {
		t.Fatalf("config.toml =\n%s\nwant\n%s", got, argusTable)
	}
	var cfg struct {
		MCPServers map[string]struct {
			URL         string            `toml:"url"`
			HTTPHeaders map[string]string `toml:"http_headers"`
			Approval    string            `toml:"default_tools_approval_mode"`
		} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatal(err)
	}
	s := cfg.MCPServers["argus"]
	if s.URL != testMCPURL+"/codex" || s.HTTPHeaders["Authorization"] != "Bearer tok1" || s.Approval != "approve" {
		t.Fatalf("argus server = %+v", s)
	}
}

func TestCodexReconcileUpdatesTokenOnly(t *testing.T) {
	orig := "# top\nmodel = \"gpt-5\"\n\n" + argusTable + "\n# about other\n[mcp_servers.other]\nurl = \"http://x\"\n"
	path := codexConfig(t, orig)
	added, err := New().Reconcile(mcpOpts("tok2"))
	if err != nil || len(added) == 0 {
		t.Fatalf("Reconcile = %v, %v", added, err)
	}
	want := strings.Replace(orig, "Bearer tok1", "Bearer tok2", 1)
	if got := readFile(t, path); got != want {
		t.Fatalf("config.toml =\n%s\nwant\n%s", got, want)
	}
	if added, err := New().Reconcile(mcpOpts("tok2")); err != nil || len(added) != 0 {
		t.Fatalf("second Reconcile = %v, %v; want no change", added, err)
	}
	if got := readFile(t, path); got != want {
		t.Fatalf("unchanged reconcile rewrote config.toml:\n%s", got)
	}
}

func TestCodexPreservesOtherContent(t *testing.T) {
	path := codexConfig(t, otherConfig)
	if _, err := New().Reconcile(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	installed := readFile(t, path)
	if !strings.HasPrefix(installed, otherConfig) || !strings.Contains(installed, argusTable) {
		t.Fatalf("install changed other content:\n%s", installed)
	}
	if err := New().Uninstall(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != otherConfig {
		t.Fatalf("after install+uninstall:\n%s\nwant\n%s", got, otherConfig)
	}
}

func TestCodexUninstallRemovesOnlyArgus(t *testing.T) {
	before := "model = \"gpt-5\"\n"
	after := "\n# other server\n[mcp_servers.other]\ncommand = \"other-mcp\"\n"
	path := codexConfig(t, before+"\n"+argusTable+"[mcp_servers.argus.tools.spawn]\napproval_mode = \"approve\"\n"+after)
	if err := New().Uninstall(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != before+after {
		t.Fatalf("config.toml =\n%q\nwant\n%q", got, before+after)
	}
}

func TestCodexRefusesArgusOutsideItsTable(t *testing.T) {
	orig := "mcp_servers.argus.url = \"http://elsewhere\"\n"
	path := codexConfig(t, orig)
	if err := New().Install(mcpOpts("tok1")); err == nil {
		t.Fatal("Install succeeded; want an error for an argus entry argus cannot edit")
	}
	if got := readFile(t, path); got != orig {
		t.Fatalf("config.toml changed:\n%s", got)
	}
}

func TestInstallSkipsWithoutMCPURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := New().Install(adapter.InstallOptions{ArgusBin: "/bin/argus"}); err != nil {
		t.Fatal(err)
	}
	if added, err := New().Reconcile(adapter.InstallOptions{ArgusBin: "/bin/argus"}); err != nil || len(added) != 0 {
		t.Fatalf("Reconcile = %v, %v", added, err)
	}
	if _, err := os.Stat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("config.toml written without an MCP URL; stat err = %v", err)
	}
}

func TestReconcileSkipsMissingCodexHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	t.Setenv("CODEX_HOME", home)
	if added, err := New().Reconcile(mcpOpts("tok1")); !errors.Is(err, adapter.ErrAgentNotInstalled) || len(added) != 0 {
		t.Fatalf("Reconcile = %v, %v", added, err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("codex home created for an agent that is not set up; stat err = %v", err)
	}
}

func TestCodexReconcileReloadsMCPServers(t *testing.T) {
	codexConfig(t, "")
	f := newFakeDaemon(t, nil)
	c, err := dialRPC(context.Background(), f.sock)
	if err != nil {
		t.Fatal(err)
	}
	d := &discoverer{}
	d.setConn(c)
	if _, err := (&cxAdapter{disc: d}).Reconcile(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	f.expect(t, "config/mcpServer/reload")
}

func TestCodexReloadWaitsForConnection(t *testing.T) {
	codexConfig(t, "")
	f := newFakeDaemon(t, nil)
	d := newDiscoverer(registry.New(), nil)
	d.sockPath = func() (string, error) { return f.sock, nil }
	d.retry = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d.ctx = ctx
	if _, err := (&cxAdapter{disc: d}).Reconcile(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	if err := d.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.expect(t, "config/mcpServer/reload")
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case m := <-f.got:
			if m.Method == "config/mcpServer/reload" {
				t.Fatal("reload sent twice")
			}
		case <-deadline:
			return
		}
	}
}

func TestCodexPreservesCRLFAndMissingFinalNewline(t *testing.T) {
	for name, orig := range map[string]string{
		"crlf":            "# c\r\nmodel = \"gpt-5\"\r\n",
		"no newline":      "model = \"gpt-5\"",
		"crlf no newline": "model = \"gpt-5\"\r\n[x]\r\na = 1",
	} {
		t.Run(name, func(t *testing.T) {
			path := codexConfig(t, orig)
			if err := New().Install(mcpOpts("tok1")); err != nil {
				t.Fatal(err)
			}
			installed := readFile(t, path)
			if strings.Contains(orig, "\r\n") && strings.Count(installed, "\n") != strings.Count(installed, "\r\n") {
				t.Fatalf("mixed line endings:\n%q", installed)
			}
			if _, err := New().Reconcile(mcpOpts("tok2")); err != nil {
				t.Fatal(err)
			}
			if err := New().Uninstall(); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, path); got != orig {
				t.Fatalf("after install+uninstall: %q, want %q", got, orig)
			}
		})
	}
}

func TestCodexMultilineValuesAreNotHeaders(t *testing.T) {
	notes := "[notes]\ntext = \"\"\"\n[mcp_servers.argus]\n\"\"\"\n"
	path := codexConfig(t, notes)
	if err := New().Install(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	if err := New().Uninstall(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != notes {
		t.Fatalf("after install+uninstall: %q, want %q", got, notes)
	}

	before := "model = \"gpt-5\"\n"
	path = codexConfig(t, before+"\n"+argusTable+"matrix = [\n  [\"a\"],\n  [\"b\"],\n]\n")
	if err := New().Uninstall(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != before {
		t.Fatalf("config.toml = %q, want %q", got, before)
	}
}

func TestCodexTokenConfigIsOwnerOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte("model = \"gpt-5\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"write", "unchanged"} {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := New().Install(mcpOpts("tok1")); err != nil {
			t.Fatal(err)
		}
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: mode = %04o, want 0600", step, fi.Mode().Perm())
		}
	}
}

func TestCodexWriteKeepsSymlinkAndMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	target := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(target, []byte("model = \"gpt-5\"\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o400); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := New().Install(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.toml is no longer a symlink: %v, %v", fi, err)
	}
	if !strings.Contains(readFile(t, target), "[mcp_servers.argus]") {
		t.Fatal("symlink target not updated")
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o400 {
		t.Fatalf("mode = %04o, want 0400", fi.Mode().Perm())
	}
}

func TestCodexMCPOffRemovesArgusServer(t *testing.T) {
	adapter.NoLiveMCPEndpointForTest(t)
	path := codexConfig(t, otherConfig)
	if err := New().Install(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	installed := readFile(t, path)
	if _, err := New().Reconcile(adapter.InstallOptions{ArgusBin: "/bin/argus"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != installed {
		t.Fatalf("config.toml changed without a token:\n%s", got)
	}
	if _, err := New().Reconcile(adapter.InstallOptions{ArgusBin: "/bin/argus", MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != otherConfig {
		t.Fatalf("config.toml after mcp off:\n%s\nwant\n%s", got, otherConfig)
	}
	if err := New().Install(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	if err := New().Install(adapter.InstallOptions{MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != otherConfig {
		t.Fatalf("config.toml after install with mcp off:\n%s", got)
	}
}

func TestCodexMCPOffReloadsMCPServers(t *testing.T) {
	adapter.NoLiveMCPEndpointForTest(t)
	codexConfig(t, argusTable)
	f := newFakeDaemon(t, nil)
	c, err := dialRPC(context.Background(), f.sock)
	if err != nil {
		t.Fatal(err)
	}
	d := &discoverer{}
	d.setConn(c)
	if _, err := (&cxAdapter{disc: d}).Reconcile(adapter.InstallOptions{MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	f.expect(t, "config/mcpServer/reload")
}

func TestCodexMCPOffKeepsLiveEndpoint(t *testing.T) {
	path := codexConfig(t, otherConfig)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := New().Install(adapter.InstallOptions{MCPURL: "http://" + l.Addr().String() + "/mcp", MCPToken: "tok1"}); err != nil {
		t.Fatal(err)
	}
	installed := readFile(t, path)
	if _, err := New().Reconcile(adapter.InstallOptions{MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != installed {
		t.Fatalf("config.toml with a live endpoint:\n%s\nwant\n%s", got, installed)
	}
	l.Close()
	if _, err := New().Reconcile(adapter.InstallOptions{MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != otherConfig {
		t.Fatalf("config.toml without a listener:\n%s\nwant\n%s", got, otherConfig)
	}
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "argus-codex-state")
	if err != nil {
		panic(err)
	}
	config.StateDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func isolateStateDir(t *testing.T) string {
	t.Helper()
	old := config.StateDir
	config.StateDir = t.TempDir()
	t.Cleanup(func() { config.StateDir = old })
	return config.GetStatePath(mcpReloadMarker)
}

func connectedDiscoverer(t *testing.T) (*discoverer, *fakeDaemon) {
	t.Helper()
	f := newFakeDaemon(t, nil)
	c, err := dialRPC(context.Background(), f.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	d := &discoverer{}
	d.setConn(c)
	return d, f
}

func countReloads(f *fakeDaemon) int {
	n := 0
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case m := <-f.got:
			if m.Method == "config/mcpServer/reload" {
				n++
			}
		case <-deadline:
			return n
		}
	}
}

func TestCodexInstallMarksMCPReloadOnChange(t *testing.T) {
	marker := isolateStateDir(t)
	codexConfig(t, otherConfig)
	if err := New().Install(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker after a change: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := New().Install(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker written without a change: %v", err)
	}
}

func TestCodexReconcileSendsMarkedReload(t *testing.T) {
	for name, cfg := range map[string]string{"unchanged": argusTable, "changed": otherConfig} {
		t.Run(name, func(t *testing.T) {
			marker := isolateStateDir(t)
			codexConfig(t, cfg)
			if err := os.WriteFile(marker, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			d, f := connectedDiscoverer(t)
			if _, err := (&cxAdapter{disc: d}).Reconcile(mcpOpts("tok1")); err != nil {
				t.Fatal(err)
			}
			if n := countReloads(f); n != 1 {
				t.Fatalf("reloads = %d; want 1", n)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("marker after the reload: %v", err)
			}
		})
	}
}

func TestCodexReconcileWithoutMarkerSkipsReload(t *testing.T) {
	isolateStateDir(t)
	codexConfig(t, argusTable)
	d, f := connectedDiscoverer(t)
	if _, err := (&cxAdapter{disc: d}).Reconcile(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	if n := countReloads(f); n != 0 {
		t.Fatalf("reloads = %d; want 0", n)
	}
}
