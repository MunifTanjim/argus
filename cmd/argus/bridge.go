package main

import (
	"fmt"
	"io"
	"net"

	"github.com/spf13/cobra"
)

// newBridgeCmd builds the hidden `argus bridge`: it connects stdin/stdout to the local
// node's socket (resolved from config exactly as the node does). A remote client runs it
// over ssh (`ssh <host> argus bridge`); the ssh login is the authorization.
func newBridgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "bridge",
		Short:         "Connect stdio to the local node socket (used over ssh)",
		Hidden:        true,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			conn, err := net.Dial("unix", cfg.Socket)
			if err != nil {
				return fail(cmd, fmt.Errorf("node socket unavailable: %w", err))
			}
			defer conn.Close()

			// Stdin ending half-closes the socket so the node can finish replying; the
			// bridge ends when the node closes its side.
			go func() {
				_, _ = io.Copy(conn, cmd.InOrStdin())
				if uc, ok := conn.(*net.UnixConn); ok {
					_ = uc.CloseWrite()
				}
			}()
			if _, err := io.Copy(cmd.OutOrStdout(), conn); err != nil {
				return fail(cmd, err)
			}
			return nil
		},
	}
	addSocketFlag(cmd.Flags())
	return cmd
}
