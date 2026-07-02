package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// newHookCmd builds `argus hook [--tool <id>] <event>`, invoked by a tool's hook
// (hidden: integration entry point, not user-facing). Reads the payload from
// stdin, enriches it with tmux pane/server from the environment, and forwards to
// argusd via the owning adapter's hook method. --tool selects the adapter and
// defaults to Claude Code, so existing Claude hooks (`argus hook <event>`) work
// unchanged. Strictly best-effort: any failure exits 0 so it never disrupts the
// tool.
func newHookCmd() *cobra.Command {
	var tool string
	cmd := &cobra.Command{
		Use:           "hook <event>",
		Short:         "Deliver a tool hook event to argusd",
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

			cfg, err := resolveConfig(cmd)
			if err != nil {
				shell.Exit(0)
			}

			ev := adapter.HookEvent{
				Event:      event,
				TmuxPane:   os.Getenv("TMUX_PANE"),
				TmuxSocket: tmux.SocketBaseFromEnv(os.Getenv("TMUX")),
				Payload:    payload,
				AutoMode:   os.Getenv("CLAUDE_CODE_ENABLE_AUTO_MODE") == "1",
			}
			hookMethod := adapters.ByTool(tool).HookMethod()

			// PermissionRequest blocks until argusd returns the user's decision, then
			// prints it for Claude Code. Print nothing on failure so Claude falls back to
			// its own prompt. Claude's 600s hook timeout bounds the wait.
			if event == "PermissionRequest" {
				client, err := api.Dial(cfg.Socket)
				if err != nil {
					shell.Exit(0)
				}
				defer client.Close()
				var res api.HookResult
				_ = client.Call(hookMethod, ev, &res)
				if res.Output != "" {
					fmt.Println(res.Output)
				}
				shell.Exit(0)
			}

			done := make(chan struct{})
			go func() {
				defer close(done)
				client, err := api.Dial(cfg.Socket)
				if err != nil {
					return
				}
				defer client.Close()
				_ = client.Call(hookMethod, ev, nil)
			}()

			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
			shell.Exit(0)
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "claude-code", "tool adapter this hook belongs to")
	return cmd
}
