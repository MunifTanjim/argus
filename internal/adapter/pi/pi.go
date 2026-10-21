// Package pi is the argus adapter for Pi (https://pi.dev), a minimal,
// extensible coding-agent harness. Argus owns one `pi --mode rpc` process per
// session, held by a relay (`argus pi-host`) that outlives argusd, and drives
// it over JSONL through the relay's socket; a bundled gate extension (loaded
// with -e at spawn) adds no approvals; the dialogs of pi's own extensions
// arrive as extension_ui_request records in RPC mode.
//
// Transcripts and history read the session files under
// ~/.pi/agent/sessions/--<cwd>--/<ts>_<id>.jsonl.
package pi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Agent is the adapter's coding-agent identifier, stored on every session it owns.
const Agent = "pi"

// gateEnv marks a pi child process as Argus-owned. The bundled gate extension
// branches on it: RPC-dialog mode when set, socket-beacon mode otherwise.
const gateEnv = "ARGUS_MANAGED"

// piHome returns pi's agent dir: PI_CODING_AGENT_DIR, else ~/.pi/agent.
func piHome() (string, error) {
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		return expandTilde(d)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// expandTilde expands a leading "~" or "~/" as pi does for its env paths.
func expandTilde(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}

// sessionsRoot returns the directory pi writes session files under, in pi's
// precedence: PI_CODING_AGENT_SESSION_DIR, then the sessionDir setting, then
// <agent dir>/sessions. A custom dir holds every project's files directly;
// the default groups them in per-project subdirectories.
func sessionsRoot() (string, error) {
	if d := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); d != "" {
		return expandTilde(d)
	}
	home, err := piHome()
	if err != nil {
		return "", err
	}
	if d := settingsSessionDir(home); d != "" {
		return expandTilde(d)
	}
	return filepath.Join(home, "sessions"), nil
}

// settingsSessionDir is the sessionDir in pi's global settings.json, or "".
func settingsSessionDir(home string) string {
	b, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil {
		return ""
	}
	var settings struct {
		SessionDir string `json:"sessionDir"`
	}
	if json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &settings) != nil {
		return ""
	}
	return settings.SessionDir
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
