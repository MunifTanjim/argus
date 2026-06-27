package push

import (
	"os"
	"path/filepath"
	"sync"

	argus "github.com/MunifTanjim/argus"
)

var (
	iconOnce sync.Once
	iconFile string
	iconOK   bool
)

// materializeIcon writes the embedded argus icon to a stable cache path once and
// returns that path. ok is false if the icon can't be written (renderers then
// skip the custom icon and fall back to the platform default). Safe for
// concurrent use.
func materializeIcon() (path string, ok bool) {
	iconOnce.Do(func() {
		dir, err := os.UserCacheDir()
		if err != nil {
			return
		}
		d := filepath.Join(dir, "argus")
		if err := os.MkdirAll(d, 0o755); err != nil {
			return
		}
		p := filepath.Join(d, "notify-icon.png")
		if err := os.WriteFile(p, argus.IconPNG, 0o644); err != nil {
			return
		}
		iconFile, iconOK = p, true
	})
	return iconFile, iconOK
}
