package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSettings(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return m
}

func TestInstallIsIdempotentAndPreservesOtherSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")

	// Pre-existing settings: an unrelated key and a user's own Stop hook.
	seed := `{
	  "model": "opus",
	  "hooks": {
	    "Stop": [ { "hooks": [ { "type": "command", "command": "my-own-thing" } ] } ]
	  }
	}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	events := []string{"Stop", "Notification"}
	if err := Install("/usr/local/bin/argus", events); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Install twice — must not duplicate.
	if err := Install("/usr/local/bin/argus", events); err != nil {
		t.Fatalf("install 2: %v", err)
	}

	m := readSettings(t, path)
	if string(m["model"]) != `"opus"` {
		t.Errorf("unrelated key not preserved: %s", m["model"])
	}

	hooks := map[string][]hookGroup{}
	if err := json.Unmarshal(m["hooks"], &hooks); err != nil {
		t.Fatal(err)
	}

	// Stop must contain the user's hook AND exactly one managed hook.
	managed, userOwned := 0, 0
	for _, g := range hooks["Stop"] {
		for _, c := range g.Hooks {
			if isManaged(c) {
				managed++
			} else if c.Command == "my-own-thing" {
				userOwned++
			}
		}
	}
	if managed != 1 {
		t.Errorf("Stop managed hooks: want 1, got %d", managed)
	}
	if userOwned != 1 {
		t.Errorf("user's own Stop hook should be preserved, got %d", userOwned)
	}

	// Uninstall removes only managed hooks; user's survive; other keys survive.
	if err := Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	m = readSettings(t, path)
	if string(m["model"]) != `"opus"` {
		t.Errorf("model lost after uninstall")
	}
	hooks = map[string][]hookGroup{}
	_ = json.Unmarshal(m["hooks"], &hooks)
	if _, ok := hooks["Notification"]; ok {
		t.Errorf("Notification event should be gone after uninstall")
	}
	stopManaged := 0
	stopUser := 0
	for _, g := range hooks["Stop"] {
		for _, c := range g.Hooks {
			if isManaged(c) {
				stopManaged++
			} else {
				stopUser++
			}
		}
	}
	if stopManaged != 0 || stopUser != 1 {
		t.Errorf("after uninstall: managed=%d userOwned=%d (want 0,1)", stopManaged, stopUser)
	}
}

func TestInstallNoPriorFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if err := Install("argus", []string{"Stop"}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}
}

func TestReconcileIfInstalledAddsMissingEvents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")

	// Simulate an older install: only a managed Stop hook, plus a user hook.
	seed := `{
	  "model": "opus",
	  "hooks": {
	    "Stop": [
	      { "hooks": [ { "type": "command", "command": "my-own-thing" } ] },
	      { "hooks": [ { "type": "command", "command": "/bin/argus hook --argus-managed --tool claude-code Stop", "timeout": 5 } ] }
	    ]
	  }
	}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	added, err := ReconcileIfInstalled("/bin/argus")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// PermissionRequest (and the rest) should now be added.
	want := false
	for _, e := range added {
		if e == "PermissionRequest" {
			want = true
		}
		if e == "Stop" {
			t.Errorf("Stop already managed; should not be in added: %v", added)
		}
	}
	if !want {
		t.Fatalf("PermissionRequest not added: %v", added)
	}

	top := readSettings(t, path)
	hooks := map[string][]hookGroup{}
	_ = json.Unmarshal(top["hooks"], &hooks)
	if !hasManaged(hooks["PermissionRequest"]) {
		t.Error("PermissionRequest managed hook not written")
	}
	// User's own Stop hook is preserved.
	if top["model"] == nil {
		t.Error("unrelated 'model' key dropped")
	}
	var foundUser bool
	for _, g := range hooks["Stop"] {
		for _, c := range g.Hooks {
			if c.Command == "my-own-thing" {
				foundUser = true
			}
		}
	}
	if !foundUser {
		t.Error("user's Stop hook was dropped")
	}

	// Legacy #argus-managed marker must be migrated to the --argus-managed flag.
	for _, g := range hooks["Stop"] {
		for _, c := range g.Hooks {
			if isManaged(c) && strings.Contains(c.Command, legacyHookMarker) {
				t.Errorf("legacy marker not migrated: %q", c.Command)
			}
		}
	}

	// Second run is a no-op.
	added2, err := ReconcileIfInstalled("/bin/argus")
	if err != nil || len(added2) != 0 {
		t.Errorf("second reconcile should be no-op: added=%v err=%v", added2, err)
	}
}

