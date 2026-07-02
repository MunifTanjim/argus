package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/adapters"
)

// newHooksCmd builds `argus hooks install|uninstall`, the opt-in command that
// writes/removes argus's Claude Code hooks in settings.json.
func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "hooks",
		Short:         "Install or remove the Claude Code hooks",
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
		Short:         "Install argus's Claude Code hooks into settings.json",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			argusBin := bin
			if argusBin == "" {
				argusBin = detectArgusBin()
			}
			for _, a := range adapters.All() {
				path, err := a.SettingsPath()
				if err != nil {
					return fail(cmd, err)
				}
				if err := a.Install(argusBin); err != nil {
					return fail(cmd, err)
				}
				fmt.Printf("installed argus hooks for %s into %s\n", a.Agent(), path)
				fmt.Printf("  events: %v\n", a.DefaultHookEvents())
			}
			fmt.Printf("  command: %s hook <event>\n", argusBin)
			return nil
		},
	}
	cmd.Flags().StringVar(&bin, "bin", "", "path to the argus binary the hooks invoke (default: auto-detect)")
	return cmd
}

func newHooksUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "uninstall",
		Short:         "Remove argus's Claude Code hooks from settings.json",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, a := range adapters.All() {
				path, err := a.SettingsPath()
				if err != nil {
					return fail(cmd, err)
				}
				if err := a.Uninstall(); err != nil {
					return fail(cmd, err)
				}
				fmt.Printf("removed argus hooks for %s from %s\n", a.Agent(), path)
			}
			return nil
		},
	}
}

// detectArgusBin returns the path hooks invoke as `<bin> hook <event>`: this
// executable, falling back to "argus" on PATH if it can't be resolved.
func detectArgusBin() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "argus"
}
