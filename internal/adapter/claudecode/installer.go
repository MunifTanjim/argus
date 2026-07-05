package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter/hookset"
)

// legacyHookMarker is the old shell-comment marker; recognized for migration, never written.
const legacyHookMarker = "#argus-managed"

// DefaultHookEvents are the Claude Code hook events argus registers.
var DefaultHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"Notification",
	"PermissionRequest",
	"Stop",
	"SessionEnd",
}

// PermissionRequestHookTimeoutSeconds is exported so node.decisionTimeout can
// derive from it instead of hardcoding a copy.
const PermissionRequestHookTimeoutSeconds = 1500

// hookTimeout returns the per-event command timeout in seconds. PermissionRequest
// blocks until the user answers, so it gets a long timeout; others are
// fire-and-forget observers.
func hookTimeout(event string) int {
	if event == "PermissionRequest" {
		return PermissionRequestHookTimeoutSeconds
	}
	return 5
}

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

func managedCommand(argusBin, event string) string {
	return hookset.ManagedCommand(argusBin, Agent, event)
}

type hookCmd = hookset.Cmd
type hookGroup = hookset.Group

func spec() hookset.Spec {
	return hookset.Spec{
		Marker:        hookset.ManagedMarker,
		LegacyMarker:  legacyHookMarker,
		Command:       managedCommand,
		Timeout:       hookTimeout,
		DefaultEvents: DefaultHookEvents,
		Load:          loadHooks,
		Save:          saveHooks,
	}
}

func isManaged(c hookCmd) bool           { return spec().IsManaged(c) }
func hasManaged(groups []hookGroup) bool { return spec().HasManaged(groups) }

func Install(argusBin string, events []string) error { return spec().Install(argusBin, events) }

func ReconcileIfInstalled(argusBin string) (added []string, err error) {
	return spec().Reconcile(argusBin)
}

// ReconcileKeepingInstalledBin reconciles hooks but keeps the binary path already in
// settings.json, for callers whose own executable path is transient (the ephemeral
// embedded node) and must not be written into the user's hooks.
func ReconcileKeepingInstalledBin() (added []string, err error) {
	return spec().Reconcile("")
}

func Uninstall() error { return spec().Uninstall() }

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
