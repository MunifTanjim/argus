package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func modelsCachePath() (string, error) {
	dir, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "models_cache.json"), nil
}

func loadModelNames() map[string]string {
	path, err := modelsCachePath()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cache struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
		} `json:"models"`
	}
	if json.Unmarshal(b, &cache) != nil {
		return nil
	}
	out := map[string]string{}
	for _, m := range cache.Models {
		if m.Slug != "" && m.DisplayName != "" {
			out[m.Slug] = m.DisplayName
		}
	}
	return out
}
