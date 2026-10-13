package main

import (
	"context"
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/cmd/argus/completion"
	"github.com/MunifTanjim/argus/internal/selfupgrade"
	"github.com/MunifTanjim/argus/internal/shell"
)

// newUpgradeCmd builds `argus upgrade`: download the latest release binary for the
// current platform and atomically swap it over the running executable.
func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "upgrade",
		Short:         "Upgrade to the latest version",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			return upgradeInProcess(ctx, cmd)
		},
	}
}

func upgradeInProcess(ctx context.Context, cmd *cobra.Command) error {
	up, err := selfupgrade.New(cmd.Root().Version)
	if err != nil {
		return fail(cmd, err)
	}
	c, err := up.Check(ctx)
	if err != nil {
		return fail(cmd, err)
	}
	shell.StdErrF("Current version: %s\n", c.Current)
	shell.StdErrF("Latest version: %s\n", c.Latest)
	if c.Latest == c.Current {
		shell.StdErrLn("Already on the latest version!")
		return nil
	}
	shell.StdErrF("Downloading %s...\n", c.Latest)
	_, to, err := up.Apply(ctx)
	if err != nil {
		return fail(cmd, err)
	}
	printUpgraded(cmd, to)
	return nil
}

// printUpgraded reports success before refreshing completion, so a completion
// problem does not mask that the upgrade itself succeeded.
func printUpgraded(cmd *cobra.Command, tag string) {
	shell.StdErrF("Upgraded to %s!\n", tag)
	switch err := completion.Install(cmd); {
	case err == nil:
		shell.StdErrLn("Updated shell completion!")
	case errors.Is(err, completion.ErrCompletionNotEnabled):
	default:
		shell.StdErrF("Warning: failed to refresh shell completion: %v\n", err)
	}
}