func TestReconcileKeepingInstalledBinPreservesPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")

	// Installed from /opt/argus; an older set with only Stop managed.
	seed := `{
	  "hooks": {
	    "Stop": [
	      { "hooks": [ { "type": "command", "command": "/opt/argus hook Stop #argus-managed", "timeout": 5 } ] }
	    ]
	  }
	}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	added, err := ReconcileKeepingInstalledBin()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(added) == 0 {
		t.Fatal("expected missing events to be added")
	}

	top := readSettings(t, path)
	hooks := map[string][]hookGroup{}
	_ = json.Unmarshal(top["hooks"], &hooks)
	// Managed commands must keep /opt/argus, not the test binary's os.Executable().
	for event, groups := range hooks {
		for _, g := range groups {
			for _, c := range g.Hooks {
				if isManaged(c) && !strings.HasPrefix(c.Command, "/opt/argus hook ") {
					t.Errorf("%s: managed bin rewritten in %q", event, c.Command)
				}
			}
		}
	}
	if !hasManaged(hooks["PermissionRequest"]) {
		t.Error("PermissionRequest managed hook not added")
	}
}

func TestReconcileKeepingInstalledBinOptInPreserved(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")

	seed := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"my-own-thing"}]}]}}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	added, err := ReconcileKeepingInstalledBin()
	if err != nil || len(added) != 0 {
		t.Fatalf("opt-in: expected no-op, got added=%v err=%v", added, err)
	}
}

func TestReconcileIfInstalledOptInPreserved(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")

	// No argus-managed hooks at all → reconcile must do nothing.
	seed := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"my-own-thing"}]}]}}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	added, err := ReconcileIfInstalled("/bin/argus")
	if err != nil || len(added) != 0 {
		t.Fatalf("opt-in: expected no-op, got added=%v err=%v", added, err)
	}
	top := readSettings(t, path)
	hooks := map[string][]hookGroup{}
	_ = json.Unmarshal(top["hooks"], &hooks)
	if _, ok := hooks["PermissionRequest"]; ok {
		t.Error("reconcile auto-installed hooks for a non-opted-in user")
	}
}

// TestReconcileMigratesLegacyMarker checks a pre-unified install (commands ending
// in the "#argus-managed" shell comment, no --agent) is recognized and rewritten to
// the current `--argus-managed --agent claude …` form, preserving user hooks.
func TestReconcileMigratesLegacyMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")

	seed := `{
	  "hooks": {
	    "Stop": [
	      { "hooks": [ { "type": "command", "command": "my-own-thing" } ] },
	      { "hooks": [ { "type": "command", "command": "/bin/argus hook Stop #argus-managed", "timeout": 5 } ] }
	    ],
	    "SessionStart": [
	      { "hooks": [ { "type": "command", "command": "/bin/argus hook SessionStart #argus-managed", "timeout": 5 } ] }
	    ]
	  }
	}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ReconcileIfInstalled("/bin/argus"); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if strings.Contains(content, "#argus-managed") {
		t.Errorf("legacy marker not migrated away:\n%s", content)
	}
	if !strings.Contains(content, "/bin/argus hook --argus-managed --agent claude Stop") {
		t.Errorf("Stop not rewritten to the unified form:\n%s", content)
	}

	top := readSettings(t, path)
	hooks := map[string][]hookGroup{}
	if err := json.Unmarshal(top["hooks"], &hooks); err != nil {
		t.Fatal(err)
	}
	for _, e := range DefaultHookEvents {
		if !hasManaged(hooks[e]) {
			t.Errorf("event %q not managed after migration", e)
		}
	}
	var foundUser bool
	for _, g := range hooks["Stop"] {
		for _, c := range g.Hooks {
			if c.Command == "my-own-thing" {
				foundUser = true
			}
		}
	}
	if !foundUser {
		t.Error("user's own hook was dropped during migration")
	}
}
