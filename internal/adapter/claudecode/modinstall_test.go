package claudecode

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
)

func TestModShipsArgusAgentsSkill(t *testing.T) {
	var body string
	err := walkInstallable(func(rel string, b []byte) error {
		if rel == "skills/argus-agents/SKILL.md" {
			body = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "name: argus-agents") {
		t.Fatalf("skill missing or without frontmatter name: %q", body)
	}
}

func TestModShipsMCPConfig(t *testing.T) {
	files := map[string][]byte{}
	err := walkInstallable(func(rel string, b []byte) error {
		files[rel] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var mcp struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(files[".mcp.json"], &mcp); err != nil {
		t.Fatalf(".mcp.json: %v", err)
	}
	server, ok := mcp.MCPServers["argus"]
	if !ok || server.Type != "http" || server.URL != "${user_config.mcp_url}" || server.Headers["Authorization"] != "Bearer ${user_config.mcp_token}" {
		t.Fatalf(".mcp.json servers = %+v", mcp.MCPServers)
	}
	var manifest struct {
		UserConfig map[string]struct {
			Type      string `json:"type"`
			Sensitive bool   `json:"sensitive"`
		} `json:"userConfig"`
	}
	if err := json.Unmarshal(files[".claude-plugin/plugin.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	uc := manifest.UserConfig
	if uc["mcp_url"].Type != "string" || uc["mcp_url"].Sensitive || uc["mcp_token"].Type != "string" || !uc["mcp_token"].Sensitive {
		t.Fatalf("userConfig = %+v", uc)
	}
}

const testMCPURL = "http://127.0.0.1:7777/mcp"

func mcpInstallOpts(socket, token string) adapter.InstallOptions {
	return adapter.InstallOptions{Socket: socket, MCPURL: testMCPURL, MCPToken: token}
}

// configureValues returns the values of every `plugin configure` call.
func configureValues(t *testing.T, f *fakeClaude) []map[string]string {
	t.Helper()
	var out []map[string]string
	for i, call := range f.calls {
		if call != "plugin configure argus@argus --values-stdin" {
			continue
		}
		var v map[string]string
		if err := json.Unmarshal([]byte(f.stdin[i]), &v); err != nil {
			t.Fatalf("configure stdin %q: %v", f.stdin[i], err)
		}
		out = append(out, v)
	}
	return out
}

func TestModConfiguresMCPValues(t *testing.T) {
	seedSettings(t, `{}`)
	f := &fakeClaude{version: "2.1.296", marketplaces: "[]", plugins: "[]"}
	installFake(t, f)
	if err := New().Install(mcpInstallOpts("/run/argus/mod.sock", "tok1")); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"socket": "/run/argus/mod.sock", "mcp_url": testMCPURL + "/claude", "mcp_token": "tok1"}
	got := configureValues(t, f)
	if len(got) != 1 || !mapsEqual(got[0], want) {
		t.Fatalf("configure values = %v; want [%v]", got, want)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "tok1") {
			t.Fatalf("token passed as an argument: %q", call)
		}
	}
}

func TestReconcileConfiguresChangedMCPToken(t *testing.T) {
	seedSettings(t, socketSettings("/tmp/argus-1/mod.sock"))
	f := currentModFake(t, strings.Replace(currentUserPlugin, "%s", modVersion(), 1))
	changed, err := New().Reconcile(mcpInstallOpts("/tmp/argus-1/mod.sock", "tok1"))
	if err != nil || len(changed) == 0 {
		t.Fatalf("Reconcile = %v, %v; want a change", changed, err)
	}
	want := map[string]string{"socket": "/tmp/argus-1/mod.sock", "mcp_url": testMCPURL + "/claude", "mcp_token": "tok1"}
	if got := configureValues(t, f); len(got) != 1 || !mapsEqual(got[0], want) {
		t.Fatalf("configure values = %v; want [%v]", got, want)
	}

	f.calls, f.stdin = nil, nil
	if changed, err := New().Reconcile(mcpInstallOpts("/tmp/argus-1/mod.sock", "tok1")); err != nil || len(changed) != 0 || len(f.calls) != 0 {
		t.Fatalf("second Reconcile = %v, %v, calls %v; want nothing", changed, err, f.calls)
	}

	if _, err := New().Reconcile(mcpInstallOpts("/tmp/argus-1/mod.sock", "tok2")); err != nil {
		t.Fatal(err)
	}
	if got := configureValues(t, f); len(got) != 1 || got[0]["mcp_token"] != "tok2" {
		t.Fatalf("configure values = %v; want the new token", got)
	}
}

func TestReconcileLeavesMCPValuesOfLiveNode(t *testing.T) {
	dir, err := os.MkdirTemp("", "argus")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	live := filepath.Join(dir, "mod.sock")
	l, err := net.Listen("unix", live)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	seedSettings(t, socketSettings(live))
	f := currentModFake(t, strings.Replace(currentUserPlugin, "%s", modVersion(), 1))
	if _, err := New().Reconcile(mcpInstallOpts("/tmp/argus-dev/mod.sock", "tok1")); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.calls, "plugin configure argus@argus --values-stdin") {
		t.Fatalf("calls = %v; the live node owns the mod's values", f.calls)
	}
}

func TestReconcileMCPOffKeepsAllowRuleOfLiveNode(t *testing.T) {
	dir, err := os.MkdirTemp("", "argus")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	live := filepath.Join(dir, "mod.sock")
	l, err := net.Listen("unix", live)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	orig := `{"permissions": {"allow": ["` + claudeAllowRule + `"]}, "pluginConfigs": {"argus@argus": {"options": {"socket": "` + live + `"}}}}`
	path := seedSettings(t, orig)
	f := currentModFake(t, strings.Replace(currentUserPlugin, "%s", modVersion(), 1))
	if _, err := New().Reconcile(adapter.InstallOptions{Socket: "/tmp/argus-dev/mod.sock", MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.calls, "plugin configure argus@argus --values-stdin") {
		t.Fatalf("calls = %v; the live node owns the mod's values", f.calls)
	}
	if got := readSettings(t, path); got != orig {
		t.Fatalf("settings = %s; want the live node's allow rule kept", got)
	}
}

func TestInstallMCPOffKeepsAllowRuleOfLiveNode(t *testing.T) {
	dir, err := os.MkdirTemp("", "argus")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	live := filepath.Join(dir, "mod.sock")
	l, err := net.Listen("unix", live)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	path := seedSettings(t, `{"permissions": {"allow": ["`+claudeAllowRule+`"]}, "pluginConfigs": {"argus@argus": {"options": {"socket": "`+live+`"}}}}`)
	currentModFake(t, strings.Replace(currentUserPlugin, "%s", modVersion(), 1))
	if err := New().Install(adapter.InstallOptions{Socket: "/tmp/argus-dev/mod.sock", MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	if got := readSettings(t, path); !strings.Contains(got, claudeAllowRule) {
		t.Fatalf("settings = %s; want the live node's allow rule kept", got)
	}
}

func TestReconcileWithoutMCPURLSendsSocketOnly(t *testing.T) {
	seedSettings(t, socketSettings("/tmp/argus-1/mod.sock"))
	f := currentModFake(t, strings.Replace(currentUserPlugin, "%s", modVersion(), 1))
	if _, err := New().Reconcile(adapter.InstallOptions{Socket: "/tmp/argus-2/mod.sock"}); err != nil {
		t.Fatal(err)
	}
	if got := configureValues(t, f); len(got) != 1 || !mapsEqual(got[0], map[string]string{"socket": "/tmp/argus-2/mod.sock"}) {
		t.Fatalf("configure values = %v", got)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

const claudeAllowRule = "mcp__plugin_argus_argus__*"

func TestClaudeAllowRule(t *testing.T) {
	for name, orig := range map[string]string{
		"others":         "{\n  \"model\": \"opus\",\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(ls *)\",\n      \"WebFetch\"\n    ],\n    \"deny\": [\n      \"Bash(sudo *)\"\n    ],\n    \"defaultMode\": \"auto\"\n  },\n  \"pluginConfigs\": {\n    \"argus@argus\": {\n      \"options\": {\n        \"socket\": \"/run/argus/mod.sock\"\n      }\n    }\n  }\n}\n",
		"no permissions": "{\n  \"model\": \"opus\"\n}\n",
		"empty allow":    `{"permissions":{"allow":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := seedSettings(t, orig)
			f := &fakeClaude{version: "2.1.296", marketplaces: `[{"name":"argus"}]`, plugins: `[{"id":"argus@argus","scope":"user","version":"x"}]`}
			installFake(t, f)
			if err := New().Install(mcpInstallOpts("/run/argus/mod.sock", "tok1")); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			var settings struct {
				Permissions struct {
					Allow []string `json:"allow"`
				} `json:"permissions"`
			}
			if err := json.Unmarshal(b, &settings); err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(string(b), claudeAllowRule); n != 1 || !slices.Contains(settings.Permissions.Allow, claudeAllowRule) {
				t.Fatalf("settings after install:\n%s", b)
			}
			if changed, err := New().Reconcile(mcpInstallOpts("/run/argus/mod.sock", "tok1")); err != nil || slices.Contains(changed, "permissions.allow "+claudeAllowRule) {
				t.Fatalf("second Reconcile = %v, %v; want the rule kept", changed, err)
			}
			if err := New().Uninstall(); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(path); string(b) != orig {
				t.Fatalf("settings after install+uninstall:\n%s\nwant\n%s", b, orig)
			}
		})
	}
}

func TestClaudeAllowRuleSkippedWithoutMCPURL(t *testing.T) {
	path := seedSettings(t, `{}`)
	installFake(t, &fakeClaude{version: "2.1.296", marketplaces: "[]", plugins: "[]"})
	if err := New().Install(adapter.InstallOptions{Socket: "/run/argus/mod.sock"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{}` {
		t.Fatalf("settings = %s", b)
	}
}

func TestUninstallKeepsAllowRuleWhenPluginUninstallFails(t *testing.T) {
	orig := `{"permissions":{"allow":["` + claudeAllowRule + `"]}}`
	path := seedSettings(t, orig)
	f := currentModFake(t, strings.Replace(currentUserPlugin, "%s", modVersion(), 1))
	f.fail = "plugin uninstall argus@argus"
	if err := New().Uninstall(); err == nil {
		t.Fatal("Uninstall succeeded; want the plugin uninstall error")
	}
	if b, _ := os.ReadFile(path); string(b) != orig {
		t.Fatalf("settings = %s; want the allow rule kept", b)
	}
}

func TestAllowRuleWaitsForModLock(t *testing.T) {
	path := seedSettings(t, `{}`)
	installFake(t, &fakeClaude{})
	unlock, err := lockMod()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := allowMCPTools()
		done <- err
	}()
	select {
	case err := <-done:
		unlock()
		t.Fatalf("allowMCPTools ran while the mod lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if b, _ := os.ReadFile(path); string(b) != `{}` {
		t.Fatalf("settings edited under another holder's lock: %s", b)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), claudeAllowRule) {
		t.Fatalf("settings = %s", b)
	}
}

func TestReconcileMCPOffClearsValuesAndAllowRule(t *testing.T) {
	const sock = "/tmp/argus-1/mod.sock"
	path := seedSettings(t, socketSettings(sock))
	f := currentModFake(t, strings.Replace(currentUserPlugin, "%s", modVersion(), 1))
	if _, err := New().Reconcile(mcpInstallOpts(sock, "tok1")); err != nil {
		t.Fatal(err)
	}
	if stampedMCP() == "" || !strings.Contains(readSettings(t, path), claudeAllowRule) {
		t.Fatalf("mcp not configured: stamp %q, settings %s", stampedMCP(), readSettings(t, path))
	}

	f.calls, f.stdin = nil, nil
	if _, err := New().Reconcile(adapter.InstallOptions{Socket: sock}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 || stampedMCP() == "" || !strings.Contains(readSettings(t, path), claudeAllowRule) {
		t.Fatalf("no token: calls %v, stamp %q, settings %s; want all kept", f.calls, stampedMCP(), readSettings(t, path))
	}

	if _, err := New().Reconcile(adapter.InstallOptions{Socket: sock, MCPOff: true}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"socket": sock, "mcp_url": "", "mcp_token": ""}
	if got := configureValues(t, f); len(got) != 1 || !mapsEqual(got[0], want) {
		t.Fatalf("configure values = %v; want [%v]", got, want)
	}
	if stampedMCP() != "" {
		t.Fatalf("mcp stamp = %q; want removed", stampedMCP())
	}
	if got := readSettings(t, path); got != socketSettings(sock) {
		t.Fatalf("settings = %s; want the allow rule removed", got)
	}

	f.calls, f.stdin = nil, nil
	if changed, err := New().Reconcile(adapter.InstallOptions{Socket: sock, MCPOff: true}); err != nil || len(changed) != 0 || len(f.calls) != 0 {
		t.Fatalf("second Reconcile = %v, %v, calls %v; want nothing", changed, err, f.calls)
	}
}

func readSettings(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
