package opencode

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/jsonc"
)

const testMCPURL = "http://127.0.0.1:7777/mcp"

func mcpOpts(token string) adapter.InstallOptions {
	return adapter.InstallOptions{MCPURL: testMCPURL, MCPToken: token}
}

func configHome(t *testing.T) string {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(configHome(t), name)
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

func decodeConfig(t *testing.T, content string) map[string]any {
	t.Helper()
	v, err := jsonc.Decode([]byte(content))
	if err != nil {
		t.Fatalf("parse %q: %v", content, err)
	}
	return v
}

func wantServer(token string) map[string]any {
	return map[string]any{
		"type":    "remote",
		"url":     testMCPURL + "/opencode",
		"headers": map[string]any{"Authorization": "Bearer " + token},
		"enabled": true,
	}
}

var wantPermission = map[string]any{"action": "argus_*", "resource": "*", "effect": "allow"}

func assertInstalled(t *testing.T, content, token string) {
	t.Helper()
	cfg := decodeConfig(t, content)
	mcp, _ := cfg["mcp"].(map[string]any)
	if !reflect.DeepEqual(mcp["argus"], wantServer(token)) {
		t.Fatalf("mcp.argus = %v in\n%s", mcp["argus"], content)
	}
	perms, _ := cfg["permissions"].([]any)
	n := 0
	for _, p := range perms {
		if reflect.DeepEqual(p, wantPermission) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("permissions has %d argus rules in\n%s", n, content)
	}
}

func TestOpenCodeInstallWritesArgusServer(t *testing.T) {
	dir := configHome(t)
	if err := New().Install(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "opencode.json"))
	if err := json.Unmarshal([]byte(got), new(map[string]any)); err != nil {
		t.Fatalf("written config is not plain JSON: %v\n%s", err, got)
	}
	assertInstalled(t, got, "tok1")
}

func TestOpenCodePreservesOtherContent(t *testing.T) {
	for name, orig := range map[string]string{
		"json": `{
  "$schema": "https://opencode.ai/config.json",
  "model": "opencode/glm",
  "mcp": {
    "other": {
      "type": "local",
      "command": ["other-mcp"]
    }
  },
  "permissions": [
    {
      "action": "bash",
      "resource": "*",
      "effect": "ask"
    }
  ]
}
`,
		"jsonc": `{
  // user settings
  "$schema": "https://opencode.ai/config.json",
  "plugins": ["superpowers"], /* block */
  "mcp": {
    "other": { "type": "remote", "url": "http://x" }, // other server
  },
  "model": "opencode/glm", // trailing
}
`,
		"compact":           `{"model":"opencode/glm","mcp":{"other":{"type":"remote","url":"http://x"}},"permissions":[]}`,
		"compact no mcp":    `{"model":"opencode/glm"}`,
		"empty mcp":         "{\n  \"model\": \"m\",\n  \"mcp\": {}\n}\n",
		"empty permissions": "{\n  \"model\": \"m\",\n  \"permissions\": [],\n  \"mcp\": { }\n}\n",
		"absent":            "{\n  \"model\": \"m\"\n}\n",
		"crlf":              "{\r\n  // c\r\n  \"model\": \"m\",\r\n  \"mcp\": {\r\n    \"other\": {}\r\n  }\r\n}\r\n",
		"no final newline":  "{\n  \"model\": \"m\"\n}",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, "opencode.json", orig)
			added, err := New().Reconcile(mcpOpts("tok1"))
			if err != nil || len(added) == 0 {
				t.Fatalf("Reconcile = %v, %v", added, err)
			}
			installed := readFile(t, path)
			assertInstalled(t, installed, "tok1")
			if strings.Contains(orig, "\r\n") && strings.Count(installed, "\n") != strings.Count(installed, "\r\n") {
				t.Fatalf("mixed line endings:\n%q", installed)
			}
			if added, err := New().Reconcile(mcpOpts("tok1")); err != nil || len(added) != 0 {
				t.Fatalf("second Reconcile = %v, %v; want no change", added, err)
			}
			if err := New().Uninstall(); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, path); got != orig {
				t.Fatalf("after install+uninstall:\n%s\nwant\n%s", got, orig)
			}
		})
	}
}

