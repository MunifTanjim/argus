package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/cmd/argus/completion"
	"github.com/MunifTanjim/argus/internal/logbuf"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tui"
)

// newRootCmd builds the argus command tree. The root command with no subcommand
// runs the TUI client; start/hooks/hook attach as subcommands.
// warnE2ESpawnUnsupported notes, when e2e is requested on a path that spawns a local
// node, that E2E is not yet supported there and the connection proceeds in plaintext.
func warnE2ESpawnUnsupported(e2e bool) {
	if e2e {
		shell.StdErrF("argus: --e2e is not yet supported when spawning a local node; connecting in plaintext\n")
	}
}

func newRootCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "argus",
		Short:         "Watch and control all your AI agents",
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

			head, herr := lockGenesisHead(cfg)
			if herr != nil {
				return fail(cmd, fmt.Errorf("lock.genesis is set but unusable (refusing to connect open): %w", herr))
			}

			var client tui.Client
			var logs *logbuf.Buffer
			switch {
			case cfg.Gateway.URL != "":
				// The TUI drives the gateway to see the whole fleet. With no local node,
				// spawn an ephemeral one enrolled on the same gateway so this machine joins
				// too; otherwise the running node enrolls itself.
				if running {
					client, err = connect(ctx, cfg.Gateway.URL, cfg.Token, cfg.Socket, cfg.Gateway.E2E, head)
				} else {
					warnE2ESpawnUnsupported(cfg.Gateway.E2E)
					client, logs, err = connectLocalSpawnWithGateway(ctx, cfg, cfg.Gateway.URL, cfg.Token, cfg.Socket)
				}
			case running:
				client, err = connect(ctx, "", cfg.Token, cfg.Socket, false, nil)
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
				case launchSpawnGateway:
					client, logs, err = connectLocalGateway(ctx, cfg, cfg.Socket)
				case launchSpawnConnected:
					warnE2ESpawnUnsupported(cfg.Gateway.E2E)
					client, logs, err = connectLocalSpawnWithGateway(ctx, cfg, choice.gatewayURL, choice.token, cfg.Socket)
				case launchGateway:
					client, err = connect(ctx, choice.gatewayURL, choice.token, cfg.Socket, cfg.Gateway.E2E, head)
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

	cmd.AddCommand(newStartCmd(version), newSpawnCmd(), newHooksCmd(), newHookCmd(), newPingCmd(), newPairCmd(), newUnpairCmd(), newFocusCmd(), newUpgradeCmd(), newConfigCmd(), newViewCmd(), newLockCmd())

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
