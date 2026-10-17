package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
)

// fakeClaude records every claude invocation and answers the read-only ones.
type fakeClaude struct {
	version      string
	marketplaces string // `plugin marketplace list --json`
	plugins      string // `plugin list --json`
	fail         string // a call that fails
	calls        []string
	stdin        []string
}

func installFake(t *testing.T, f *fakeClaude) {
	t.Helper()
	oldRun, oldLook, oldDir := runClaude, claudeOnPath, marketplaceDir
	t.Cleanup(func() { runClaude, claudeOnPath, marketplaceDir = oldRun, oldLook, oldDir })
	dir := filepath.Join(t.TempDir(), "claude-marketplace")
	marketplaceDir = func() string { return dir }
	claudeOnPath = func() bool { return true }
	runClaude = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		f.calls = append(f.calls, call)
		f.stdin = append(f.stdin, stdin)
		if call == f.fail {
			return nil, errors.New("failed")
		}
		switch call {
		case "--version":
			return []byte(f.version + " (Claude Code)\n"), nil
		case "plugin marketplace list --json":
			// A bare argus entry stands for argus's own marketplace.
			return []byte(strings.ReplaceAll(f.marketplaces, `{"name":"argus"}`, `{"name":"argus","path":"`+dir+`"}`)), nil
		case "plugin list --json":
			return []byte(f.plugins), nil
		}
		return nil, nil
	}
}

