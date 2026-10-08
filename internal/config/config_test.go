package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/MunifTanjim/argus/internal/config"
)

// isolateConfigDir points ConfigDir at an empty temp dir so the default-path lookup
// can't pick up a real ~/.config/argus/config.yaml on the dev machine.
func isolateConfigDir(t *testing.T) {
	t.Helper()
	orig := config.ConfigDir
	config.ConfigDir = t.TempDir()
	t.Cleanup(func() { config.ConfigDir = orig })
}

func load(t *testing.T, path string) config.Config {
	t.Helper()
	v := viper.New()
	if err := config.Load(v, path, false); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return config.FromViper(v)
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	isolateConfigDir(t)
	c := load(t, "")
	if c.Gateway.ListenAddr != ":8443" {
		t.Errorf("gateway.listen-addr = %q, want :8443", c.Gateway.ListenAddr)
	}
	if c.Log.Level != "info" || c.Log.Format != "pretty" {
		t.Errorf("log = %+v, want info/pretty", c.Log)
	}
}

func TestFileNested(t *testing.T) {
	path := writeConfig(t, `
token: filetok
gateway:
  listen-addr: "127.0.0.1:9000"
log:
  level: debug
  format: json
tunnel:
  provider: cloudflare
  cloudflare:
    hostname: argus.example.com
    tunnel-name: desk
`)
	c := load(t, path)
	if c.Gateway.ListenAddr != "127.0.0.1:9000" || c.Token != "filetok" {
		t.Errorf("gateway=%+v token=%q", c.Gateway, c.Token)
	}
	if c.Log.Level != "debug" || c.Log.Format != "json" {
		t.Errorf("log = %+v", c.Log)
	}
	if c.Tunnel.Provider != "cloudflare" || c.Tunnel.Cloudflare.Hostname != "argus.example.com" || c.Tunnel.Cloudflare.TunnelName != "desk" {
		t.Errorf("tunnel = %+v", c.Tunnel)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	path := writeConfig(t, "gateway:\n  listen-addr: \":8443\"\n")
	t.Setenv("ARGUS_GATEWAY_LISTEN_ADDR", ":9999")
	if got := load(t, path).Gateway.ListenAddr; got != ":9999" {
		t.Errorf("gateway.listen-addr = %q, want env :9999 to override file", got)
	}
}

func TestPreservedEnvNames(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("ARGUS_TOKEN", "envtok")
	t.Setenv("ARGUS_CLOUDFLARE_HOSTNAME", "h.example.com")
	t.Setenv("ARGUS_CLOUDFLARE_TUNNEL_NAME", "tn")
	t.Setenv("ARGUS_NGROK_DOMAIN", "argus.ngrok.app")
	c := load(t, "")
	if c.Token != "envtok" {
		t.Errorf("ARGUS_TOKEN should map to token, got %q", c.Token)
	}
	if c.Tunnel.Cloudflare.Hostname != "h.example.com" || c.Tunnel.Cloudflare.TunnelName != "tn" {
		t.Errorf("preserved cloudflare env names not mapped: %+v", c.Tunnel.Cloudflare)
	}
	if c.Tunnel.Ngrok.Domain != "argus.ngrok.app" {
		t.Errorf("ARGUS_NGROK_DOMAIN should map to tunnel.ngrok.domain, got %q", c.Tunnel.Ngrok.Domain)
	}
}

func TestArgusGatewayEnv(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("ARGUS_GATEWAY_URL", "wss://gateway.example.com")
	c := load(t, "")
	if c.Gateway.URL != "wss://gateway.example.com" {
		t.Errorf("ARGUS_GATEWAY_URL should map to gateway.url, got %q", c.Gateway.URL)
	}
}

func TestModeRoundTrips(t *testing.T) {
	isolateConfigDir(t)
	if got := load(t, "").Mode; got != "" {
		t.Errorf("default mode = %q, want empty", got)
	}

	path := writeConfig(t, "mode: gateway\n")
	if got := load(t, path).Mode; got != "gateway" {
		t.Errorf("file mode = %q, want gateway", got)
	}

	t.Setenv("ARGUS_MODE", "node")
	if got := load(t, "").Mode; got != "node" {
		t.Errorf("ARGUS_MODE should map to mode, got %q", got)
	}

	v := viper.New()
	if err := config.Load(v, path, false); err != nil {
		t.Fatalf("Load: %v", err)
	}
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("mode", "", "")
	_ = fs.Set("mode", "node")
	_ = v.BindPFlag("mode", fs.Lookup("mode"))
	if got := config.FromViper(v).Mode; got != "node" {
		t.Errorf("mode = %q, want flag 'node' to override file", got)
	}
}

func TestFlagOverridesEnv(t *testing.T) {
	t.Setenv("ARGUS_GATEWAY_LISTEN_ADDR", ":9999")
	v := viper.New()
	if err := config.Load(v, "", false); err != nil {
		t.Fatalf("Load: %v", err)
	}
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("listen-addr", "", "")
	_ = fs.Set("listen-addr", ":7000")
	_ = v.BindPFlag("gateway.listen-addr", fs.Lookup("listen-addr"))
	if got := config.FromViper(v).Gateway.ListenAddr; got != ":7000" {
		t.Errorf("gateway.listen-addr = %q, want flag :7000 to override env", got)
	}
}

func TestExplicitMissingErrors(t *testing.T) {
	v := viper.New()
	if err := config.Load(v, filepath.Join(t.TempDir(), "nope.yaml"), false); err == nil {
		t.Error("explicit missing config path should error")
	}
}

func TestDefaultMissingOK(t *testing.T) {
	isolateConfigDir(t)
	v := viper.New()
	if err := config.Load(v, "", false); err != nil {
		t.Errorf("missing default config should not error: %v", err)
	}
}

func TestPushDesktopDefaultFalse(t *testing.T) {
	isolateConfigDir(t)
	if c := load(t, ""); c.Push.Desktop.Enabled {
		t.Fatal("push.desktop.enabled default = true, want false")
	}
}

func TestPushDesktopFromFile(t *testing.T) {
	path := writeConfig(t, "push:\n  desktop:\n    enabled: true\n")
	if c := load(t, path); !c.Push.Desktop.Enabled {
		t.Fatal("push.desktop.enabled from file = false, want true")
	}
}

func TestPushDesktopFromEnv(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("ARGUS_PUSH_DESKTOP_ENABLED", "true")
	if c := load(t, ""); !c.Push.Desktop.Enabled {
		t.Fatal("push.desktop.enabled from env = false, want true")
	}
}

func TestPushMobileDelayDefaultZero(t *testing.T) {
	isolateConfigDir(t)
	if c := load(t, ""); c.Push.Mobile.Delay != 0 {
		t.Fatalf("push.mobile.delay default = %v, want 0", c.Push.Mobile.Delay)
	}
}

func TestPushMobileDelayFromFile(t *testing.T) {
	path := writeConfig(t, "push:\n  mobile:\n    delay: 30s\n")
	if c := load(t, path); c.Push.Mobile.Delay != 30*time.Second {
		t.Fatalf("push.mobile.delay from file = %v, want 30s", c.Push.Mobile.Delay)
	}
}

func TestPushMobileDelayFromEnv(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("ARGUS_PUSH_MOBILE_DELAY", "45s")
	if c := load(t, ""); c.Push.Mobile.Delay != 45*time.Second {
		t.Fatalf("push.mobile.delay from env = %v, want 45s", c.Push.Mobile.Delay)
	}
}

func TestTmuxMirrorSessionAffixDefaults(t *testing.T) {
	isolateConfigDir(t)
	c := load(t, "")
	if c.Tmux.MirrorSessionPrefix != "_" || c.Tmux.MirrorSessionSuffix != "_" {
		t.Fatalf("want _/_ got %q/%q", c.Tmux.MirrorSessionPrefix, c.Tmux.MirrorSessionSuffix)
	}
}

func TestValidateRejectsTmuxHostileAffixes(t *testing.T) {
	// Defaults are valid.
	valid := config.Config{Tmux: config.TmuxConfig{MirrorSessionPrefix: "_", MirrorSessionSuffix: "_"}, TUI: config.TUIConfig{Mouse: config.MouseOff}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid affixes should pass: %v", err)
	}
	// tmux session names can't contain '.' or ':'; a bad affix would silently
	// break mirror creation, so it must be caught at load.
	bad := []config.TmuxConfig{
		{MirrorSessionPrefix: "a.b", MirrorSessionSuffix: "_"},
		{MirrorSessionPrefix: "_", MirrorSessionSuffix: "x:y"},
	}
	for _, tc := range bad {
		if err := (config.Config{Tmux: tc, TUI: config.TUIConfig{Mouse: config.MouseOff}}).Validate(); err == nil {
			t.Errorf("affix %+v should be rejected", tc)
		}
	}
}

func TestE2EEDefaultFalse(t *testing.T) {
	isolateConfigDir(t)
	if c := load(t, ""); c.E2EE.Enabled {
		t.Fatal("e2ee.enabled default = true, want false")
	}
}

func TestE2EEFromFile(t *testing.T) {
	path := writeConfig(t, "e2ee:\n  enabled: true\n")
	if c := load(t, path); !c.E2EE.Enabled {
		t.Fatal("e2ee.enabled from file = false, want true")
	}
}

func TestE2EEFromEnv(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("ARGUS_E2EE_ENABLED", "true")
	if c := load(t, ""); !c.E2EE.Enabled {
		t.Fatal("e2ee.enabled from env = false, want true")
	}
}

func TestLockGenesisDefaultEmpty(t *testing.T) {
	isolateConfigDir(t)
	if c := load(t, ""); c.Lock.Genesis != "" {
		t.Fatalf("lock.genesis default = %q, want empty", c.Lock.Genesis)
	}
}

func TestLockGenesisFromFile(t *testing.T) {
	path := writeConfig(t, "lock:\n  genesis: abc123\n")
	if c := load(t, path); c.Lock.Genesis != "abc123" {
		t.Fatalf("lock.genesis from file = %q, want abc123", c.Lock.Genesis)
	}
}

func TestWorktreeDirTemplateDefault(t *testing.T) {
	isolateConfigDir(t)
	if got := load(t, "").Workspace.WorktreeDirTemplate; got != ".worktrees/{{.Branch.Slug}}" {
		t.Errorf("WorktreeDirTemplate = %q, want .worktrees/{{.Branch.Slug}}", got)
	}
}

func TestIssueBranchTemplateDefault(t *testing.T) {
	isolateConfigDir(t)
	if got := load(t, "").Workspace.IssueBranchTemplate; got != "issue-{{.Issue.Number}}-{{.Issue.Slug}}" {
		t.Errorf("IssueBranchTemplate = %q, want issue-{{.Issue.Number}}-{{.Issue.Slug}}", got)
	}
}

func TestAutoAdoptDirs(t *testing.T) {
	isolateConfigDir(t)
	for _, tc := range []struct {
		name, file, env string
		want            []string
	}{
		{name: "default", want: []string{"~"}},
		{name: "file", file: "workspace:\n  auto-adopt-dirs: [~/Dev, /srv/src]\n", want: []string{"~/Dev", "/srv/src"}},
		{name: "empty", file: "workspace:\n  auto-adopt-dirs: []\n", want: []string{}},
		{name: "env is ignored", env: "/srv/src", want: []string{"~"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := ""
			if tc.file != "" {
				path = writeConfig(t, tc.file)
			}
			if tc.env != "" {
				t.Setenv("ARGUS_WORKSPACE_AUTO_ADOPT_DIRS", tc.env)
			}
			if got := load(t, path).Workspace.AutoAdoptDirs; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("AutoAdoptDirs = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestKeyTimeoutDefault(t *testing.T) {
	isolateConfigDir(t)
	if c := load(t, ""); c.TUI.KeyTimeout != time.Second {
		t.Errorf("tui.key-timeout = %v, want 1s", c.TUI.KeyTimeout)
	}
}

func TestLeaderDefault(t *testing.T) {
	isolateConfigDir(t)
	if c := load(t, ""); c.TUI.LeaderKey != "<Space>" {
		t.Errorf("tui.leader-key = %q, want <Space>", c.TUI.LeaderKey)
	}
}

func TestMouseDefaultOn(t *testing.T) {
	isolateConfigDir(t)
	if c := load(t, ""); c.TUI.Mouse != config.MouseOn {
		t.Errorf("tui.mouse = %q, want on", c.TUI.Mouse)
	}
}

func TestVerboseTranscriptDefaultOff(t *testing.T) {
	isolateConfigDir(t)
	if c := load(t, ""); c.TUI.VerboseTranscript {
		t.Error("tui.verbose-transcript = true, want false by default")
	}
}

func TestVerboseTranscriptFromFile(t *testing.T) {
	isolateConfigDir(t)
	if c := load(t, writeConfig(t, "tui:\n  verbose-transcript: true\n")); !c.TUI.VerboseTranscript {
		t.Error("tui.verbose-transcript: true not honoured")
	}
}

func TestMouseOffFromFile(t *testing.T) {
	isolateConfigDir(t)
	if c := load(t, writeConfig(t, "tui:\n  mouse: off\n")); c.TUI.Mouse != config.MouseOff {
		t.Errorf("tui.mouse = %q, want off", c.TUI.Mouse)
	}
}

func TestValidateRejectsUnknownMouse(t *testing.T) {
	tmux := config.TmuxConfig{MirrorSessionPrefix: "_", MirrorSessionSuffix: "_"}
	for _, mode := range []config.MouseMode{config.MouseOn, config.MouseOff} {
		if err := (config.Config{Tmux: tmux, TUI: config.TUIConfig{Mouse: mode}}).Validate(); err != nil {
			t.Errorf("tui.mouse %q should pass: %v", mode, err)
		}
	}
	for _, mode := range []config.MouseMode{"true", "yes", ""} {
		if err := (config.Config{Tmux: tmux, TUI: config.TUIConfig{Mouse: mode}}).Validate(); err == nil {
			t.Errorf("tui.mouse %q should be rejected", mode)
		}
	}
}

func TestReadKeymapsKeepsCaseAndDots(t *testing.T) {
	path := writeConfig(t, `
tui:
  key-timeout: 500ms
  keymap:
    global:
      "g.": toggle show-hidden
      "G": goto bottom
      "z": ""
      "x":
    transcript:
      "gt": goto top
      "n": 5
`)
	km, err := config.ReadKeymaps(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{
		"global":     {"g.": "toggle show-hidden", "G": "goto bottom", "z": "", "x": ""},
		"transcript": {"gt": "goto top", "n": "5"},
	}
	if !reflect.DeepEqual(km, want) {
		t.Errorf("keymaps = %v, want %v", km, want)
	}
	if c := load(t, path); c.TUI.KeyTimeout != 500*time.Millisecond {
		t.Errorf("tui.key-timeout = %v, want 500ms", c.TUI.KeyTimeout)
	}
}

func TestReadKeymapsWithoutFile(t *testing.T) {
	km, err := config.ReadKeymaps("")
	if err != nil || km != nil {
		t.Errorf("no file should give no keymaps: %v %v", km, err)
	}
}

func TestReadKeymapsBadShape(t *testing.T) {
	path := writeConfig(t, "tui:\n  keymap:\n    global: 5\n")
	if _, err := config.ReadKeymaps(path); err == nil {
		t.Error("a section that is not a map should fail")
	}
}
