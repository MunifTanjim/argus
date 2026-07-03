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
	"github.com/MunifTanjim/argus/internal/tmux"
)

// newHookCmd builds `argus hook <event>`, invoked by a Claude Code hook (hidden:
// integration entry point, not user-facing). Reads the payload from stdin, enriches it
// with tmux pane/server from the environment, and forwards to argusd. Strictly
// best-effort: any failure exits 0 so it never disrupts Claude Code.
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "hook <event>",
		Short:         "Deliver a Claude Code hook event to argusd",
		Hidden:        true,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, args []string) {
			// --agent selects the adapter. Only claude-code exists today; other agents
			// plug in here. Unknown agent → best-effort no-op so an old hook line never
			// disrupts the client.
			agent, _ := cmd.Flags().GetString("agent")
			if agent != claudecode.Agent {
				shell.Exit(0)
			}
			event := ""
			if len(args) > 0 {
				event = args[0]
			}
			payload, _ := io.ReadAll(io.LimitReader(os.Stdin, 8*1024*1024))

			cfg, err := resolveConfig(cmd)
			if err != nil {
				shell.Exit(0)
			}

			ev := claudecode.HookEvent{
				Agent:      agent,
				Event:      event,
				TmuxPane:   os.Getenv("TMUX_PANE"),
				TmuxSocket: tmux.SocketBaseFromEnv(os.Getenv("TMUX")),
				Payload:    payload,
				AutoMode:   os.Getenv("CLAUDE_CODE_ENABLE_AUTO_MODE") == "1",
			}

			// PermissionRequest blocks until argusd returns the user's decision, then
			// prints it for Claude Code. Print nothing on failure so Claude falls back to
			// its own prompt. claudecode.PermissionRequestHookTimeoutSeconds bounds the wait.
			if event == "PermissionRequest" {
				client, err := api.Dial(cfg.Socket)
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
				client, err := api.Dial(cfg.Socket)
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
	cmd.Flags().String("agent", claudecode.Agent, "coding agent the hook event originates from")
	// Marker flag installed into settings.json commands; parsed and ignored here.
	cmd.Flags().Bool("argus-managed", false, "")
	_ = cmd.Flags().MarkHidden("argus-managed")
	return cmd
}
