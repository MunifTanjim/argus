// Package wsscript reads a project's workspace scripts and runs them.
package wsscript

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Scripts are a project's resolved workspace scripts; "" means none.
type Scripts struct {
	Setup    string
	Teardown string
}

type settingsFile struct {
	Scripts struct {
		Setup    *string `toml:"setup"`
		Teardown *string `toml:"teardown"`
	} `toml:"scripts"`
}

// Load reads .argus/settings.toml, then .argus/settings.local.toml, from
// mainDir. A key in the local file replaces the shared one; "" turns it off.
func Load(mainDir string) (Scripts, error) {
	var s Scripts
	for _, name := range []string{"settings.toml", "settings.local.toml"} {
		p := filepath.Join(mainDir, ".argus", name)
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Scripts{}, err
		}
		var f settingsFile
		if err := toml.Unmarshal(b, &f); err != nil {
			return Scripts{}, fmt.Errorf(".argus/%s: %w", name, err)
		}
		if f.Scripts.Setup != nil {
			s.Setup = *f.Scripts.Setup
		}
		if f.Scripts.Teardown != nil {
			s.Teardown = *f.Scripts.Teardown
		}
	}
	return s, nil
}
