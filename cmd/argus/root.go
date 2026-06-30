package main

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/cmd/argus/completion"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/logbuf"
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

			// Apply the log level for an embedded node's buffered logs, but unlike
			// `start` don't install the stderr handler (logger.Init): it would corrupt
			// the alt-screen.
			if err := applyLogLevel(cfg); err != nil {
				return fail(cmd, err)
			}

			// Any embedded node spawned below is tied to ctx, so it stops with the TUI.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			running, perr := localNodeRunning(cfg.Socket)
			if perr != nil {
				shell.StdErrF("argus: cannot reach argusd at %s: %v\n", cfg.Socket, perr)
				return errSilent
			}

			var client *api.ReconnectingClient
			var logs *logbuf.Buffer
			switch {
			case cfg.Gateway.URL != "":
				// The TUI drives the gateway to see the whole fleet. With no local node,
				// spawn an ephemeral one enrolled on the same gateway so this machine joins
				// too; otherwise the running node enrolls itself.
				if running {
					client, err = connect(ctx, cfg.Gateway.URL, cfg.Token, cfg.Socket)
				} else {
					client, logs, err = connectLocalSpawnWithGateway(ctx, cfg, cfg.Gateway.URL, cfg.Token, cfg.Socket)
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
					client, logs, err = connectLocalSpawn(ctx, cfg, cfg.Token, cfg.Socket)
				case launchSpawnConnected:
					client, logs, err = connectLocalSpawnWithGateway(ctx, cfg, choice.gatewayURL, choice.token, cfg.Socket)
				case launchGateway:
					client, err = connect(ctx, choice.gatewayURL, choice.token, cfg.Socket)
				}
			}
			if err != nil {
				return errSilent // connect/connectLocalSpawn already printed the diagnostic
			}
			defer client.Close()

			if err := tui.Run(client, logs); err != nil {
				return fail(cmd, err)
			}
			return nil
		},
	}

	cmd.SetVersionTemplate("argus {{.Version}}\n")

	// --config is persistent so subcommands share it. Other flags default to zero; real
	// defaults come from config (viper), so a flag only overrides when set.
	cmd.PersistentFlags().String("config", "", "config file (default: $XDG_CONFIG_HOME/argus/config.yaml) [$ARGUS_CONFIG]")
	// --no-config skips reading any config file (also ignores $ARGUS_CONFIG); defaults+env+flags still apply.
	cmd.PersistentFlags().Bool("no-config", false, "do not read any config file")

	cmd.MarkFlagsMutuallyExclusive("config", "no-config")

	addClientFlags(cmd.Flags())

	cmd.AddCommand(newStartCmd(version), newHooksCmd(), newHookCmd(), newPingCmd(), newPairCmd(), newUnpairCmd(), newFocusCmd(), newUpgradeCmd(), newConfigCmd())

	cmd.InitDefaultCompletionCmd()
	if subcmd, _, _ := cmd.Find([]string{"completion"}); subcmd != nil && subcmd.Name() == "completion" {
		subcmd.AddCommand(completion.InstallCommand())
	}

	return cmd
}

// errSilent is returned by RunE when a command already printed its diagnostics; main()
// exits non-zero but prints nothing more. Its message must stay empty: main() guards on
// identity and never prints it.
var errSilent = errors.New("")

// fail prints a diagnostic prefixed with the command path (e.g. "argus start") and
// returns errSilent, so callers report once and exit non-zero without cobra re-printing.
func fail(cmd *cobra.Command, err error) error {
	shell.StdErrF("%s: %v\n", cmd.CommandPath(), err)
	return errSilent
}
