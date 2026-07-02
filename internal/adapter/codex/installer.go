package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// managedMarker identifies argus-installed Codex hooks. hooks.json can't carry a
// comment like Claude's settings.json, so the command's `--tool codex` flag
// doubles as the marker: any command hook containing it is argus-managed.
const managedMarker = "hook --tool " + Tool

// DefaultHookEvents are the Codex hook events argus registers. PermissionRequest
// is intentionally omitted in this cut: argus does not answer Codex permission
// prompts yet, so there is no blocking-decision hook to install. Codex has no
// Notification/SessionEnd events.
var DefaultHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"Stop",
}

// hookTimeout returns the per-event command timeout in seconds. All argus Codex
// hooks are fire-and-forget observers.
func hookTimeout(string) int { return 5 }

// SettingsPath returns the Codex hooks.json path, honoring CODEX_HOME and
// falling back to ~/.codex. argus manages a dedicated hooks.json rather than
// editing config.toml, so it never rewrites the user's TOML config.
//
// NOTE (validate against a real Codex): confirm Codex loads hooks from
// ~/.codex/hooks.json and that its top-level shape matches decodeHooks below
// (an object keyed by event name → array of matcher groups).
func SettingsPath() (string, error) {
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".codex")
	}
	return filepath.Join(dir, "hooks.json"), nil
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

func isManaged(c hookCmd) bool { return strings.Contains(c.Command, managedMarker) }

func managedCommand(argusBin, event string) string {
	return fmt.Sprintf("%s hook --tool %s %s", argusBin, Tool, event)
}

// Install adds argus's hooks for the given events to hooks.json, preserving any
// other hooks. Idempotent: re-running replaces argus's managed entries rather
// than duplicating them.
func Install(argusBin string, events []string) error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}
	hooks, err := loadHooks(path)
	if os.IsNotExist(err) {
		hooks = map[string][]hookGroup{}
	} else if err != nil {
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
	return writeHooks(path, hooks)
}

// ReconcileIfInstalled brings argus's managed hooks in line with the current
// DefaultHookEvents, but only when argus hooks are already installed — a user who
// never opted in is never auto-installed. Returns the events newly added.
func ReconcileIfInstalled(argusBin string) (added []string, err error) {
	path, err := SettingsPath()
	if err != nil {
		return nil, err
	}
	hooks, err := loadHooks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
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
	if err := writeHooks(path, hooks); err != nil {
		return nil, err
	}
	return added, nil
}

// reconcileNeeded reports whether the managed hooks differ from the desired set.
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
// for an event.
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

// Uninstall removes all argus-managed hooks from hooks.json, leaving the user's
// own hooks untouched.
func Uninstall() error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}
	hooks, err := loadHooks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	stripManagedFromAll(hooks)
	return writeHooks(path, hooks)
}

// stripManagedFromAll removes argus-managed hooks from every event, dropping
// events left with no hooks.
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

// loadHooks reads hooks.json into an event→groups map. A missing or empty file
// yields an empty map.
func loadHooks(path string) (map[string][]hookGroup, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err // caller distinguishes os.IsNotExist
	}
	hooks := map[string][]hookGroup{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return hooks, nil
	}
	if err := json.Unmarshal(b, &hooks); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return hooks, nil
}

func writeHooks(path string, hooks map[string][]hookGroup) error {
	out, err := json.MarshalIndent(hooks, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}
