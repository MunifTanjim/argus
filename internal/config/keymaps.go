package config

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Mapping is one tui.keymap entry: a string, or an object with action and
// desc. Problem says what is wrong with an object, for the TUI to report.
type Mapping struct {
	Action, Desc, Problem string
}

// ReadKeymaps reads tui.keymap from the YAML file at path. It does not use
// viper, which lowercases map keys and splits them on ".", and so breaks keys
// such as "G" and "g.". An empty path gives no keymaps. A value is a string or
// an object with action and desc. Other values are kept as their text, so the
// TUI reports them as bad mappings.
func ReadKeymaps(path string) (map[string]map[string]Mapping, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tui.keymap: %w", err)
	}
	var doc struct {
		TUI struct {
			Keymap map[string]map[string]any `yaml:"keymap"`
		} `yaml:"tui"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("tui.keymap: %w", err)
	}
	if doc.TUI.Keymap == nil {
		return nil, nil
	}
	out := make(map[string]map[string]Mapping, len(doc.TUI.Keymap))
	for screen, entries := range doc.TUI.Keymap {
		out[screen] = make(map[string]Mapping, len(entries))
		for k, v := range entries {
			out[screen][k] = mappingOf(v)
		}
	}
	return out, nil
}

func mappingOf(v any) Mapping {
	obj, ok := v.(map[string]any)
	if !ok {
		return Mapping{Action: textOf(v)}
	}
	var m Mapping
	for _, k := range slices.Sorted(maps.Keys(obj)) {
		switch k {
		case "action":
			m.Action = textOf(obj[k])
		case "desc":
			m.Desc = textOf(obj[k])
		default:
			if m.Problem == "" {
				m.Problem = fmt.Sprintf("unknown field %q", k)
			}
		}
	}
	if _, ok := obj["action"]; !ok && m.Problem == "" {
		m.Problem = "needs an action"
	}
	return m
}

func textOf(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