func TestOpenCodeReconcileUpdatesToken(t *testing.T) {
	path := writeConfig(t, "opencode.json", "{\n  \"model\": \"m\" // keep\n}\n")
	if _, err := New().Reconcile(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, path)
	if _, err := New().Reconcile(mcpOpts("tok2")); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	assertInstalled(t, got, "tok2")
	if want := strings.Replace(first, "Bearer tok1", "Bearer tok2", 1); got != want {
		t.Fatalf("token update changed more than the token:\n%s\nwant\n%s", got, want)
	}
}

func TestOpenCodeUsesJSONCWhenOnlyItExists(t *testing.T) {
	path := writeConfig(t, "opencode.jsonc", "{\n  // comment\n  \"model\": \"m\"\n}\n")
	if err := New().Install(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "opencode.json")); !os.IsNotExist(err) {
		t.Fatalf("opencode.json created next to opencode.jsonc; stat err = %v", err)
	}
	got := readFile(t, path)
	assertInstalled(t, got, "tok1")
	if !strings.Contains(got, "// comment") {
		t.Fatalf("comment dropped:\n%s", got)
	}
}

func TestOpenCodeRefusesUnparsableConfig(t *testing.T) {
	orig := `{"model": "m",,}`
	path := writeConfig(t, "opencode.json", orig)
	if err := New().Install(mcpOpts("tok1")); err == nil {
		t.Fatal("Install succeeded on an unparsable config")
	}
	if got := readFile(t, path); got != orig {
		t.Fatalf("config changed:\n%s", got)
	}
}

func TestInstallSkipsWithoutMCPURL(t *testing.T) {
	dir := configHome(t)
	if err := New().Install(adapter.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if added, err := New().Reconcile(adapter.InstallOptions{}); err != nil || len(added) != 0 {
		t.Fatalf("Reconcile = %v, %v", added, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "opencode.json")); !os.IsNotExist(err) {
		t.Fatalf("opencode.json written without an MCP URL; stat err = %v", err)
	}
}

func TestReconcileSkipsMissingConfigDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if added, err := New().Reconcile(mcpOpts("tok1")); !errors.Is(err, adapter.ErrAgentNotInstalled) || len(added) != 0 {
		t.Fatalf("Reconcile = %v, %v", added, err)
	}
	if _, err := os.Stat(filepath.Join(xdg, "opencode")); !os.IsNotExist(err) {
		t.Fatalf("opencode config dir created; stat err = %v", err)
	}
}

func TestOpenCodeTokenConfigIsOwnerOnly(t *testing.T) {
	dir := configHome(t)
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, []byte("{\n  \"model\": \"m\"\n}\n"), 0o644); err != nil {
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

func TestOpenCodeWriteKeepsSymlinkAndMode(t *testing.T) {
	dir := configHome(t)
	target := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(target, []byte("{\n  \"model\": \"m\"\n}\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o400); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "opencode.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := New().Install(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("opencode.json is no longer a symlink: %v, %v", fi, err)
	}
	assertInstalled(t, readFile(t, target), "tok1")
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o400 {
		t.Fatalf("mode = %04o, want 0400", fi.Mode().Perm())
	}
}

func TestOpenCodeMCPOffRemovesArgusServer(t *testing.T) {
	adapter.NoLiveMCPEndpointForTest(t)
	orig := "{\n  // keep\n  \"model\": \"opencode/glm\",\n  \"mcp\": {\n    \"other\": {\"type\": \"local\", \"command\": [\"x\"]}\n  }\n}\n"
	path := writeConfig(t, "opencode.json", orig)
	if err := New().Install(mcpOpts("tok1")); err != nil {
		t.Fatal(err)
	}
	installed := readFile(t, path)
	if _, err := New().Reconcile(adapter.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != installed {
		t.Fatalf("config changed without a token:\n%s", got)
	}
	if _, err := New().Reconcile(adapter.InstallOptions{MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != orig {
		t.Fatalf("config after mcp off:\n%s\nwant\n%s", got, orig)
	}
}

func TestOpenCodeMCPOffKeepsLiveEndpoint(t *testing.T) {
	orig := "{\n  \"model\": \"opencode/glm\"\n}\n"
	path := writeConfig(t, "opencode.json", orig)
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
		t.Fatalf("config with a live endpoint:\n%s\nwant\n%s", got, installed)
	}
	l.Close()
	if _, err := New().Reconcile(adapter.InstallOptions{MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != orig {
		t.Fatalf("config without a listener:\n%s\nwant\n%s", got, orig)
	}
}