func seedSettings(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const legacyAndUserHooks = `{
  "model": "opus",
  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command", "command": "my-own-thing" } ] },
      { "hooks": [ { "type": "command", "command": "/usr/bin/argus hook Stop --agent claude --argus-managed" } ] }
    ]
  }
}`

func TestInstallKeepsUserHooks(t *testing.T) {
	path := seedSettings(t, legacyAndUserHooks)
	installFake(t, &fakeClaude{version: "2.1.296", marketplaces: "[]", plugins: "[]"})

	if err := New().Install(adapter.InstallOptions{Socket: "/run/argus/mod.sock"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "argus hook") {
		t.Fatalf("argus hook left in settings.json:\n%s", b)
	}
	if !strings.Contains(string(b), "my-own-thing") || !strings.Contains(string(b), `"opus"`) {
		t.Fatalf("user settings lost:\n%s", b)
	}
}

func TestInstallAddsMarketplaceAndInstallsMod(t *testing.T) {
	seedSettings(t, `{}`)
	f := &fakeClaude{version: "2.1.296", marketplaces: "[]", plugins: "[]"}
	installFake(t, f)

	if err := New().Install(adapter.InstallOptions{Socket: "/run/argus/mod.sock"}); err != nil {
		t.Fatal(err)
	}
	dir := marketplaceDir()
	want := []string{
		"--version",
		"plugin list --json",
		"plugin marketplace list --json",
		"plugin marketplace add " + dir,
		"plugin install argus@argus --config socket=/run/argus/mod.sock",
	}
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
	var manifest struct {
		Version string `json:"version"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "argus", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &manifest)
	if manifest.Version != modVersion() {
		t.Fatalf("written version = %q, want %q", manifest.Version, modVersion())
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "marketplace.json")); err != nil {
		t.Fatalf("marketplace.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "argus", "hooks", "register.test.ts")); !os.IsNotExist(err) {
		t.Fatal("tests must not be installed with the mod")
	}
}

func TestInstallUpdatesStaleModAndSetsSocket(t *testing.T) {
	seedSettings(t, `{}`)
	f := &fakeClaude{
		version:      "2.1.296",
		marketplaces: `[{"name":"argus"}]`,
		plugins:      `[{"id":"argus@argus","scope":"user","version":"old"}]`,
	}
	installFake(t, f)

	if err := New().Install(adapter.InstallOptions{Socket: "/run/argus/mod.sock"}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{
		"plugin marketplace update argus",
		"plugin update argus@argus",
		"plugin configure argus@argus --values-stdin",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in calls:\n%s", want, joined)
		}
	}
	if got := f.stdin[len(f.stdin)-1]; got != `{"socket":"/run/argus/mod.sock"}` {
		t.Fatalf("configure stdin = %q", got)
	}
}

func TestInstallRefusesOldClaudeAndKeepsHooks(t *testing.T) {
	path := seedSettings(t, legacyAndUserHooks)
	f := &fakeClaude{version: "2.1.290"}
	installFake(t, f)

	err := New().Install(adapter.InstallOptions{Socket: "/run/argus/mod.sock"})
	if err == nil || !strings.Contains(err.Error(), minClaudeVersion) {
		t.Fatalf("err = %v, want one naming %s", err, minClaudeVersion)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "argus hook") {
		t.Fatal("old hooks must stay until Claude Code is upgraded")
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls = %v, want only --version", f.calls)
	}
	if !strings.Contains(err.Error(), "upgrade Claude Code") || !strings.Contains(err.Error(), "argus hooks install") {
		t.Fatalf("err = %v, want it to state the upgrade and the re-run", err)
	}
}

func TestInstallWithoutClaudeIsNotInstalled(t *testing.T) {
	seedSettings(t, `{}`)
	installFake(t, &fakeClaude{})
	claudeOnPath = func() bool { return false }
	if err := New().Install(adapter.InstallOptions{}); !errors.Is(err, adapter.ErrAgentNotInstalled) {
		t.Fatalf("err = %v, want ErrAgentNotInstalled", err)
	}
}

func TestReconcileInstallsWhenNeverInstalled(t *testing.T) {
	seedSettings(t, `{}`)
	f := &fakeClaude{version: "2.1.296", marketplaces: "[]", plugins: "[]"}
	installFake(t, f)
	changed, err := New().Reconcile(adapter.InstallOptions{Socket: "/run/argus/mod.sock"})
	if err != nil || len(changed) == 0 {
		t.Fatalf("Reconcile = %v, %v; want an install", changed, err)
	}
	if !slices.Contains(f.calls, "plugin install argus@argus --config socket=/run/argus/mod.sock") {
		t.Fatalf("calls = %v; want the mod installed", f.calls)
	}
	if stampedVersion() != modVersion() {
		t.Fatal("a fresh install must be stamped")
	}
}

func TestReconcileIgnoresNonUserScopeRows(t *testing.T) {
	seedSettings(t, `{}`)
	f := &fakeClaude{version: "2.1.296", marketplaces: `[{"name":"argus"}]`,
		plugins: `[{"id":"argus@argus","scope":"project","version":"other"},{"id":"argus@argus","scope":"user","version":"` + modVersion() + `"}]`}
	installFake(t, f)
	if err := os.MkdirAll(marketplaceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	changed, err := New().Reconcile(adapter.InstallOptions{})
	if err != nil || len(changed) != 0 {
		t.Fatalf("Reconcile = %v, %v; want nothing", changed, err)
	}
}

func TestReconcileMigratesLegacyHooks(t *testing.T) {
	path := seedSettings(t, legacyAndUserHooks)
	f := &fakeClaude{version: "2.1.296", marketplaces: "[]", plugins: "[]"}
	installFake(t, f)
	changed, err := New().Reconcile(adapter.InstallOptions{Socket: "/run/argus/mod.sock"})
	if err != nil || len(changed) == 0 {
		t.Fatalf("Reconcile = %v, %v; want a migration", changed, err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "argus hook") {
		t.Fatal("reconcile must remove old hooks")
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "plugin install argus@argus") {
		t.Fatalf("reconcile must install the mod: %v", f.calls)
	}
	for _, c := range []string{"--version", "plugin list --json"} {
		if n := slices.Index(f.calls, c); n < 0 || slices.Index(f.calls[n+1:], c) >= 0 {
			t.Fatalf("want %q run once: %v", c, f.calls)
		}
	}
}

func TestReconcileReportsLegacyHooksWithoutClaude(t *testing.T) {
	seedSettings(t, legacyAndUserHooks)
	f := &fakeClaude{}
	installFake(t, f)
	claudeOnPath = func() bool { return false }
	_, err := New().Reconcile(adapter.InstallOptions{Socket: "/run/argus/mod.sock"})
	if err == nil || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("err = %v, want it to name the missing claude", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("claude called: %v", f.calls)
	}
}

func TestReconcileWithoutClaudeOrLegacyHooksIsNotInstalled(t *testing.T) {
	seedSettings(t, `{}`)
	f := &fakeClaude{}
	installFake(t, f)
	claudeOnPath = func() bool { return false }
	if changed, err := New().Reconcile(adapter.InstallOptions{Socket: "/run/argus/mod.sock"}); !errors.Is(err, adapter.ErrAgentNotInstalled) || len(changed) != 0 {
		t.Fatalf("Reconcile = %v, %v; want ErrAgentNotInstalled", changed, err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("claude called: %v", f.calls)
	}
}

func TestReconcileWithoutSocketDoesNotInstall(t *testing.T) {
	path := seedSettings(t, legacyAndUserHooks)
	f := &fakeClaude{version: "2.1.296", marketplaces: "[]", plugins: "[]"}
	installFake(t, f)
	changed, err := New().Reconcile(adapter.InstallOptions{})
	if err == nil {
		t.Fatalf("Reconcile = %v, nil; want an error for a fresh install with no socket", changed)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "plugin install") {
			t.Fatalf("installed the mod with no socket: %v", f.calls)
		}
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "argus hook") {
		t.Fatal("old hooks must stay when the mod cannot be installed")
	}
}

func TestReconcileSkipsClaudeWhenMarketplaceIsCurrent(t *testing.T) {
	seedSettings(t, socketSettings("/run/argus/mod.sock"))
	f := &fakeClaude{version: "2.1.296", marketplaces: `[{"name":"argus"}]`, plugins: `[{"id":"argus@argus","scope":"user","version":"` + modVersion() + `"}]`}
	installFake(t, f)
	if err := writeMarketplace(marketplaceDir()); err != nil {
		t.Fatal(err)
	}
	if err := stampInstalled(); err != nil {
		t.Fatal(err)
	}
	changed, err := New().Reconcile(adapter.InstallOptions{Socket: "/run/argus/mod.sock"})
	if err != nil || len(changed) != 0 {
		t.Fatalf("Reconcile = %v, %v; want nothing", changed, err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("claude called: %v", f.calls)
	}
}

func socketSettings(socket string) string {
	return `{"pluginConfigs": {"argus@argus": {"options": {"socket": "` + socket + `"}}}}`
}

func currentModFake(t *testing.T, plugins string) *fakeClaude {
	t.Helper()
	f := &fakeClaude{version: "2.1.296", marketplaces: `[{"name":"argus"}]`, plugins: plugins}
	installFake(t, f)
	if err := writeMarketplace(marketplaceDir()); err != nil {
		t.Fatal(err)
	}
	if err := stampInstalled(); err != nil {
		t.Fatal(err)
	}
	return f
}

const currentUserPlugin = `[{"id":"argus@argus","scope":"user","version":"%s"}]`

func TestReconcileReappliesChangedSocket(t *testing.T) {
	seedSettings(t, socketSettings("/tmp/argus-1/mod.sock"))
	f := currentModFake(t, strings.Replace(currentUserPlugin, "%s", modVersion(), 1))
	changed, err := New().Reconcile(adapter.InstallOptions{Socket: "/tmp/argus-2/mod.sock"})
	if err != nil || len(changed) == 0 {
		t.Fatalf("Reconcile = %v, %v; want a change", changed, err)
	}
	want := []string{"--version", "plugin list --json", "plugin configure argus@argus --values-stdin"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v; want %v", f.calls, want)
	}
	if got := f.stdin[2]; got != `{"socket":"/tmp/argus-2/mod.sock"}` {
		t.Fatalf("configure stdin = %q", got)
	}
}

func TestReconcileKeepsMatchingSocket(t *testing.T) {
	seedSettings(t, socketSettings("/tmp/argus-1/mod.sock"))
	f := currentModFake(t, strings.Replace(currentUserPlugin, "%s", modVersion(), 1))
	changed, err := New().Reconcile(adapter.InstallOptions{Socket: "/tmp/argus-1/mod.sock"})
	if err != nil || len(changed) != 0 || len(f.calls) != 0 {
		t.Fatalf("got %v, %v, calls %v; want nothing", changed, err, f.calls)
	}
}

func TestReconcileReinstallsRemovedPlugin(t *testing.T) {
	seedSettings(t, socketSettings("/tmp/argus-1/mod.sock"))
	f := currentModFake(t, `[{"id":"argus@argus","scope":"project","version":"x"}]`)
	changed, err := New().Reconcile(adapter.InstallOptions{Socket: "/tmp/argus-2/mod.sock"})
	if err != nil || len(changed) == 0 {
		t.Fatalf("got %v, %v; want an install", changed, err)
	}
	if !slices.Contains(f.calls, "plugin install argus@argus --config socket=/tmp/argus-2/mod.sock") {
		t.Fatalf("calls = %v; want the mod installed on the new socket", f.calls)
	}
}

func TestReconcileEmptySocketMakesNoCalls(t *testing.T) {
	seedSettings(t, socketSettings("/tmp/argus-1/mod.sock"))
	f := currentModFake(t, strings.Replace(currentUserPlugin, "%s", modVersion(), 1))
	changed, err := New().Reconcile(adapter.InstallOptions{})
	if err != nil || len(changed) != 0 || len(f.calls) != 0 {
		t.Fatalf("got %v, %v, calls %v; want nothing", changed, err, f.calls)
	}
}

func TestReconcileUpdatesStaleMod(t *testing.T) {
	seedSettings(t, `{}`)
	f := &fakeClaude{version: "2.1.296", marketplaces: `[{"name":"argus"}]`, plugins: `[{"id":"argus@argus","scope":"user","version":"old"}]`}
	installFake(t, f)
	if err := os.MkdirAll(marketplaceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	changed, err := New().Reconcile(adapter.InstallOptions{})
	if err != nil || len(changed) == 0 {
		t.Fatalf("Reconcile = %v, %v; want an update", changed, err)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "plugin update argus@argus") {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestReconcileLeavesCurrentModAlone(t *testing.T) {
	seedSettings(t, `{}`)
	f := &fakeClaude{version: "2.1.296", marketplaces: `[{"name":"argus"}]`, plugins: `[{"id":"argus@argus","scope":"user","version":"` + modVersion() + `"}]`}
	installFake(t, f)
	if err := os.MkdirAll(marketplaceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	changed, err := New().Reconcile(adapter.InstallOptions{})
	if err != nil || len(changed) != 0 {
		t.Fatalf("Reconcile = %v, %v; want nothing", changed, err)
	}
}

func TestUninstallRemovesModAndHooks(t *testing.T) {
	path := seedSettings(t, legacyAndUserHooks)
	f := &fakeClaude{version: "2.1.296", marketplaces: `[{"name":"argus"}]`, plugins: `[{"id":"argus@argus","scope":"user","version":"x"}]`}
	installFake(t, f)
	if err := New().Uninstall(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{"plugin uninstall argus@argus", "plugin marketplace remove argus"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, f.calls)
		}
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "argus hook") || !strings.Contains(string(b), "my-own-thing") {
		t.Fatalf("settings after uninstall:\n%s", b)
	}
}

func TestClaudeVersionAtLeast(t *testing.T) {
	for _, tc := range []struct {
		have string
		ok   bool
	}{
		{"2.1.293", true}, {"2.1.296", true}, {"2.2.0", true}, {"3.0.0", true},
		{"2.1.292", false}, {"2.0.999", false}, {"garbage", false},
	} {
		if got := versionAtLeast(tc.have, minClaudeVersion); got != tc.ok {
			t.Errorf("versionAtLeast(%q) = %v, want %v", tc.have, got, tc.ok)
		}
	}
}

// A failed install keeps the old hooks, so the next start migrates again.
func TestFailedMigrationIsRetried(t *testing.T) {
	path := seedSettings(t, legacyAndUserHooks)
	f := &fakeClaude{version: "2.1.296", marketplaces: "[]", plugins: "[]", fail: "plugin install argus@argus --config socket=/run/argus/mod.sock"}
	installFake(t, f)
	if _, err := New().Reconcile(adapter.InstallOptions{Socket: "/run/argus/mod.sock"}); err == nil {
		t.Fatal("reconcile must report the failed install")
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "argus hook") {
		t.Fatal("a failed install must keep the old hooks")
	}
	f.fail, f.calls = "", nil
	changed, err := New().Reconcile(adapter.InstallOptions{Socket: "/run/argus/mod.sock"})
	if err != nil || len(changed) == 0 {
		t.Fatalf("Reconcile = %v, %v; want the migration retried", changed, err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "argus hook") {
		t.Fatal("the retried migration must remove the old hooks")
	}
}

// A failed update leaves the old stamp, so the next start updates again.
func TestFailedUpdateIsRetried(t *testing.T) {
	seedSettings(t, socketSettings("/run/argus/mod.sock"))
	f := &fakeClaude{version: "2.1.296", marketplaces: `[{"name":"argus"}]`, plugins: `[{"id":"argus@argus","scope":"user","version":"old"}]`, fail: "plugin update argus@argus"}
	installFake(t, f)
	if err := os.MkdirAll(marketplaceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Reconcile(adapter.InstallOptions{Socket: "/run/argus/mod.sock"}); err == nil {
		t.Fatal("reconcile must report the failed update")
	}
	f.fail, f.calls = "", nil
	if _, err := New().Reconcile(adapter.InstallOptions{Socket: "/run/argus/mod.sock"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.calls, "plugin update argus@argus") {
		t.Fatalf("the update must be retried: %v", f.calls)
	}
}

func TestInstallRefusesForeignMarketplaceNamedArgus(t *testing.T) {
	seedSettings(t, `{}`)
	f := &fakeClaude{version: "2.1.296", marketplaces: `[{"name":"argus","path":"/elsewhere"}]`, plugins: "[]"}
	installFake(t, f)
	err := New().Install(adapter.InstallOptions{Socket: "/run/argus/mod.sock"})
	if err == nil || !strings.Contains(err.Error(), "/elsewhere") {
		t.Fatalf("err = %v, want it to name the other marketplace", err)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "plugin marketplace update") || strings.HasPrefix(c, "plugin marketplace add") || strings.HasPrefix(c, "plugin install") {
			t.Fatalf("must not touch another marketplace: %v", f.calls)
		}
	}
}

func TestInstallRefreshesOldClaudeHooks(t *testing.T) {
	path := seedSettings(t, legacyAndUserHooks)
	installFake(t, &fakeClaude{version: "2.1.290"})
	err := New().Install(adapter.InstallOptions{ArgusBin: "/new/argus", Socket: "/run/argus/mod.sock"})
	if err == nil || !strings.Contains(err.Error(), minClaudeVersion) {
		t.Fatalf("err = %v, want one naming %s", err, minClaudeVersion)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "/new/argus hook") || strings.Contains(string(b), "/usr/bin/argus") {
		t.Fatalf("old hooks must follow the new binary:\n%s", b)
	}
}

func TestReconcileRefreshesHooksWithoutClaude(t *testing.T) {
	path := seedSettings(t, legacyAndUserHooks)
	installFake(t, &fakeClaude{})
	claudeOnPath = func() bool { return false }
	_, err := New().Reconcile(adapter.InstallOptions{ArgusBin: "/new/argus", Socket: "/run/argus/mod.sock"})
	if err == nil || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("err = %v, want it to name the missing claude", err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "/new/argus hook") {
		t.Fatalf("old hooks must follow the new binary:\n%s", b)
	}
}

func TestReconcileReportsStaleModWithoutClaude(t *testing.T) {
	seedSettings(t, `{}`)
	installFake(t, &fakeClaude{})
	claudeOnPath = func() bool { return false }
	if err := os.MkdirAll(marketplaceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Reconcile(adapter.InstallOptions{}); err == nil || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("err = %v, want it to name the missing claude", err)
	}
}

func TestUninstallWithoutClaudeKeepsMarketplace(t *testing.T) {
	seedSettings(t, `{}`)
	installFake(t, &fakeClaude{})
	claudeOnPath = func() bool { return false }
	if err := os.MkdirAll(marketplaceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := New().Uninstall(); err == nil {
		t.Fatal("uninstall without claude must report the plugin it cannot remove")
	}
	if _, err := os.Stat(marketplaceDir()); err != nil {
		t.Fatalf("marketplace removed: %v", err)
	}
}

func TestReconcileLeavesSocketOfLiveNode(t *testing.T) {
	dir, err := os.MkdirTemp("", "argus") // short: unix socket paths are limited
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
	changed, err := New().Reconcile(adapter.InstallOptions{Socket: "/tmp/argus-dev/mod.sock"})
	if err != nil || len(changed) != 0 || len(f.calls) != 0 {
		t.Fatalf("got %v, %v, calls %v; want the live node's socket kept", changed, err, f.calls)
	}
}
