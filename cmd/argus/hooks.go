package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
)

// newHooksCmd builds `argus hooks install|uninstall`, the opt-in command that
// writes/removes argus's Claude Code hooks in settings.json.
func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "hooks",
		Short:         "Install or remove the Claude Code hooks",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Reached only when no install/uninstall subcommand matched. Unlike the other
		// commands, this returns a real error (printed by main) rather than the
		// errSilent path — there is nothing of its own to print first.
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
			path, err := claudecode.SettingsPath()
			if err != nil {
				return fail(cmd, err)
			}
			argusBin := bin
			if argusBin == "" {
				argusBin = detectArgusBin()
			}
			if err := claudecode.Install(argusBin, claudecode.DefaultHookEvents); err != nil {
				return fail(cmd, err)
			}
			fmt.Printf("installed argus hooks into %s\n", path)
			fmt.Printf("  events: %v\n", claudecode.DefaultHookEvents)
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
			path, err := claudecode.SettingsPath()
			if err != nil {
				return fail(cmd, err)
			}
			if err := claudecode.Uninstall(); err != nil {
				return fail(cmd, err)
			}
			fmt.Printf("removed argus hooks from %s\n", path)
			return nil
		},
	}
}

// detectArgusBin returns the path hooks should invoke as `<bin> hook <event>`:
// this executable itself (the unified argus binary), falling back to "argus" on
// PATH if the path can't be resolved.
func detectArgusBin() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "argus"
}
