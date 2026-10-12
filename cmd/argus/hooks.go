package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/shell"
)

// newHooksCmd builds `argus hooks install|uninstall`, the opt-in command that
// writes/removes argus's Claude Code hooks in settings.json.
func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "hooks",
		Short:         "Install or remove argus's agent integrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Reached only when no subcommand matched; returns a real error (printed by
		// main) rather than errSilent, since there's nothing of its own to print first.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("hooks: a subcommand is required (install or uninstall)")
			}
			return fmt.Errorf("unknown hooks subcommand %q", args[0])
		},
	}
	cmd.AddCommand(newHooksInstallCmd(), newHooksUninstallCmd())
	return cmd
}

func newHooksInstallCmd() *cobra.Command {
	var bin string
	cmd := &cobra.Command{
		Use:           "install",
		Short:         "Install argus's agent integrations (hooks, Claude Code plugin)",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			argusBin := bin
			if argusBin == "" {
				argusBin = detectArgusBin()
			}
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			opts := adapter.InstallOptions{ArgusBin: argusBin, Socket: node.ModSocketPath(cfg.Socket)}
			hooked, failed := false, false
			for _, a := range adapters.All() {
				path, err := a.SettingsPath()
				if err != nil {
					shell.StdErrF("failed to install argus integration for %s: %v\n", a.Agent(), err)
					failed = true
					continue
				}
				if path == "" {
					continue // agent needs no hooks
				}
				// Only install for agents the user actually has set up: a missing config
				// dir means the agent was never run, so don't materialize one for it.
				if dir := filepath.Dir(path); !dirExists(dir) {
					fmt.Printf("skipped %s: %s does not exist (agent not set up)\n", a.Agent(), dir)
					continue
				}
				if err := a.Install(opts); err != nil {
					if errors.Is(err, adapter.ErrAgentNotInstalled) {
						fmt.Printf("skipped %s: not installed\n", a.Agent())
						continue
					}
					shell.StdErrF("failed to install argus integration for %s: %v\n", a.Agent(), err)
					failed = true
					continue
				}
				if events := a.DefaultHookEvents(); len(events) > 0 {
					hooked = true
					fmt.Printf("installed argus hooks for %s into %s\n", a.Agent(), path)
					fmt.Printf("  events: %v\n", events)
				} else {
					fmt.Printf("installed the argus plugin for %s\n", a.Agent())
				}
			}
			if hooked {
				fmt.Printf("  command: %s hook <event>\n", argusBin)
			}
			if failed {
				return errSilent // each failure was already printed
			}
			return nil
		},
	}
	addSocketFlag(cmd.Flags())
	cmd.Flags().StringVar(&bin, "bin", "", "path to the argus binary the hooks invoke (default: auto-detect)")
	return cmd
}

func newHooksUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "uninstall",
		Short:         "Remove argus's agent integrations",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			failed := false
			for _, a := range adapters.All() {
				path, err := a.SettingsPath()
				if err == nil {
					err = a.Uninstall()
				}
				if err != nil {
					shell.StdErrF("failed to remove argus integration for %s: %v\n", a.Agent(), err)
					failed = true
					continue
				}
				if path == "" {
					fmt.Printf("removed argus hooks for %s\n", a.Agent())
					continue
				}
				if len(a.DefaultHookEvents()) == 0 {
					fmt.Printf("removed argus integration for %s\n", a.Agent())
					continue
				}
				fmt.Printf("removed argus hooks for %s from %s\n", a.Agent(), path)
			}
			if failed {
				return errSilent // each failure was already printed
			}
			return nil
		},
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func detectArgusBin() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "argus"
}
