package adapter

import (
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

var dialMCPEndpoint = func(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, timeout)
}

// SetMCPDialerForTest replaces the dialer MCPEndpointLive uses, returning a func
// that restores it.
func SetMCPDialerForTest(dial func(addr string, timeout time.Duration) (net.Conn, error)) (restore func()) {
	orig := dialMCPEndpoint
	dialMCPEndpoint = dial
	return func() { dialMCPEndpoint = orig }
}

// NoLiveMCPEndpointForTest makes every argus MCP endpoint look unserved for the
// rest of the test, whatever listens on its port.
func NoLiveMCPEndpointForTest(t interface{ Cleanup(func()) }) {
	t.Cleanup(SetMCPDialerForTest(func(string, time.Duration) (net.Conn, error) { return nil, net.ErrClosed }))
}

// SyncMCP applies opts to the agent's argus MCP entry. With MCP off it removes
// the entry unless another node serves it at currentURL; with no MCPURL it
// leaves the entry as it is.
func SyncMCP[T any](agent string, opts InstallOptions, currentURL func() string, remove func() (T, error), write func(url, token string) (T, error)) (T, error) {
	var zero T
	switch {
	case opts.MCPOff:
		if MCPEndpointLive(agent, currentURL()) {
			return zero, nil
		}
		return remove()
	case opts.MCPURL == "":
		return zero, nil
	}
	return write(opts.MCPEndpoint(agent), opts.MCPToken)
}

// RestrictTokenFile removes group and other access from the config at path
// before it holds the argus MCP token: any local user who reads the token can
// start agents as the owner. A symlinked path restricts its target.
func RestrictTokenFile(agent, path string) error {
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	fi, err := os.Stat(target)
	if err != nil {
		return err
	}
	mode := fi.Mode().Perm()
	if mode&0o077 == 0 {
		return nil
	}
	if err := os.Chmod(target, mode&^0o077); err != nil {
		return err
	}
	slog.Default().With("scope", agent).Info("restricted the config holding the argus mcp token to its owner", "path", target, "was", mode.String())
	return nil
}

// MCPEndpointLive reports whether something listens at the host:port of the
// agent's argus MCP entry. With MCP off the node has no listener, so a live one
// belongs to another node and its entry must stay.
func MCPEndpointLive(agent, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || u.Port() == "" {
		return false
	}
	c, err := dialMCPEndpoint(u.Host, 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	slog.Default().With("scope", agent).Info("keeping the argus mcp entry: another node serves it", "url", rawURL)
	return true
}
