package antigravity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter/hookset"
)

// argusHookSet is the top-level key argus owns in hooks.json; other sets are preserved.
const argusHookSet = "argus"

var DefaultHookEvents = []string{
	"PreInvocation",
	"Stop",
}

func hookTimeout(string) int { return 5 }

func managedCommand(argusBin, event string) string {
	return hookset.ManagedCommand(argusBin, Agent, event)
}

// store is one on-disk hooks.json backend. load returns os.IsNotExist for a missing file.
type store struct {
	path string
	load func() (hookset.Map, error)
	save func(hookset.Map) error
}

func newStore(path string) store {
	return store{
		path: path,
		load: func() (hookset.Map, error) {
			top, err := readTop(path)
			if err != nil {
				return nil, err
			}
			hooks := hookset.Map{}
			if raw, ok := top[argusHookSet]; ok && len(raw) > 0 {
				defs, _, err := splitSet(raw)
				if err != nil {
					return nil, fmt.Errorf("parse %s set: %w", argusHookSet, err)
				}
				hooks = defs
			}
			return hooks, nil
		},
		save: func(hooks hookset.Map) error { return saveToPath(path, hooks) },
	}
}

func specFor(s store) hookset.Spec {
	// No matcher: agy requires it omitted on lifecycle events (only tool-scoped events accept one).
	return hookset.Spec{
		Marker:        hookset.ManagedMarker,
		Command:       managedCommand,
		Timeout:       hookTimeout,
		DefaultEvents: DefaultHookEvents,
		Load:          s.load,
		Save:          s.save,
	}
}

func stores() ([]store, error) {
	primary, err := hooksJSONPath()
	if err != nil {
		return nil, err
	}
	secondary, err := configHooksJSONPath()
	if err != nil {
		return nil, err
	}
	return []store{newStore(primary), newStore(secondary)}, nil
}

// Install adds argus's managed hooks to both hook files. Idempotent.
func Install(argusBin string, events []string) error {
	all, err := stores()
	if err != nil {
		return err
	}
	for _, s := range all {
		if err := specFor(s).Install(argusBin, events); err != nil {
			return err
		}
	}
	return nil
}

// ReconcileIfInstalled aligns managed hooks with DefaultHookEvents when already
// opted in. A store missing the argus set is healed by installing.
func ReconcileIfInstalled(argusBin string) ([]string, error) {
	all, err := stores()
	if err != nil {
		return nil, err
	}

	type storeState struct {
		s          store
		hasManaged bool
	}
	states := make([]storeState, 0, len(all))
	anyOptedIn := false

	for _, s := range all {
		hooks, err := s.load()
		if err != nil {
			if os.IsNotExist(err) {
				states = append(states, storeState{s: s})
				continue
			}
			return nil, err
		}
		managed := specFor(s).AnyManaged(hooks)
		states = append(states, storeState{s: s, hasManaged: managed})
		if managed {
			anyOptedIn = true
		}
	}

	if !anyOptedIn {
		return nil, nil
	}

	addedSet := map[string]bool{}
	for _, ss := range states {
		if ss.hasManaged {
			newAdded, err := specFor(ss.s).Reconcile(argusBin)
			if err != nil {
				return nil, err
			}
			for _, ev := range newAdded {
				addedSet[ev] = true
			}
		} else {
			if err := specFor(ss.s).Install(argusBin, DefaultHookEvents); err != nil {
				return nil, err
			}
		}
	}

	if len(addedSet) == 0 {
		return nil, nil
	}
	added := make([]string, 0, len(addedSet))
	for ev := range addedSet {
		added = append(added, ev)
	}
	return added, nil
}

// Uninstall removes argus-managed hooks from both hook files. Removes a file if it becomes empty.
func Uninstall() error {
	all, err := stores()
	if err != nil {
		return err
	}
	for _, s := range all {
		if err := specFor(s).Uninstall(); err != nil {
			return err
		}
	}
	return nil
}

func SettingsPath() (string, error) { return hooksJSONPath() }

// readTop reads hooks.json into a set-name → raw map. A missing file returns
// os.IsNotExist; an empty file yields an empty map.
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

// splitSet separates an agy hook set into event-definition arrays and scalar
// members (e.g. "enabled"), preserving both.
func splitSet(raw json.RawMessage) (hookset.Map, map[string]json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, nil, err
	}
	defs := hookset.Map{}
	extras := map[string]json.RawMessage{}
	for k, v := range obj {
		if !isJSONArray(v) {
			extras[k] = v
			continue
		}
		var groups []hookset.Group
		if err := json.Unmarshal(v, &groups); err != nil {
			return nil, nil, err
		}
		defs[k] = groups
	}
	return defs, extras, nil
}

func isJSONArray(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}

// saveToPath writes the argus set back, preserving other sets. Removes the set/file when empty.
func saveToPath(path string, hooks hookset.Map) error {
	top, err := readTop(path)
	if os.IsNotExist(err) {
		top = map[string]json.RawMessage{}
	} else if err != nil {
		return err
	}
	if len(hooks) == 0 {
		delete(top, argusHookSet)
	} else {
		// Preserve scalar members; force "enabled": true so agy runs the hooks.
		extras := map[string]json.RawMessage{}
		if raw, ok := top[argusHookSet]; ok && len(raw) > 0 {
			if _, ex, err := splitSet(raw); err == nil {
				extras = ex
			}
		}
		setObj := map[string]json.RawMessage{}
		for k, v := range extras {
			setObj[k] = v
		}
		setObj["enabled"] = json.RawMessage("true")
		for event, groups := range hooks {
			gb, err := hookset.MarshalNoEscape(groups)
			if err != nil {
				return err
			}
			setObj[event] = gb
		}
		raw, err := hookset.MarshalNoEscape(setObj)
		if err != nil {
			return err
		}
		top[argusHookSet] = raw
	}
	if len(top) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
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
