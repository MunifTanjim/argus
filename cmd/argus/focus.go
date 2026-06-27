package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

// newFocusCmd builds `argus _focus <session_id>`: bring the user's tmux client to
// the given session's pane on this (local) node. It is invoked by a desktop
// notification click, so on success it is silent; an unknown/non-local session
// is a non-zero exit (the expected no-op on a non-owning desktop). It always
// dials the local node socket, never the gateway. Hidden: it's an internal hook
// for notification clicks, not a command users run by hand.
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
