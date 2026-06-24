package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/socketpath"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// newHookCmd builds `argus hook <event>`, invoked by a Claude Code command hook
// (hidden from help — it's an integration entry point, not a user command). It
// reads the hook payload from stdin, enriches it with the tmux pane and server
// derived from the environment, and forwards it to argusd. It is strictly
// best-effort: any failure (node down, timeout) exits 0 so it never disrupts
// Claude Code.
func newHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "hook <event>",
		Short:         "Deliver a Claude Code hook event to argusd",
		Hidden:        true,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, args []string) {
			event := ""
			if len(args) > 0 {
				event = args[0]
			}
			payload, _ := io.ReadAll(io.LimitReader(os.Stdin, 8*1024*1024))

			// Honor a configured socket so a custom path works end-to-end; fall back to
			// the default if config can't be resolved (hook is strictly best-effort).
			socket := socketpath.Default()
			if cfg, err := resolveConfig(cmd); err == nil {
				socket = cfg.Socket
			}

			ev := claudecode.HookEvent{
				Event:      event,
				TmuxPane:   os.Getenv("TMUX_PANE"),
				TmuxSocket: tmux.SocketBaseFromEnv(os.Getenv("TMUX")),
				Payload:    payload,
				AutoMode:   os.Getenv("CLAUDE_CODE_ENABLE_AUTO_MODE") == "1",
			}

			// PermissionRequest is the decision point: block until argusd returns the
			// user's decision, then print it to stdout for Claude Code. If argusd is
			// unreachable or returns nothing, print nothing so Claude falls back to its
			// own interactive prompt. Claude's own 600s hook timeout bounds the wait.
			if event == "PermissionRequest" {
				client, err := api.Dial(socket)
				if err != nil {
					shell.Exit(0)
				}
				defer client.Close()
				var res api.HookResult
				_ = client.Call(claudecode.HookMethod, ev, &res)
				if res.Output != "" {
					fmt.Println(res.Output)
				}
				shell.Exit(0)
			}

			done := make(chan struct{})
			go func() {
				defer close(done)
				client, err := api.Dial(socket)
				if err != nil {
					return
				}
				defer client.Close()
				_ = client.Call(claudecode.HookMethod, ev, nil)
			}()

			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
			shell.Exit(0)
		},
	}
}
