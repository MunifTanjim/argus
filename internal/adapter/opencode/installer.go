package opencode

import (
	"os"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/jsonc"
)

type mcpServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Enabled bool              `json:"enabled"`
}

type permissionRule struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

var argusPermission = permissionRule{Action: "argus_*", Resource: "*", Effect: "allow"}

func configDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "opencode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "opencode"), nil
}

// configPaths lists the global config files argus may edit, existing ones only.
func configPaths(dir string) []string {
	var paths []string
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

// writeMCPServer adds or updates the argus server and its permission rule in the
// global config, preferring opencode.json. It reports ErrAgentNotInstalled when
// the config directory does not exist.
func writeMCPServer(url, token string) (changed bool, err error) {
	dir, err := configDir()
	if err != nil {
		return false, err
	}
	if !adapter.DirExists(dir) {
		return false, adapter.ErrAgentNotInstalled
	}
	path := filepath.Join(dir, "opencode.json")
	if paths := configPaths(dir); len(paths) > 0 {
		path = paths[0]
	}
	if err := adapter.RestrictTokenFile(Agent, path); err != nil {
		return false, err
	}
	server := mcpServer{Type: "remote", URL: url, Headers: map[string]string{"Authorization": "Bearer " + token}, Enabled: true}
	return jsonc.EditFile(path, func(src []byte) ([]byte, error) { return jsonc.Set(src, mcpEntries(&server)...) })
}

// mcpServerURL is the url of the argus server in the global config argus
// edits, or "".
func mcpServerURL() string {
	dir, err := configDir()
	if err != nil {
		return ""
	}
	for _, path := range configPaths(dir) {
		if url := jsonc.LookupString(path, "mcp", "argus", "url"); url != "" {
			return url
		}
	}
	return ""
}

func removeMCPServer() error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	for _, path := range configPaths(dir) {
		if _, err := jsonc.EditFile(path, func(src []byte) ([]byte, error) { return jsonc.Unset(src, false, mcpEntries(nil)...) }); err != nil {
			return err
		}
	}
	return nil
}

func mcpEntries(server *mcpServer) []jsonc.Entry {
	e := []jsonc.Entry{
		{Path: []string{"mcp"}, Key: "argus"},
		{Path: []string{"permissions"}, Value: argusPermission},
	}
	if server != nil {
		e[0].Value = *server
	}
	return e
}
