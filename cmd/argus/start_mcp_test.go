package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/node"
)

type fakeMCPListener struct {
	calls int
	port  int
	token string
	err   error
}

func (f *fakeMCPListener) ListenMCP(port int, token string) error {
	f.calls++
	f.port, f.token = port, token
	return f.err
}

func TestStartMCPBusyPortLeavesEntries(t *testing.T) {
	tempStateDir(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port

	var logs bytes.Buffer
	cfg := &config.Config{MCP: config.MCPConfig{Port: port}}
	opts := startMCP(cfg, node.New(), adapter.InstallOptions{ArgusBin: "argus"}, slog.New(slog.NewTextHandler(&logs, nil)))
	if opts.MCPURL != "" || opts.MCPToken != "" || opts.MCPOff || opts.ArgusBin != "argus" {
		t.Fatalf("opts = %+v", opts)
	}
	if out := logs.String(); strings.Count(out, "level=ERROR") != 1 || !strings.Contains(out, fmt.Sprintf("port=%d", port)) {
		t.Fatalf("log = %q", out)
	}
}

func TestStartMCPListensBeforeSettingURL(t *testing.T) {
	tempStateDir(t)
	f := &fakeMCPListener{}
	opts := startMCP(&config.Config{MCP: config.MCPConfig{Port: 7734}}, f, adapter.InstallOptions{}, slog.New(slog.DiscardHandler))
	if f.calls != 1 || f.port != 7734 || f.token == "" || f.token != opts.MCPToken || opts.MCPURL != "http://127.0.0.1:7734/mcp" {
		t.Fatalf("listener %+v, opts %+v", f, opts)
	}

	f = &fakeMCPListener{err: errors.New("address already in use")}
	opts = startMCP(&config.Config{MCP: config.MCPConfig{Port: 7734}}, f, adapter.InstallOptions{}, slog.New(slog.DiscardHandler))
	if f.calls != 1 || opts.MCPURL != "" || opts.MCPOff {
		t.Fatalf("listener %+v, opts %+v", f, opts)
	}
}

func TestStartMCPPortZeroDoesNotListen(t *testing.T) {
	tempStateDir(t)
	f := &fakeMCPListener{}
	opts := startMCP(&config.Config{}, f, adapter.InstallOptions{}, slog.New(slog.DiscardHandler))
	if f.calls != 0 || !opts.MCPOff || opts.MCPURL != "" {
		t.Fatalf("listener %+v, opts %+v", f, opts)
	}
	if _, err := os.Stat(config.GetStatePath("mcp.token")); !os.IsNotExist(err) {
		t.Fatalf("token created with mcp off: %v", err)
	}
}

func TestStartMCPTokenErrorDoesNotListen(t *testing.T) {
	tempStateDir(t)
	if err := os.Mkdir(config.GetStatePath("mcp.token"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := &fakeMCPListener{}
	opts := startMCP(&config.Config{MCP: config.MCPConfig{Port: 7734}}, f, adapter.InstallOptions{}, slog.New(slog.DiscardHandler))
	if f.calls != 0 || opts.MCPOff || opts.MCPURL != "" {
		t.Fatalf("listener %+v, opts %+v", f, opts)
	}
}
