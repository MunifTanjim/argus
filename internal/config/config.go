package config

import (
	"errors"
	"fmt"
	"strings"

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
}

type DesktopConfig struct {
	Enabled bool // render native desktop notifications on this node (opt-in)
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

// defaults are the built-in fallback values for unset keys.
var defaults = map[string]any{
	"socket":                        GetRuntimePath("argus.sock"),
	"token":                         "",
	"gateway.url":                   "",
	"gateway.listen-addr":           ":8443",
	"node.id":                       "",
	"node.label":                    "",
	"push.desktop.enabled":          false,
	"log.level":                     "info",
	"log.format":                    "pretty",
	"tunnel.provider":               "",
	"tunnel.cloudflare.token":       "",
	"tunnel.cloudflare.tunnel-name": "",
	"tunnel.cloudflare.hostname":    "",
	"tunnel.external.url":           "",
	"tunnel.zrok.name":              "",
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
	}
}
