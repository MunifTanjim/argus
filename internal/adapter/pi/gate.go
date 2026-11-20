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

// gateMCP is the node's MCP endpoint and token for owned sessions' gate,
// set by UseMCP; empty when MCP is off.
var gateMCP struct {
	sync.Mutex
	url, token string
}

// stageGate writes the gate for owned sessions with the node's MCP values and
// returns its path. The content hash in the name stages a new file when the
// source or the values change. Owned sessions do not depend on the global
// gate, which can be missing or the user's own.
func stageGate() (string, error) {
	gateMCP.Lock()
	src := stampGate(gateStamp{mcpURL: gateMCP.url, mcpToken: gateMCP.token})
	gateMCP.Unlock()
	sum := sha256.Sum256([]byte(src))
	dir := config.GetCachePath("pi")
	path := filepath.Join(dir, "argus-gate-"+hex.EncodeToString(sum[:])[:12]+".ts")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("pi: stage gate extension: %w", err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		return "", fmt.Errorf("pi: stage gate extension: %w", err)
	}
	return path, nil
}

// removeStagedGates deletes the copies staged for owned sessions; they hold
// the MCP token.
func removeStagedGates() {
	paths, _ := filepath.Glob(filepath.Join(config.GetCachePath("pi"), "argus-gate-*.ts"))
	for _, p := range paths {
		os.Remove(p)
	}
}

// managedEnv marks an owned pi process for the gate, and names this node's MCP
// endpoint: a global gate stamped for another node then registers nothing.
func managedEnv() []string {
	gateMCP.Lock()
	defer gateMCP.Unlock()
	return []string{gateEnv + "=1", mcpEnv + "=" + gateMCP.url}
}

// gateMarker is the first line of the bundled gate. It marks an installed
// gate as Argus-managed, so Reconcile can upgrade one an older build wrote.
const gateMarker = "// argus-gate: managed by argus; edits are overwritten"

// gateStamp is what Argus writes into the gate's constants: the mod socket
// for beacons, and the MCP endpoint and token for the agent tools.
type gateStamp struct {
	socket, mcpURL, mcpToken string
}

// The constants as embedded: empty, so a gate nobody stamped dials no beacon
// and registers no MCP server.
const (
	socketLine   = `const SOCKET_PATH = "";`
	mcpURLLine   = `const MCP_URL = "";`
	mcpTokenLine = `const MCP_TOKEN = "";`
)

var (
	socketLineRe   = regexp.MustCompile(`(?m)^const SOCKET_PATH = (".*");$`)
	mcpURLLineRe   = regexp.MustCompile(`(?m)^const MCP_URL = (".*");$`)
	mcpTokenLineRe = regexp.MustCompile(`(?m)^const MCP_TOKEN = (".*");$`)
)

func stampGate(st gateStamp) string {
	src := gateSource
	for _, c := range []struct{ line, name, value string }{
		{socketLine, "SOCKET_PATH", st.socket},
		{mcpURLLine, "MCP_URL", st.mcpURL},
		{mcpTokenLine, "MCP_TOKEN", st.mcpToken},
	} {
		lit, _ := json.Marshal(c.value)
		src = strings.Replace(src, c.line, "const "+c.name+" = "+string(lit)+";", 1)
	}
	return src
}

// stampedGate reports the stamp a gate source carries, and whether src is a
// gate Argus wrote, from this build or an older one. Older gates have no MCP
// constants.
func stampedGate(src string) (gateStamp, bool) {
	if !strings.HasPrefix(src, gateMarker+"\n") {
		return gateStamp{}, false
	}
	var st gateStamp
	for _, c := range []struct {
		re       *regexp.Regexp
		dst      *string
		required bool
	}{
		{socketLineRe, &st.socket, true},
		{mcpURLLineRe, &st.mcpURL, false},
		{mcpTokenLineRe, &st.mcpToken, false},
	} {
		m := c.re.FindStringSubmatch(src)
		if m == nil {
			if c.required {
				return gateStamp{}, false
			}
			continue
		}
		if json.Unmarshal([]byte(m[1]), c.dst) != nil {
			return gateStamp{}, false
		}
	}
	return st, true
}
