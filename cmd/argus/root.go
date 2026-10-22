package main

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/cmd/argus/completion"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/logbuf"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tui"
)

// newRootCmd builds the argus command tree. The root command with no subcommand
// runs the TUI client; start/hooks/hook attach as subcommands.
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
			cfg, err := resolveTUIConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}

			// Reject an invalid log level before the TUI takes the screen.
			if err := applyLogLevel(cfg); err != nil {
				return fail(cmd, err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			running, perr := localNodeRunning(cfg.Socket)
			if perr != nil {
				shell.StdErrF("argus: cannot reach argusd at %s: %v\n", cfg.Socket, perr)
				return errSilent
			}
			gwSocket := gatewaySocketPath(cfg.Socket)
			gatewayRunning, perr := localNodeRunning(gwSocket)
			if perr != nil {
				shell.StdErrF("argus: cannot reach the gateway at %s: %v\n", gwSocket, perr)
				return errSilent
			}

			st, err := loadLaunchState(cfg.Socket)
			if err != nil {
				return fail(cmd, err)
			}
			action, stale := decideAttach(st, running, gatewayRunning)
			if action == attachPartial {
				shell.StdErrF("argus: some servers that the TUI started are not running; run 'argus stop' first\n")
				return errSilent
			}
			if stale {
				if err := removeLaunchState(cfg.Socket); err != nil {
					return fail(cmd, err)
				}
				st = nil
			}

			var head []byte
			if cfg.E2EE.Enabled {
				if head, err = resolveTrustHead(cfg); err != nil {
					return fail(cmd, err)
				}
			}

			spec := tuiSpec(cmd, cfg)
			var client tui.Client
			var gatewayURL string // empty: the TUI drives a local socket
			var spawned *launchState
			switch {
			case action == attachState:
				gatewayURL = st.GatewayURL
				attachHead := head
				switch st.Mode {
				case launchModeIsolated:
					attachHead = nil
				case launchModeGateway:
					if attachHead, err = resolveTrustHead(cfg); err != nil {
						return fail(cmd, err)
					}
				}
				client, err = connect(ctx, cfg, st.GatewayURL, st.Token, cfg.Socket, attachHead)
			case cfg.Gateway.URL != "":
				// The TUI drives the gateway to see the whole fleet. With no local node,
				// start one enrolled on the same gateway so this machine joins too;
				// otherwise the running node enrolls itself.
				gatewayURL = cfg.Gateway.URL
				if running {
					client, err = connect(ctx, cfg, cfg.Gateway.URL, cfg.Token, cfg.Socket, head)
				} else {
					client, _, err = connectLocalSpawnWithGateway(ctx, cfg, spec, cfg.Gateway.URL, cfg.Token, head)
					spawned = &launchState{Mode: launchModeConnected, GatewayURL: cfg.Gateway.URL, Token: cfg.Token}
				}
			case running:
				client, err = connect(ctx, cfg, "", cfg.Token, cfg.Socket, nil)
			default:
				choice, lerr := runLauncher(cfg.Token)
				if lerr != nil {
					return fail(cmd, lerr)
				}
				switch choice.kind {
				case launchQuit:
					return nil
				case launchSpawnIsolated:
					client, _, err = connectLocalSpawn(ctx, cfg, spec)
					spawned = &launchState{Mode: launchModeIsolated}
				case launchSpawnGateway:
					client, _, gatewayURL, err = connectLocalGateway(ctx, cfg, spec)
					spawned = &launchState{Mode: launchModeGateway, GatewayURL: gatewayURL, Token: cfg.Token}
				case launchSpawnConnected:
					gatewayURL = choice.gatewayURL
					client, _, err = connectLocalSpawnWithGateway(ctx, cfg, spec, choice.gatewayURL, choice.token, head)
					spawned = &launchState{Mode: launchModeConnected, GatewayURL: choice.gatewayURL, Token: choice.token}
				case launchGateway:
					gatewayURL = choice.gatewayURL
					client, err = connect(ctx, cfg, choice.gatewayURL, choice.token, cfg.Socket, head)
				}
			}
			if err != nil {
				return errSilent // connect/connectLocalSpawn already printed the diagnostic
			}
			defer client.Close()

			if spawned != nil {
				if err := saveLaunchState(cfg.Socket, *spawned); err != nil {
					return fail(cmd, err)
				}
				st = spawned
			}

			var logs *logbuf.Buffer
			if st != nil {
				logs = logbuf.New(1000)
				go followLog(ctx, serverLogPath(cfg.Socket, roleNode), logs)
				if st.needsGateway() {
					go followLog(ctx, serverLogPath(cfg.Socket, roleGateway), logs)
				}
			}

			if err := tui.Run(client, gatewayURL, logs, cfg.TUI); err != nil {
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

	cmd.AddCommand(newStartCmd(version), newSpawnCmd(), newHooksCmd(), newHookCmd(), newPingCmd(), newPairCmd(), newUnpairCmd(), newFocusCmd(), newUpgradeCmd(), newStopCmd(), newConfigCmd(), newViewCmd(), newLockCmd(), newProjectCmd(), newWorkspaceCmd(), newPushPortCmd(pushPortAppID, pushPortBaseURL))

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
	shell.StdErrF("%s", gatewayAuthHint(err))
	return errSilent
}

// gatewayAuthHint names the three places a gateway token comes from, for the one
// failure whose cause the transport error cannot state: the gateway answered, and
// refused this caller. Returns "" for every other error.
func gatewayAuthHint(err error) string {
	var authErr *api.DialAuthError
	if !errors.As(err, &authErr) {
		return ""
	}
	if authErr.TokenSent {
		return "  the gateway expects a different token: check --token, $ARGUS_TOKEN, or token: in the config file\n"
	}
	return "  no token was configured: pass --token, set $ARGUS_TOKEN, or set token: in the config file\n"
}

// tuiSpec gives the managed servers the pretty format with color, because the
// Logs tab shows their lines.
func tuiSpec(cmd *cobra.Command, cfg *config.Config) spawnSpec {
	return spawnSpec{
		socket:     cfg.Socket,
		configArgs: configArgs(cmd),
		extraArgs:  append([]string{"--log-format=pretty"}, setFlags("log-level", cfg.Log.Level)...),
		extraEnv:   []string{"CLICOLOR_FORCE=1"},
	}
}
