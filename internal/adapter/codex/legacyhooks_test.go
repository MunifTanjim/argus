package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/hookset"
)

// Events older argus versions installed.
var legacyHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"PermissionRequest",
	"Stop",
}

type hookCmd = hookset.Cmd
type hookGroup = hookset.Group

// config.toml if it has hook definitions, else hooks.json.
func activeStore() (store, error) {
	path, err := configTOMLPath()
	if err != nil {
		return store{}, err
	}
	top, err := readTOMLTop(path)
	if err != nil && !os.IsNotExist(err) {
		return store{}, err
	}
	if defs, _ := splitHooksTable(top["hooks"]); len(defs) > 0 {
		return tomlStore()
	}
	return jsonStore()
}

func loadHooks() (hookset.Map, error) {
	s, err := jsonStore()
	if err != nil {
		return nil, err
	}
	return s.load()
}

// seedLegacyHooks writes argus-managed hooks the way older argus versions did.
func seedLegacyHooks(t *testing.T, argusBin string) {
	t.Helper()
	s, err := activeStore()
	if err != nil {
		t.Fatal(err)
	}
	spec := specFor(s)
	spec.Command = func(bin, event string) string { return hookset.ManagedCommand(bin, Agent, event) }
	spec.Timeout = func(event string) int {
		if event == "PermissionRequest" {
			return 1500
		}
		return 5
	}
	if err := spec.Install(argusBin, legacyHookEvents); err != nil {
		t.Fatal(err)
	}
}

func activeStorePath(t *testing.T) string {
	t.Helper()
	s, err := activeStore()
	if err != nil {
		t.Fatal(err)
	}
	return s.path
}

func TestRemoveLegacyHooksDeletesManagedEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	seedLegacyHooks(t, "/bin/argus")

	removed, err := removeLegacyHooks()
	if err != nil || !removed {
		t.Fatalf("removeLegacyHooks = %v, %v; want true", removed, err)
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("hooks.json should be removed when only argus hooks were in it; stat err = %v", err)
	}
	if removed, err := removeLegacyHooks(); err != nil || removed {
		t.Fatalf("second removeLegacyHooks = %v, %v; want false", removed, err)
	}
}

func TestReconcileIfInstalledRemovesLegacyHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	seedLegacyHooks(t, "/bin/argus")
	if added, err := New().ReconcileIfInstalled("/bin/argus"); err != nil || len(added) != 0 {
		t.Fatalf("ReconcileIfInstalled = %v, %v", added, err)
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); !os.IsNotExist(err) {
		t.Fatal("node start must strip argus-managed codex hooks")
	}
}

func TestInstallerPreservesUserHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := activeStorePath(t)

	user := map[string]any{
		"hooks": map[string][]hookGroup{
			"PreToolUse": {{Hooks: []hookCmd{{Type: "command", Command: "/usr/bin/user-script"}}}},
		},
	}
	b, _ := json.MarshalIndent(user, "", "  ")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	seedLegacyHooks(t, "/bin/argus")
	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	hooks, err := loadHooks()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range hooks["PreToolUse"] {
		for _, c := range g.Hooks {
			if c.Command == "/usr/bin/user-script" {
				found = true
			}
		}
	}
	if !found {
		t.Error("user hook was lost across install/uninstall")
	}
}

func TestConfigTOMLBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(home, "config.toml")
	seed := `model = "gpt-5"

[[hooks.PreToolUse]]
matcher = "Bash"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "/usr/bin/user-script"
statusMessage = "user check"
`
	if err := os.WriteFile(tomlPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	// config.toml has hooks → it is the active target.
	if p := activeStorePath(t); p != tomlPath {
		t.Fatalf("activeStorePath = %q; want config.toml", p)
	}

	seedLegacyHooks(t, "/bin/argus")
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); !os.IsNotExist(err) {
		t.Error("install must not create hooks.json when config.toml owns hooks")
	}
	got, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(got)
	for _, want := range []string{"gpt-5", "/usr/bin/user-script", "user check", "--agent codex"} {
		if !strings.Contains(content, want) {
			t.Errorf("config.toml missing %q after install:\n%s", want, content)
		}
	}

	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(tomlPath)
	if err != nil {
		t.Fatalf("config.toml must not be deleted on uninstall: %v", err)
	}
	content = string(got)
	if strings.Contains(content, "--agent codex") {
		t.Error("uninstall left argus-managed hooks in config.toml")
	}
	for _, want := range []string{"gpt-5", "/usr/bin/user-script"} {
		if !strings.Contains(content, want) {
			t.Errorf("uninstall dropped user content %q:\n%s", want, content)
		}
	}
}

func TestConfigTOMLPreservesStateAlongsideDefs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(home, "config.toml")
	seed := `[hooks.state."/x/hooks.json:pre:0:0"]
trusted_hash = "sha256:keepme"

[[hooks.PreToolUse]]
matcher = "Bash"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "/usr/bin/user-script"
`
	if err := os.WriteFile(tomlPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if p := activeStorePath(t); p != tomlPath {
		t.Fatalf("activeStorePath = %q; want config.toml (has definitions)", p)
	}
	seedLegacyHooks(t, "/bin/argus")
	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(tomlPath)
	content := string(b)
	for _, want := range []string{"trusted_hash", "sha256:keepme", "/usr/bin/user-script"} {
		if !strings.Contains(content, want) {
			t.Errorf("lost %q across install/uninstall:\n%s", want, content)
		}
	}
	if strings.Contains(content, "argus-managed") {
		t.Error("argus-managed hook not removed on uninstall")
	}
}

func TestConfigTOMLPreservesUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(home, "config.toml")
	seed := `model = "gpt-5"

[mcp_servers.fs]
command = "npx"
args = ["-y", "server-filesystem"]

[[hooks.PreToolUse]]
matcher = "Bash|Edit"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "/usr/bin/user-script"
commandWindows = "user-script.exe"
timeout = 30
statusMessage = "user check"
`
	if err := os.WriteFile(tomlPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	seedLegacyHooks(t, "/bin/argus")
	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(got)
	// Unrelated config + every documented hook field must survive the round trip.
	for _, want := range []string{
		"mcp_servers", "server-filesystem", "Bash|Edit",
		"/usr/bin/user-script", "user-script.exe", "user check", "30",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("config.toml lost %q across install/uninstall:\n%s", want, content)
		}
	}
	if strings.Contains(content, "--agent codex") {
		t.Error("argus-managed hook not removed on uninstall")
	}
}

func TestUninstallLeavesConfigWithoutHooksUntouched(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "config.toml")
	orig := "# my settings\nmodel = \"gpt-5\"\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != orig {
		t.Fatalf("config.toml rewritten:\n%s", b)
	}
}
