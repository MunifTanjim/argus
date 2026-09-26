package config

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// ReadKeymaps reads tui.keymap from the YAML file at path. It does not use
// viper, which lowercases map keys and splits them on ".", and so breaks keys
// such as "G" and "g.". An empty path gives no keymaps. A value that is not a
// string is kept as its text, so the TUI reports it as an unknown command.
func ReadKeymaps(path string) (map[string]map[string]string, error) {
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
	out := make(map[string]map[string]string, len(doc.TUI.Keymap))
	for screen, entries := range doc.TUI.Keymap {
		out[screen] = make(map[string]string, len(entries))
		for k, v := range entries {
			switch v := v.(type) {
			case nil:
				out[screen][k] = ""
			case string:
				out[screen][k] = v
			default:
				out[screen][k] = fmt.Sprint(v)
			}
		}
	}
	return out, nil
}
