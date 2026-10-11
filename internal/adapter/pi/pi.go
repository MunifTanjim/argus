// Package pi is the argus adapter for Pi (https://pi.dev), a minimal,
// extensible coding-agent harness. Argus owns one `pi --mode rpc` child
// process per session and drives it over JSONL on stdin/stdout; a bundled
// gate extension (loaded with -e at spawn) provides tool-approval dialogs,
// which arrive as extension_ui_request records in RPC mode.
//
// Session files live under ~/.pi/agent/sessions/--<cwd>--/<ts>_<id>.jsonl,
// but a file is created only after the first user/assistant message, so the
// adapter treats get_entries/get_messages over RPC as the primary transcript
// source and the file as fallback/history.
package pi

import (
	"os"
	"path/filepath"
	"strings"
)

// Agent is the adapter's coding-agent identifier, stored on every session it owns.
const Agent = "pi"

// minVersionFloor is the oldest pi release the adapter speaks RPC with.
// Verified against 1.1.0.
const minVersionFloor = "1.1.0"

// gateEnv marks a pi child process as Argus-owned. The bundled gate extension
// branches on it: RPC-dialog mode when set, socket-beacon mode otherwise.
const gateEnv = "ARGUS_MANAGED"

// piHome returns ~/.pi/agent (PI_CODING_AGENT_HOME override wins).
func piHome() (string, error) {
	if h := os.Getenv("PI_CODING_AGENT_HOME"); h != "" {
		return filepath.Join(h, "agent"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// sessionsRoot returns the directory holding per-project session groups.
// PI_CODING_AGENT_SESSION_DIR / sessionDir setting override wins, mirroring
// pi's own --session-dir precedence.
func sessionsRoot() (string, error) {
	if d := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); d != "" {
		return d, nil
	}
	home, err := piHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "sessions"), nil
}

// projectGroupDir maps a cwd to its session group directory name:
// "--" + cwd with leading separator stripped and /, \, : replaced by "-".
func projectGroupDir(cwd string) string {
	p := strings.TrimPrefix(cwd, string(filepath.Separator))
	p = strings.ReplaceAll(p, "\\", "-")
	p = strings.ReplaceAll(p, ":", "-")
	p = strings.ReplaceAll(p, "/", "-")
	return "--" + p + "--"
}
