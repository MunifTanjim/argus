package pi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/MunifTanjim/argus/internal/config"

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
var gateExtension = sync.OnceValues(func() (string, error) {
	sum := sha256.Sum256([]byte(gateSource))
	dir := config.GetCachePath("pi")
	path := filepath.Join(dir, "argus-gate-"+hex.EncodeToString(sum[:])[:12]+".ts")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("pi: stage gate extension: %w", err)
	}
	if err := os.WriteFile(path, []byte(gateSource), 0o644); err != nil {
		return "", fmt.Errorf("pi: stage gate extension: %w", err)
	}
	return path, nil
})
