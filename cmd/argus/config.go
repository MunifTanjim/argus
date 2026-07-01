package main

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/shell"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect argus configuration",
	}
	cmd.AddCommand(newConfigDirCmd())
	return cmd
}

func newConfigDirCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dir",
		Short: "Print the config directory path",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			shell.StdOut(config.ConfigDir)
			shell.StdErrLn()
			return nil
		},
	}
}

// flagKeys maps a cobra flag name to its viper config key, so a set flag overrides
// the corresponding env var and config-file value.
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
	"external-url":           "tunnel.external.url",
	"zrok-name":              "tunnel.zrok.name",
}

// addClientFlags registers the node/gateway-reaching flags shared by the root TUI
// and ping. Defined once so they stay in sync; viper keys live in flagKeys.
func addClientFlags(f *pflag.FlagSet) {
	f.String("socket", "", "argusd JSON-RPC socket to connect to (default: XDG runtime path)")
	f.String("gateway", "", "remote gateway (the /client route is implicit): ws(s)://host, or ssh://[user@]host[:ssh-port][?port=N]; overrides --socket [$ARGUS_GATEWAY_URL]")
	f.String("token", "", "bearer token for the gateway [$ARGUS_TOKEN]")
}

// resolveConfig builds cmd's effective config, layering (lowest first) defaults, the
// config file, env vars, and flags. Config path: --config, else $ARGUS_CONFIG, else
// the default under ConfigDir.
func resolveConfig(cmd *cobra.Command) (*config.Config, error) {
	v := viper.New()

	noConfig, _ := cmd.Flags().GetBool("no-config")

	cfgPath := os.Getenv("ARGUS_CONFIG")
	if f := cmd.Flags().Lookup("config"); f != nil && f.Changed {
		cfgPath = f.Value.String()
	}
	if noConfig {
		cfgPath = "" // also ignore $ARGUS_CONFIG
	}
	if err := config.Load(v, cfgPath, noConfig); err != nil {
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
