package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/tui"
)

// newViewCmd builds the `argus view <file.argus>` offline bundle-viewer command.
func newViewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "view <file.argus>",
		Short: "View an exported session bundle offline",
		Long: `Opens an exported .argus session bundle in an offline viewer.
No running argus node, gateway, or config file is required.

Note: .argus files contain the session's raw transcript data, including full
tool input and output. Share them only with people you trust.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			if _, err := os.Stat(path); err != nil {
				return fail(cmd, err)
			}
			if err := tui.RunBundle(path); err != nil {
				return fail(cmd, err)
			}
			return nil
		},
	}
	return cmd
}
