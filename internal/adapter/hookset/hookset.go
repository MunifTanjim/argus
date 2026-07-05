// Package hookset manages argus's command hooks inside a tool's hook config
// without disturbing the user's hooks.
package hookset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// MarshalIndentNoEscape is json.MarshalIndent without HTML-escaping <, >, and &.
func MarshalIndentNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MarshalNoEscape marshals v to compact JSON without HTML-escaping <, >, and &.
func MarshalNoEscape(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// ManagedMarker is the argv token argus uses to identify its own installed hooks.
const ManagedMarker = "--argus-managed"

// ManagedCommand builds the hook command argus installs for an agent and event.
func ManagedCommand(argusBin, agent, event string) string {
	return fmt.Sprintf("%s hook %s --agent %s %s", argusBin, ManagedMarker, agent, event)
}

// installedBin recovers the binary path from a managed command.
func installedBin(command string) string {
	if i := strings.Index(command, " hook "); i >= 0 {
		return command[:i]
	}
	return ""
}

type Cmd struct {
	Type           string `json:"type"`
	Command        string `json:"command"`
	CommandWindows string `json:"commandWindows,omitempty"`
	Timeout        int    `json:"timeout,omitempty"`
	StatusMessage  string `json:"statusMessage,omitempty"`
}

type Group struct {
	Matcher string `json:"matcher,omitempty"`
	Hooks   []Cmd  `json:"hooks"`
}

type Map = map[string][]Group

// Spec configures managed-hook reconciliation for one tool.
// Load must return os.IsNotExist when the config file is absent.
type Spec struct {
	// Marker identifies managed hooks: a hook is managed iff its Command contains this.
	Marker string
	// LegacyMarker recognizes hooks from older installs for cleanup/migration.
	LegacyMarker string
	// Matcher is set on every managed group. "" omits it.
	Matcher string
	// Command builds the managed command for an event; Timeout its per-event timeout.
	Command func(argusBin, event string) string
	Timeout func(event string) int
	// DefaultEvents is the desired managed event set for Reconcile.
	DefaultEvents []string
	Load          func() (Map, error)
	Save          func(Map) error
}

// Install adds argus's managed hooks for the given events (idempotent).
func (s Spec) Install(argusBin string, events []string) error {
	hooks, err := s.Load()
	if os.IsNotExist(err) {
		hooks = Map{}
	} else if err != nil {
		return err
	}
	for _, event := range events {
		hooks[event] = append(s.stripManaged(hooks[event]), s.managedGroup(argusBin, event))
	}
	return s.Save(hooks)
}

// Reconcile brings managed hooks in line with DefaultEvents, only when already installed.
func (s Spec) Reconcile(argusBin string) (added []string, err error) {
	hooks, err := s.Load()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	managed, ok := s.firstManaged(hooks)
	if !ok {
		return nil, nil // opt-in preserved: don't auto-install
	}
	if argusBin == "" {
		argusBin = installedBin(managed.Command)
	}

	for _, event := range s.DefaultEvents {
		if !s.hasManaged(hooks[event]) {
			added = append(added, event)
		}
	}
	if !s.reconcileNeeded(hooks, argusBin) {
		return nil, nil
	}

	s.stripManagedFromAll(hooks)
	for _, event := range s.DefaultEvents {
		hooks[event] = append(hooks[event], s.managedGroup(argusBin, event))
	}
	if err := s.Save(hooks); err != nil {
		return nil, err
	}
	return added, nil
}

// Uninstall removes all argus-managed hooks, leaving user hooks untouched.
func (s Spec) Uninstall() error {
	hooks, err := s.Load()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	s.stripManagedFromAll(hooks)
	return s.Save(hooks)
}

func (s Spec) managedGroup(argusBin, event string) Group {
	return Group{
		Matcher: s.Matcher,
		Hooks: []Cmd{{
			Type:    "command",
			Command: s.Command(argusBin, event),
			Timeout: s.Timeout(event),
		}},
	}
}

func (s Spec) reconcileNeeded(hooks Map, argusBin string) bool {
	want := map[string]bool{}
	for _, event := range s.DefaultEvents {
		want[event] = true
		if !s.managedMatches(hooks[event], argusBin, event) {
			return true
		}
	}
	for event, groups := range hooks {
		if !want[event] && s.hasManaged(groups) {
			return true // orphaned managed entry
		}
	}
	return false
}

func (s Spec) managedMatches(groups []Group, argusBin, event string) bool {
	wantCmd := s.Command(argusBin, event)
	for _, g := range groups {
		for _, c := range g.Hooks {
			if s.IsManaged(c) {
				return c.Command == wantCmd && c.Timeout == s.Timeout(event) && g.Matcher == s.Matcher
			}
		}
	}
	return false
}

func (s Spec) IsManaged(c Cmd) bool {
	if strings.Contains(c.Command, s.Marker) {
		return true
	}
	return s.LegacyMarker != "" && strings.Contains(c.Command, s.LegacyMarker)
}

func (s Spec) anyManaged(hooks Map) bool {
	for _, groups := range hooks {
		if s.hasManaged(groups) {
			return true
		}
	}
	return false
}
func (s Spec) AnyManaged(hooks Map) bool { return s.anyManaged(hooks) }

func (s Spec) firstManaged(hooks Map) (Cmd, bool) {
	for _, groups := range hooks {
		for _, g := range groups {
			for _, c := range g.Hooks {
				if s.IsManaged(c) {
					return c, true
				}
			}
		}
	}
	return Cmd{}, false
}

func (s Spec) hasManaged(groups []Group) bool {
	for _, g := range groups {
		if slices.ContainsFunc(g.Hooks, s.IsManaged) {
			return true
		}
	}
	return false
}
func (s Spec) HasManaged(groups []Group) bool { return s.hasManaged(groups) }

func (s Spec) stripManagedFromAll(hooks Map) {
	for event, groups := range hooks {
		stripped := s.stripManaged(groups)
		if len(stripped) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = stripped
		}
	}
}

func (s Spec) stripManaged(groups []Group) []Group {
	var out []Group
	for _, g := range groups {
		var kept []Cmd
		for _, c := range g.Hooks {
			if !s.IsManaged(c) {
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
