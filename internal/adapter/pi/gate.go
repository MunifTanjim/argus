package pi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	_ "embed"
)

// gateSource is the bundled Argus gate extension, loaded into every
// Argus-spawned pi process with -e. Embedded so installs never drift from
// the binary; staged to a content-hashed cache path so pi sees a stable file.
//
//go:embed gate/argus.ts
var gateSource string

// gateExtension stages the bundled extension and returns its path. The hash
// in the filename restages automatically when the source changes.
func (d *discoverer) gateExtension() (string, error) {
	d.gateOnce.Do(func() {
		sum := sha256.Sum256([]byte(gateSource))
		name := "argus-gate-" + hex.EncodeToString(sum[:])[:12] + ".ts"
		dir, err := os.UserCacheDir()
		if err != nil {
			dir = os.TempDir()
		}
		dir = filepath.Join(dir, "argus", "pi")
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			if mkerr := os.MkdirAll(dir, 0o755); mkerr != nil {
				d.gateErr = mkerr
				return
			}
			if werr := os.WriteFile(path, []byte(gateSource), 0o644); werr != nil {
				d.gateErr = werr
				return
			}
		}
		d.gatePath = path
	})
	if d.gateErr != nil {
		return "", fmt.Errorf("pi: stage gate extension: %w", d.gateErr)
	}
	return d.gatePath, nil
}
