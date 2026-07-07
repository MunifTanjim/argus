package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the resolved argus configuration. Precedence: flags > env > file > defaults.
type Config struct {
	Socket  string
	Token   string // shared gateway token: presented by clients/nodes, required by the gateway
	Gateway GatewayConfig
	Node    NodeConfig
	Push    PushConfig
	Log     LogConfig
	Tunnel  TunnelConfig
	Tmux    TmuxConfig
}

type GatewayConfig struct {
	URL        string
	ListenAddr string
}

type NodeConfig struct {
	ID    string
	Label string
}

type PushConfig struct {
	Desktop DesktopConfig
	Mobile  MobileConfig
}

type DesktopConfig struct {
	Enabled bool // render native desktop notifications on this node (opt-in)
}

// MobileConfig tunes device (mobile) push delivery.
type MobileConfig struct {
	// Delay is the grace period before a mobile push fires. 0 (default) or any
	// non-positive value fires immediately. Re-checked when it elapses: sent
	// only if the session is still awaiting input or idle.
	Delay time.Duration
}

type LogConfig struct {
	Level  string
	Format string
}

type TunnelConfig struct {
	Provider   string
	Cloudflare CloudflareConfig
	External   ExternalConfig
	Zrok       ZrokConfig
}

type CloudflareConfig struct {
	Token      string
	TunnelName string
	Hostname   string
}

type ExternalConfig struct {
	URL string // gateway's public URL when the tunnel is managed outside argus
}

type ZrokConfig struct {
	Name string // reserved name selection ("namespace:name" or "name") for a stable URL
}

// TmuxConfig names the tmux mirror sessions spawned for terminal.* calls. The
// prefix and suffix wrap the "argus-mirror-<termID>" marker so a reaper can
// match them without touching unrelated sessions.
type TmuxConfig struct {
	MirrorSessionPrefix string
	MirrorSessionSuffix string
}

// defaults are the built-in fallback values for unset keys.
var defaults = map[string]any{
	"socket":                        GetRuntimePath("argus.sock"),
	"token":                         "",
	"gateway.url":                   "",
	"gateway.listen-addr":           ":8443",
	"node.id":                       "",
	"node.label":                    "",
	"push.desktop.enabled":          false,
	"push.mobile.delay":             "0s",
	"log.level":                     "info",
	"log.format":                    "pretty",
	"tunnel.provider":               "",
	"tunnel.cloudflare.token":       "",
	"tunnel.cloudflare.tunnel-name": "",
	"tunnel.cloudflare.hostname":    "",
	"tunnel.external.url":           "",
	"tunnel.zrok.name":              "",
	"tmux.mirror-session-prefix":    "_",
	"tmux.mirror-session-suffix":    "_",
}

// Load configures v with argus's defaults, env binding, and config file. configPath,
// when non-empty, is read instead of the default ConfigDir/config.yaml. skipFile skips
// reading any config file, leaving only defaults and env.
// A missing default file is not an error; a missing explicit configPath is.
func Load(v *viper.Viper, configPath string, skipFile bool) error {
	for key, val := range defaults {
		v.SetDefault(key, val)
	}

	v.SetEnvPrefix("ARGUS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	// Preserve historical env var names where the derived ARGUS_<KEY_PATH> would differ.
	_ = v.BindEnv("tunnel.cloudflare.token", "ARGUS_CLOUDFLARE_TOKEN")
	_ = v.BindEnv("tunnel.cloudflare.tunnel-name", "ARGUS_CLOUDFLARE_TUNNEL_NAME")
	_ = v.BindEnv("tunnel.cloudflare.hostname", "ARGUS_CLOUDFLARE_HOSTNAME")
	_ = v.BindEnv("tunnel.external.url", "ARGUS_EXTERNAL_URL")
	_ = v.BindEnv("tunnel.zrok.name", "ARGUS_ZROK_NAME")

	if skipFile {
		return nil
	}

	v.SetConfigType("yaml")
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath(ConfigDir)
	}

	if err := v.ReadInConfig(); err != nil {
		if configPath != "" {
			return fmt.Errorf("read config %s: %w", configPath, err)
		}
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return fmt.Errorf("read config: %w", err)
		}
	}
	return nil
}

// Validate checks resolved values for constraints that would otherwise fail
// opaquely at runtime. Returns the first problem found.
func (c Config) Validate() error {
	// tmux session names cannot contain ':' or '.' (target-spec separators), so a
	// mirror-session affix carrying either would silently break mirror creation.
	for _, a := range []struct{ name, val string }{
		{"tmux.mirror-session-prefix", c.Tmux.MirrorSessionPrefix},
		{"tmux.mirror-session-suffix", c.Tmux.MirrorSessionSuffix},
	} {
		if strings.ContainsAny(a.val, ":.") {
			return fmt.Errorf("%s %q must not contain ':' or '.' (tmux session name constraint)", a.name, a.val)
		}
	}
	return nil
}

// FromViper reads resolved values out of v into a Config. Explicit Get calls (not
// Unmarshal) keep bound-flag precedence predictable.
func FromViper(v *viper.Viper) Config {
	return Config{
		Socket: v.GetString("socket"),
		Token:  v.GetString("token"),
		Gateway: GatewayConfig{
			URL:        v.GetString("gateway.url"),
			ListenAddr: v.GetString("gateway.listen-addr"),
		},
		Node: NodeConfig{
			ID:    v.GetString("node.id"),
			Label: v.GetString("node.label"),
		},
		Push: PushConfig{
			Desktop: DesktopConfig{
				Enabled: v.GetBool("push.desktop.enabled"),
			},
			Mobile: MobileConfig{
				Delay: v.GetDuration("push.mobile.delay"),
			},
		},
		Log: LogConfig{
			Level:  v.GetString("log.level"),
			Format: v.GetString("log.format"),
		},
		Tunnel: TunnelConfig{
			Provider: v.GetString("tunnel.provider"),
			Cloudflare: CloudflareConfig{
				Token:      v.GetString("tunnel.cloudflare.token"),
				TunnelName: v.GetString("tunnel.cloudflare.tunnel-name"),
				Hostname:   v.GetString("tunnel.cloudflare.hostname"),
			},
			External: ExternalConfig{
				URL: v.GetString("tunnel.external.url"),
			},
			Zrok: ZrokConfig{
				Name: v.GetString("tunnel.zrok.name"),
			},
		},
		Tmux: TmuxConfig{
			MirrorSessionPrefix: v.GetString("tmux.mirror-session-prefix"),
			MirrorSessionSuffix: v.GetString("tmux.mirror-session-suffix"),
		},
	}
}
