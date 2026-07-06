package config_test

import (
	"os"
	"path/filepath"
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
	c := load(t, "")
	if c.Token != "envtok" {
		t.Errorf("ARGUS_TOKEN should map to token, got %q", c.Token)
	}
	if c.Tunnel.Cloudflare.Hostname != "h.example.com" || c.Tunnel.Cloudflare.TunnelName != "tn" {
		t.Errorf("preserved cloudflare env names not mapped: %+v", c.Tunnel.Cloudflare)
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
