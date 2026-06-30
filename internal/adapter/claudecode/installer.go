package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HookMarker is appended (as a shell comment) to every installed command so the
// installer can recognize and remove only its own hooks, leaving the user's.
const HookMarker = "#argus-managed"

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

// hookTimeout returns the per-event command timeout in seconds. PermissionRequest
// blocks until the user answers, so it gets a long timeout; others are
// fire-and-forget observers.
func hookTimeout(event string) int {
	if event == "PermissionRequest" {
		return 600
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

type hookCmd struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type hookGroup struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []hookCmd `json:"hooks"`
}

func isManaged(c hookCmd) bool { return strings.Contains(c.Command, HookMarker) }

func managedCommand(argusBin, event string) string {
	return fmt.Sprintf("%s hook %s %s", argusBin, event, HookMarker)
}

// Install adds argus's hooks for the given events to settings.json, preserving
// other settings and existing hooks. Idempotent: re-running replaces argus's
// managed entries rather than duplicating them. argusBin is the client binary
// the hooks invoke.
func Install(argusBin string, events []string) error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}
	top, err := loadSettings(path)
	if err != nil {
		return err
	}
	hooks, err := decodeHooks(top)
	if err != nil {
		return err
	}

	for _, event := range events {
		groups := stripManaged(hooks[event])
		groups = append(groups, hookGroup{
			Hooks: []hookCmd{{
				Type:    "command",
				Command: managedCommand(argusBin, event),
				Timeout: hookTimeout(event),
			}},
		})
		hooks[event] = groups
	}

	return writeHooks(path, top, hooks)
}

// ReconcileIfInstalled brings argus's managed hooks in line with the current
// DefaultHookEvents, but only when argus hooks are already installed — a user who
// never opted in is never auto-installed. Lets a fresh argusd self-heal stale
// hook sets without a manual reinstall. Returns the events newly added.
func ReconcileIfInstalled(argusBin string) (added []string, err error) {
	path, err := SettingsPath()
	if err != nil {
		return nil, err
	}
	top, err := loadSettings(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	hooks, err := decodeHooks(top)
	if err != nil {
		return nil, err
	}
	if !anyManaged(hooks) {
		return nil, nil // opt-in preserved: don't auto-install
	}

	for _, event := range DefaultHookEvents {
		if !hasManaged(hooks[event]) {
			added = append(added, event)
		}
	}
	if !reconcileNeeded(hooks, argusBin) {
		return nil, nil
	}

	// Rebuild the managed set: strip managed entries everywhere, then add the
	// current DefaultHookEvents. User (non-managed) hooks are preserved.
	stripManagedFromAll(hooks)
	for _, event := range DefaultHookEvents {
		hooks[event] = append(hooks[event], hookGroup{
			Hooks: []hookCmd{{
				Type:    "command",
				Command: managedCommand(argusBin, event),
				Timeout: hookTimeout(event),
			}},
		})
	}
	if err := writeHooks(path, top, hooks); err != nil {
		return nil, err
	}
	return added, nil
}

// reconcileNeeded reports whether the managed hooks differ from the desired set:
// a DefaultHookEvent missing/mismatched its managed entry, or a managed entry on
// an event no longer in DefaultHookEvents.
func reconcileNeeded(hooks map[string][]hookGroup, argusBin string) bool {
	want := map[string]bool{}
	for _, event := range DefaultHookEvents {
		want[event] = true
		if !managedMatches(hooks[event], argusBin, event) {
			return true
		}
	}
	for event, groups := range hooks {
		if !want[event] && hasManaged(groups) {
			return true // orphaned managed entry
		}
	}
	return false
}

// managedMatches reports whether groups contain exactly the desired managed entry
// (right command + timeout) for an event.
func managedMatches(groups []hookGroup, argusBin, event string) bool {
	wantCmd := managedCommand(argusBin, event)
	for _, g := range groups {
		for _, c := range g.Hooks {
			if isManaged(c) {
				return c.Command == wantCmd && c.Timeout == hookTimeout(event)
			}
		}
	}
	return false
}

func anyManaged(hooks map[string][]hookGroup) bool {
	for _, groups := range hooks {
		if hasManaged(groups) {
			return true
		}
	}
	return false
}

func hasManaged(groups []hookGroup) bool {
	for _, g := range groups {
		for _, c := range g.Hooks {
			if isManaged(c) {
				return true
			}
		}
	}
	return false
}

// Uninstall removes all argus-managed hooks from settings.json, leaving the
// user's own hooks and other settings untouched.
func Uninstall() error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}
	top, err := loadSettings(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	hooks, err := decodeHooks(top)
	if err != nil {
		return err
	}
	stripManagedFromAll(hooks)
	return writeHooks(path, top, hooks)
}

// stripManagedFromAll removes argus-managed hooks from every event in hooks,
// dropping events left with no hooks.
func stripManagedFromAll(hooks map[string][]hookGroup) {
	for event, groups := range hooks {
		stripped := stripManaged(groups)
		if len(stripped) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = stripped
		}
	}
}

// stripManaged returns groups with argus-managed command hooks removed; groups
// left with no hooks are dropped.
func stripManaged(groups []hookGroup) []hookGroup {
	var out []hookGroup
	for _, g := range groups {
		var kept []hookCmd
		for _, c := range g.Hooks {
			if !isManaged(c) {
				kept = append(kept, c)
			}
		}
		if len(kept) > 0 {
			g.Hooks = kept
			out = append(out, g)
		}
	}
	return out
}

// loadSettings reads settings.json into a top-level map of raw values,
// preserving every key. A missing file yields an empty map.
func loadSettings(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]json.RawMessage{}, nil
		}
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

func decodeHooks(top map[string]json.RawMessage) (map[string][]hookGroup, error) {
	hooks := map[string][]hookGroup{}
	if raw, ok := top["hooks"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("parse hooks: %w", err)
		}
	}
	return hooks, nil
}

func writeHooks(path string, top map[string]json.RawMessage, hooks map[string][]hookGroup) error {
	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		raw, err := json.Marshal(hooks)
		if err != nil {
			return err
		}
		top["hooks"] = raw
	}
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}
