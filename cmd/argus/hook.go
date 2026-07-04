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

// newHookCmd builds the hidden `argus hook` command. Best-effort: any failure exits 0.
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "hook <event>",
		Short:         "Deliver a tool hook event to argusd",
		Hidden:        true,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, args []string) {
			agent, _ := cmd.Flags().GetString("agent")
			a := adapters.ByAgent(agent)
			if a == nil {
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

			ev := adapter.HookEvent{
				Agent:      agent,
				Event:      event,
				TmuxPane:   os.Getenv("TMUX_PANE"),
				TmuxSocket: tmux.SocketBaseFromEnv(os.Getenv("TMUX")),
				Payload:    payload,
				AutoMode:   os.Getenv("CLAUDE_CODE_ENABLE_AUTO_MODE") == "1",
				Env:        captureEnv([]string{"ANTIGRAVITY_CONVERSATION_ID"}),
			}

			// Blocking: wait for argusd's decision; on failure print nothing so the
			// tool falls back to its own prompt.
			if a.ShouldBlock(ev) {
				client, err := api.Dial(cfg.Socket)
				if err != nil {
					shell.Exit(0)
				}
				defer client.Close()
				var res api.HookResult
				_ = client.Call(adapter.HookMethod, ev, &res)
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
				_ = client.Call(adapter.HookMethod, ev, nil)
			}()

			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
			shell.Exit(0)
		},
	}
	cmd.Flags().String("agent", adapters.Default().Agent(), "coding agent the hook event originates from")
	// Marker flag installed into settings.json commands; parsed and ignored here.
	cmd.Flags().Bool("argus-managed", false, "")
	_ = cmd.Flags().MarkHidden("argus-managed")
	return cmd
}

func captureEnv(keys []string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
