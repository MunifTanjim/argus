package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/adapter/pi"
)

// newPiHostCmd builds the hidden `argus pi-host --socket <path> [--cwd <dir>] -- <argv...>`:
// it holds the stdio of one `pi --mode rpc` process and serves it on a unix
// socket, so argusd can restart without stopping pi.
func newPiHostCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "pi-host --socket <path> [--cwd <dir>] -- <argv...>",
		Short:         "Relay a pi RPC process's stdio over a unix socket",
		Hidden:        true,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, args []string) {
			socket, _ := cmd.Flags().GetString("socket")
			cwd, _ := cmd.Flags().GetString("cwd")
			os.Exit(pi.RunHost(socket, cwd, args))
		},
	}
	cmd.Flags().String("socket", "", "unix socket to serve pi's stdio on")
	cmd.Flags().String("cwd", "", "directory the relay reports as pi's cwd")
	_ = cmd.MarkFlagRequired("socket")
	return cmd
}
