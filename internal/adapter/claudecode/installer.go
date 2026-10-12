package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter/hookset"
)

// legacyHookMarker is the old shell-comment marker; recognized for removal only.
const legacyHookMarker = "#argus-managed"

// SettingsPath returns the Claude Code settings.json path, honoring
// CLAUDE_CONFIG_DIR and falling back to ~/.claude.
func SettingsPath() (string, error) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Join(dir, "settings.json"), nil
}

// legacyHookEvents are the events the old settings hooks covered. A Claude too
// old for the mod keeps these hooks, refreshed to the current argus binary.
var legacyHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"Notification",
	"PermissionRequest",
	"Stop",
	"SessionEnd",
}

// legacyHookTimeout is the per-event command timeout in seconds. PermissionRequest
// blocks until the user answers; the others only report.
func legacyHookTimeout(event string) int {
	if event == "PermissionRequest" {
		return 1500
	}
	return 5
}

// spec describes the hooks argus used to install, so they can be refreshed for
// an old Claude or removed once the mod replaces them.
func spec() hookset.Spec {
	return hookset.Spec{
		Marker:       hookset.ManagedMarker,
		LegacyMarker: legacyHookMarker,
		Command: func(argusBin, event string) string {
			return hookset.ManagedCommand(argusBin, Agent, event)
		},
		Timeout:       legacyHookTimeout,
		DefaultEvents: legacyHookEvents,
		Load:          loadHooks,
		Save:          saveHooks,
	}
}

// refreshLegacyHooks points the old hooks at argusBin (kept when empty) and
// returns cause, which says why they stay.
func refreshLegacyHooks(argusBin string, cause error) ([]string, error) {
	added, err := spec().Reconcile(argusBin)
	if err != nil {
		return nil, errors.Join(cause, err)
	}
	return added, cause
}

// removeLegacyHooks strips argus's settings.json hooks, leaving the user's own.
func removeLegacyHooks() error {
	return spec().Uninstall()
}

// loadHooks reads settings.json's "hooks" section.
// Returns os.IsNotExist when the config file is absent.
func loadHooks() (hookset.Map, error) {
	path, err := SettingsPath()
	if err != nil {
		return nil, err
	}
	top, err := readTop(path)
	if err != nil {
		return nil, err
	}
	return decodeHooks(top)
}

// saveHooks writes the "hooks" section back, preserving other top-level keys.
// An empty hook set drops the "hooks" key.
func saveHooks(hooks hookset.Map) error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}
	top, err := readTop(path)
	if os.IsNotExist(err) {
		top = map[string]json.RawMessage{}
	} else if err != nil {
		return err
	}
	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		raw, err := hookset.MarshalNoEscape(hooks)
		if err != nil {
			return err
		}
		top["hooks"] = raw
	}
	out, err := hookset.MarshalIndentNoEscape(top)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// readTop reads settings.json into a raw-value map. A missing file returns
// os.IsNotExist unchanged; an empty file yields an empty map.
func readTop(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	top := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return top, nil
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return top, nil
}

func decodeHooks(top map[string]json.RawMessage) (hookset.Map, error) {
	hooks := hookset.Map{}
	if raw, ok := top["hooks"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("parse hooks: %w", err)
		}
	}
	return hooks, nil
}
