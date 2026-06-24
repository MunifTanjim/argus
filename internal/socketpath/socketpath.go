// Package socketpath computes the default location of the argusd JSON-RPC unix
// socket, shared by the node and its clients.
package socketpath

import (
	"os"
	"path/filepath"
)

// Default returns the socket path. An explicit ARGUS_SOCKET overrides
// everything; otherwise it is derived under XDG_RUNTIME_DIR (or the temp dir).
func Default() string {
	if s := os.Getenv("ARGUS_SOCKET"); s != "" {
		return s
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "argus", "argus.sock")
}
