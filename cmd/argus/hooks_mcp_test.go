package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/config"
)

type fakeAgent struct {
	adapter.Adapter
	name        string
	settings    string
	events      []string
	installErr  error
	installed   bool
	uninstalled bool
}

func (f *fakeAgent) Agent() string                 { return f.name }
func (f *fakeAgent) SettingsPath() (string, error) { return f.settings, nil }
func (f *fakeAgent) DefaultHookEvents() []string   { return f.events }
func (f *fakeAgent) Install(adapter.InstallOptions) error {
	f.installed = f.installErr == nil
	return f.installErr
}
func (f *fakeAgent) Uninstall() error { f.uninstalled = true; return nil }

func TestMCPDisabledWithPortZero(t *testing.T) {
	cfg := &config.Config{}
	opts, err := mcpInstallOptions(cfg, adapter.InstallOptions{ArgusBin: "argus"})
	if err != nil || !opts.MCPOff || opts.MCPURL != "" || opts.MCPToken != "" || opts.ArgusBin != "argus" {
		t.Fatalf("opts = %+v, err = %v", opts, err)
	}
}

func TestMCPTokenErrorIsNotOff(t *testing.T) {
	tempStateDir(t)
	if err := os.Mkdir(config.GetStatePath("mcp.token"), 0o700); err != nil {
		t.Fatal(err)
	}
	opts, err := mcpInstallOptions(&config.Config{MCP: config.MCPConfig{Port: 7734}}, adapter.InstallOptions{})
	if err == nil || opts.MCPOff || opts.MCPURL != "" || opts.MCPToken != "" {
		t.Fatalf("opts = %+v, err = %v", opts, err)
	}
}

func TestInstallAgentMCPOnly(t *testing.T) {
	on := adapter.InstallOptions{MCPURL: "http://127.0.0.1:7734/mcp", MCPToken: "t"}

	a := &fakeAgent{name: "codex"}
	line, ok, _ := installAgent(a, adapter.InstallOptions{})
	if !ok || a.installed || line != "skipped codex: no argus mcp token\n" {
		t.Fatalf("no token: %q ok=%v installed=%v", line, ok, a.installed)
	}

	line, ok, _ = installAgent(a, adapter.InstallOptions{MCPOff: true})
	if !ok || !a.installed || line != "argus mcp server is off; removed it from codex\n" {
		t.Fatalf("off: %q ok=%v installed=%v", line, ok, a.installed)
	}
	a.installed = false

	line, ok, _ = installAgent(a, on)
	if !ok || !a.installed || line != "installed the argus mcp server for codex\n" {
		t.Fatalf("on: %q ok=%v installed=%v", line, ok, a.installed)
	}

	missing := &fakeAgent{name: "opencode", installErr: adapter.ErrAgentNotInstalled}
	line, ok, _ = installAgent(missing, on)
	if !ok || line != "skipped opencode: not installed\n" {
		t.Fatalf("missing: %q ok=%v", line, ok)
	}

	broken := &fakeAgent{name: "opencode", installErr: errors.New("boom")}
	line, ok, _ = installAgent(broken, on)
	if ok || !strings.Contains(line, "failed to install argus integration for opencode: boom") {
		t.Fatalf("broken: %q ok=%v", line, ok)
	}
}

func TestInstallAgentSkipsMissingSettingsDir(t *testing.T) {
	a := &fakeAgent{name: "claude", settings: t.TempDir() + "/nope/settings.json"}
	line, ok, _ := installAgent(a, adapter.InstallOptions{})
	if !ok || a.installed || !strings.HasPrefix(line, "skipped claude:") {
		t.Fatalf("%q ok=%v installed=%v", line, ok, a.installed)
	}
}

func TestUninstallAgentMessages(t *testing.T) {
	mcp := &fakeAgent{name: "codex"}
	if line, err := uninstallAgent(mcp); err != nil || !mcp.uninstalled || line != "removed the argus mcp server for codex\n" {
		t.Fatalf("%q %v", line, err)
	}
	hooks := &fakeAgent{name: "claude", settings: "/s.json", events: []string{"Stop"}}
	if line, _ := uninstallAgent(hooks); line != "removed argus hooks for claude from /s.json\n" {
		t.Fatalf("%q", line)
	}
}
