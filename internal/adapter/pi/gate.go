package pi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// gateMarker is the first line of the bundled gate. It marks an installed
// gate as Argus-managed, so Reconcile can upgrade one an older build wrote.
const gateMarker = "// argus-gate: managed by argus; edits are overwritten"

// socketLine is the gate's socket constant as embedded: empty, so the copy
// staged for managed sessions never dials a beacon.
const socketLine = `const SOCKET_PATH = "";`

var socketLineRe = regexp.MustCompile(`(?m)^const SOCKET_PATH = (".*");$`)

// stampSocket is the gate source with its socket constant set to socket.
func stampSocket(socket string) string {
	lit, _ := json.Marshal(socket)
	return strings.Replace(gateSource, socketLine, "const SOCKET_PATH = "+string(lit)+";", 1)
}

// stampedSocket reports the socket a gate source carries, and whether src is
// a gate Argus wrote, from this build or an older one.
func stampedSocket(src string) (string, bool) {
	if !strings.HasPrefix(src, gateMarker+"\n") {
		return "", false
	}
	m := socketLineRe.FindStringSubmatch(src)
	if m == nil {
		return "", false
	}
	var socket string
	if json.Unmarshal([]byte(m[1]), &socket) != nil {
		return "", false
	}
	return socket, true
}
