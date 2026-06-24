package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/viper"

	"github.com/MunifTanjim/argus/internal/socketpath"
)

// Config is the resolved argus configuration. Values come from (highest priority
// first) command-line flags, environment variables, the config file, then built-in
// defaults.
type Config struct {
	Socket  string
	Token   string // shared gateway token: presented by clients/nodes, required by the gateway
	Gateway GatewayConfig
	Node    NodeConfig
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

type LogConfig struct {
	Level  string
	Format string
}

type TunnelConfig struct {
	Provider   string
	Cloudflare CloudflareConfig
}

type CloudflareConfig struct {
	Token      string
	TunnelName string
	Hostname   string
}

// defaults are the built-in values, used when a key is set by neither a flag, an env
// var, nor the config file.
var defaults = map[string]any{
	"socket":                        socketpath.Default(),
	"token":                         "",
	"gateway.url":                   "",
	"gateway.listen-addr":           ":8443",
	"node.id":                       "",
	"node.label":                    "",
	"log.level":                     "info",
	"log.format":                    "pretty",
	"tunnel.provider":               "",
	"tunnel.cloudflare.token":       "",
	"tunnel.cloudflare.tunnel-name": "",
	"tunnel.cloudflare.hostname":    "",
}

// Load configures v with argus's defaults, environment binding, and config file.
// configPath, when non-empty, is read instead of the default ConfigDir/config.yaml.
//
// A missing default file is not an error; a missing explicit configPath is. Callers
// may BindPFlag command flags onto v before reading values, so flags win over env,
// which wins over the file, which wins over defaults.
func Load(v *viper.Viper, configPath string) error {
	for key, val := range defaults {
		v.SetDefault(key, val)
	}

	v.SetEnvPrefix("ARGUS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	// Preserve historical env var names where the derived ARGUS_<KEY_PATH> would differ.
	// (token derives to ARGUS_TOKEN automatically as a top-level key.)
	_ = v.BindEnv("tunnel.cloudflare.token", "ARGUS_CLOUDFLARE_TOKEN")
	_ = v.BindEnv("tunnel.cloudflare.tunnel-name", "ARGUS_CLOUDFLARE_TUNNEL_NAME")
	_ = v.BindEnv("tunnel.cloudflare.hostname", "ARGUS_CLOUDFLARE_HOSTNAME")
	// gateway.url derives automatically to ARGUS_GATEWAY_URL via SetEnvKeyReplacer + AutomaticEnv.

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

// FromViper reads the resolved values out of v into a Config. Using explicit Get
// calls (rather than Unmarshal) keeps bound-flag precedence predictable.
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
		},
	}
}
