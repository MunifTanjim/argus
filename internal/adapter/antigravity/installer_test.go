package antigravity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/hookset"
)

func setupHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	homeDirOverride = filepath.Join(dir, "antigravity-cli")
	t.Cleanup(func() { homeDirOverride = "" })
	return dir
}

func readHooksFile(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return top
}

func assertArgusSet(t *testing.T, path string) {
	t.Helper()
	top := readHooksFile(t, path)
	raw, ok := top[argusHookSet]
	if !ok {
		t.Fatalf("%s: missing argus hook set", path)
	}
	hooks, extras, err := splitSet(raw)
	if err != nil {
		t.Fatalf("%s: parse argus set: %v", path, err)
	}
	if string(extras["enabled"]) != "true" {
		t.Errorf("%s: argus set enabled = %q; want true so agy runs the hooks", path, string(extras["enabled"]))
	}
	for _, ev := range DefaultHookEvents {
		groups := hooks[ev]
		if len(groups) == 0 {
			t.Fatalf("%s: event %q not installed", path, ev)
		}
		for _, g := range groups {
			if g.Matcher != "" {
				t.Errorf("%s: event %q group matcher = %q; want it omitted", path, ev, g.Matcher)
			}
		}
	}
}

func TestInstallToleratesEnabledAndForcesTrue(t *testing.T) {
	setupHome(t)
	primary, _ := hooksJSONPath()
	seed := `{"argus":{"enabled":false,"other":"keep","Stop":[{"hooks":[{"type":"command","command":"x hook --argus-managed --agent antigravity Stop"}]}]}}`
	if err := os.MkdirAll(filepath.Dir(primary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(primary, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Install("/usr/bin/argus", DefaultHookEvents); err != nil {
		t.Fatalf("install over enabled:false set: %v", err)
	}

	_, extras, err := splitSet(readHooksFile(t, primary)[argusHookSet])
	if err != nil {
		t.Fatal(err)
	}
	if string(extras["enabled"]) != "true" {
		t.Errorf("enabled = %q; want true", string(extras["enabled"]))
	}
	if string(extras["other"]) != `"keep"` {
		t.Errorf("foreign scalar 'other' = %q; want preserved", string(extras["other"]))
	}
	assertArgusSet(t, primary)
}

func TestSavePreservesAngleBrackets(t *testing.T) {
	setupHome(t)
	primary, _ := hooksJSONPath()
	seed := `{"probe":{"PreToolUse":[{"matcher":".*","hooks":[{"type":"command","command":"sh -c 'env >> /tmp/x.txt'"}]}]}}`
	if err := os.MkdirAll(filepath.Dir(primary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(primary, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Install("/usr/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(primary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `\u003e`) {
		t.Errorf("'>' escaped as \\u003e in written file:\n%s", b)
	}
	if !strings.Contains(string(b), ">> /tmp/x.txt") {
		t.Errorf("redirect not preserved verbatim:\n%s", b)
	}
}

func TestInstallWritesBothFiles(t *testing.T) {
	setupHome(t)
	if err := Install("/usr/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	primary, _ := hooksJSONPath()
	secondary, _ := configHooksJSONPath()
	assertArgusSet(t, primary)
	assertArgusSet(t, secondary)
}

func TestInstallPreservesOtherSets(t *testing.T) {
	setupHome(t)
	primary, _ := hooksJSONPath()
	secondary, _ := configHooksJSONPath()

	foreign := []byte(`{"user-guard":{"Stop":[{"hooks":[{"type":"command","command":"echo hi"}]}]}}`)
	for _, path := range []string{primary, secondary} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, foreign, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := Install("/usr/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{primary, secondary} {
		top := readHooksFile(t, path)
		if _, ok := top["user-guard"]; !ok {
			t.Fatalf("%s: user set dropped", path)
		}
		if _, ok := top[argusHookSet]; !ok {
			t.Fatalf("%s: argus set missing", path)
		}
	}
}

func TestUninstallRemovesBothFiles(t *testing.T) {
	setupHome(t)
	primary, _ := hooksJSONPath()
	secondary, _ := configHooksJSONPath()

	foreign := []byte(`{"user-guard":{"Stop":[{"hooks":[{"type":"command","command":"echo hi"}]}]}}`)
	for _, path := range []string{primary, secondary} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, foreign, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Install("/usr/bin/argus", DefaultHookEvents); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{primary, secondary} {
		top := readHooksFile(t, path)
		if _, ok := top[argusHookSet]; ok {
			t.Fatalf("%s: argus set not removed", path)
		}
		if _, ok := top["user-guard"]; !ok {
			t.Fatalf("%s: user set must survive", path)
		}
	}
}

func TestReconcileSelfHeals(t *testing.T) {
	setupHome(t)
	primary, _ := hooksJSONPath()
	secondary, _ := configHooksJSONPath()

	hooks := hookset.Map{}
	for _, ev := range DefaultHookEvents {
		hooks[ev] = []hookset.Group{{
			Matcher: ".*",
			Hooks: []hookset.Cmd{{
				Type:    "command",
				Command: managedCommand("/usr/bin/argus", ev),
				Timeout: hookTimeout(ev),
			}},
		}}
	}
	raw, _ := json.Marshal(hooks)
	top := map[string]json.RawMessage{argusHookSet: raw}
	b, _ := json.MarshalIndent(top, "", "  ")
	if err := os.MkdirAll(filepath.Dir(primary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(primary, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(secondary); !os.IsNotExist(err) {
		t.Fatal("secondary file should not exist before reconcile")
	}

	if _, err := ReconcileIfInstalled("/usr/bin/argus"); err != nil {
		t.Fatal(err)
	}

	assertArgusSet(t, primary)
	assertArgusSet(t, secondary)

	added, err := ReconcileIfInstalled("/usr/bin/argus")
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Errorf("second reconcile should be no-op, got added=%v", added)
	}
}

func TestReconcileNoOpWhenNotInstalled(t *testing.T) {
	setupHome(t)
	added, err := ReconcileIfInstalled("/usr/bin/argus")
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Errorf("reconcile with no hooks: added=%v; want none", added)
	}
}
