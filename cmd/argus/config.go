package main

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/MunifTanjim/argus/internal/config"
)

// flagKeys maps a cobra flag name to its viper config key, so a set flag overrides the
// corresponding env var and config-file value.
var flagKeys = map[string]string{
	"socket":                 "socket",
	"gateway":                "gateway.url",
	"token":                  "token",
	"listen-addr":            "gateway.listen-addr",
	"id":                     "node.id",
	"label":                  "node.label",
	"log-level":              "log.level",
	"log-format":             "log.format",
	"tunnel":                 "tunnel.provider",
	"cloudflare-token":       "tunnel.cloudflare.token",
	"cloudflare-tunnel-name": "tunnel.cloudflare.tunnel-name",
	"cloudflare-hostname":    "tunnel.cloudflare.hostname",
}

// addClientFlags registers the flags shared by client-side commands (the root TUI and
// ping) for reaching a node or gateway. Defined once so the two stay in sync; their
// viper keys live in flagKeys.
func addClientFlags(f *pflag.FlagSet) {
	f.String("socket", "", "argusd JSON-RPC socket to connect to (default: XDG runtime path)")
	f.String("gateway", "", "remote gateway (the /client route is implicit): ws(s)://host, or ssh://[user@]host[:ssh-port][?port=N]; overrides --socket [$ARGUS_GATEWAY_URL]")
	f.String("token", "", "bearer token for the gateway [$ARGUS_TOKEN]")
}

// resolveConfig builds the effective configuration for cmd, layering (lowest priority
// first) built-in defaults, the config file, environment variables, and this command's
// flags. The config file path comes from --config, else $ARGUS_CONFIG, else the default
// under ConfigDir.
func resolveConfig(cmd *cobra.Command) (*config.Config, error) {
	v := viper.New()

	cfgPath := os.Getenv("ARGUS_CONFIG")
	if f := cmd.Flags().Lookup("config"); f != nil && f.Changed {
		cfgPath = f.Value.String()
	}
	if err := config.Load(v, cfgPath); err != nil {
		return nil, err
	}

	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if key, ok := flagKeys[f.Name]; ok {
			_ = v.BindPFlag(key, f)
		}
	})

	c := config.FromViper(v)
	return &c, nil
}
