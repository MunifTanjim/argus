package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

// newFocusCmd builds `argus _focus <session_id>`: bring tmux to the session's pane on
// this local node. Invoked by a notification click, so it's silent on success and exits
// non-zero on an unknown/non-local session (the expected no-op on a non-owning desktop).
// Always dials the local socket, never the gateway. Hidden: internal, not user-facing.
func newFocusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "_focus <session_id>",
		Short:         "Focus a session's tmux pane (used by notification clicks)",
		Hidden:        true,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			dial, err := gatewayDialer("", "", cfg.Socket) // force local socket
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, err := dial(ctx)
			if err != nil {
				return fail(cmd, err)
			}
			client := api.NewClient(conn)
			defer client.Close()
			if err := client.Call(api.MethodSessionFocus, api.SessionRef{SessionID: args[0]}, nil); err != nil {
				return fail(cmd, err)
			}
			return nil
		},
	}
	cmd.Flags().String("socket", "", "argusd JSON-RPC socket to connect to (default: XDG runtime path)")
	return cmd
}
