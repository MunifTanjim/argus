package main

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/cmd/argus/completion"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tui"
)

// newRootCmd builds the argus command tree. The root command with no subcommand
// runs the TUI client; start/hooks/hook attach as subcommands.
func newRootCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "argus",
		Short:         "Watch and control all your AI coding sessions — from one place",
		Version:       version,
		Args:          cobra.NoArgs, // no subcommand → run the TUI; reject stray args
		SilenceUsage:  true,
		SilenceErrors: true,
		// Default action (no subcommand): run the TUI client.
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}

			// Any embedded node spawned below is tied to this context, so it stops
			// when the TUI exits — nothing lingers.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			running, perr := localNodeRunning(cfg.Socket)
			if perr != nil {
				shell.StdErrF("argus: cannot reach argusd at %s: %v\n", cfg.Socket, perr)
				return errSilent
			}

			var client *api.ReconnectingClient
			switch {
			case cfg.Gateway.URL != "":
				// The TUI drives the gateway so it sees the whole fleet. If no local node
				// is running, spawn an ephemeral one that enrolls with the same gateway, so
				// this machine joins the fleet too; otherwise the running node enrolls itself.
				if running {
					client, err = connect(ctx, cfg.Gateway.URL, cfg.Token, cfg.Socket)
				} else {
					client, err = connectLocalSpawnWithGateway(ctx, cfg.Gateway.URL, cfg.Token, cfg.Socket)
				}
			case running:
				client, err = connect(ctx, "", cfg.Token, cfg.Socket)
			default:
				choice, lerr := runLauncher(cfg.Token)
				if lerr != nil {
					return fail(cmd, lerr)
				}
				switch choice.kind {
				case launchQuit:
					return nil
				case launchSpawnIsolated:
					client, err = connectLocalSpawn(ctx, cfg.Token, cfg.Socket)
				case launchSpawnConnected:
					client, err = connectLocalSpawnWithGateway(ctx, choice.gatewayURL, choice.token, cfg.Socket)
				case launchGateway:
					client, err = connect(ctx, choice.gatewayURL, choice.token, cfg.Socket)
				}
			}
			if err != nil {
				return errSilent // connect/connectLocalSpawn already printed the diagnostic
			}
			defer client.Close()

			if err := tui.Run(client); err != nil {
				return fail(cmd, err)
			}
			return nil
		},
	}

	cmd.SetVersionTemplate("argus {{.Version}}\n")

	// --config is persistent so subcommands (start, hook) share it. Other flags default
	// to zero values; real defaults come from config (viper), so a flag only overrides
	// config/env when set. See resolveConfig / config.Load.
	cmd.PersistentFlags().String("config", "", "config file (default: $XDG_CONFIG_HOME/argus/config.yaml) [$ARGUS_CONFIG]")

	addClientFlags(cmd.Flags())

	cmd.AddCommand(newStartCmd(version), newHooksCmd(), newHookCmd(), newPingCmd(), newPairCmd(), newUnpairCmd(), newFocusCmd())

	cmd.InitDefaultCompletionCmd()
	if subcmd, _, _ := cmd.Find([]string{"completion"}); subcmd != nil && subcmd.Name() == "completion" {
		subcmd.AddCommand(completion.InstallCommand())
	}

	return cmd
}

// errSilent is returned by RunE when a command has already printed its own
// diagnostics; main() still exits non-zero, but cobra prints nothing more
// (the root command sets SilenceErrors). Its message must stay empty: main()
// guards on identity and never prints it.
var errSilent = errors.New("")

// fail prints a diagnostic prefixed with the command path (e.g. "argus start") and
// returns errSilent, so callers report once and exit non-zero without cobra
// re-printing. Centralizes the StdErrF + errSilent pattern and keeps prefixes correct.
func fail(cmd *cobra.Command, err error) error {
	shell.StdErrF("%s: %v\n", cmd.CommandPath(), err)
	return errSilent
}
