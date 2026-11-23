package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot/routine"
	"github.com/MunifTanjim/argus/internal/shell"
)

func botUsage(dial callerFor) *cobra.Command {
	var since string
	return sessionSub(dial, "usage [name]", "Show bots' sessions, cost and tokens by who started the work",
		"  argus bot usage\n  argus bot usage scout --since 7d", cobra.MaximumNArgs(1),
		func(cmd *cobra.Command) {
			cmd.Flags().StringVar(&since, "since", "", "only work started this long ago or later, such as 7d or 12h")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			var from time.Time
			if since != "" {
				d, err := routine.ParseDuration(since)
				if err != nil || d <= 0 {
					return fmt.Errorf("--since must be a duration such as 7d or 12h")
				}
				from = time.Now().Add(-d)
			}
			bots, err := botsFor(cmd, call, args)
			if err != nil {
				return err
			}
			var all []api.BotUsageResult
			var rows [][]string
			for _, a := range bots {
				var res api.BotUsageResult
				if err := onNode(call, a.NodeID)(api.MethodBotUsage, api.BotUsageParams{BotID: a.ID, Since: from}, &res); err != nil {
					return fmt.Errorf("%s: %w", a.Name, err)
				}
				all = append(all, res)
				for _, r := range append(res.Rows, res.Total) {
					rows = append(rows, []string{a.Name, botNodeLabel(a), r.StartedBy, strconv.FormatInt(r.Sessions, 10),
						"$" + strconv.FormatFloat(r.Cost, 'f', 2, 64), strconv.FormatInt(r.Tokens, 10)})
				}
			}
			if asJSON {
				return printJSON(map[string]any{"usage": all})
			}
			printTable([]string{"BOT", "NODE", "STARTED BY", "SESSIONS", "COST", "TOKENS"}, rows)
			return nil
		})
}

func botSet(dial callerFor) *cobra.Command {
	return sessionSub(dial, "set <name> <setting> <value>", "Change one setting in a bot's bot.yaml",
		`  argus bot set scout notify all
  argus bot set scout proactivity low
  argus bot set scout memory.mode review
  argus bot set scout quiet-hours 22:00-07:00
  argus bot set scout brief.enabled true
  argus bot set scout notify ""        # back to the default`, cobra.ExactArgs(3), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, call, err := botOn(cmd, call, args[0])
			if err != nil {
				return err
			}
			var res api.BotSetResult
			if err := call(api.MethodBotSet, api.BotSetParams{BotID: a.ID, Key: args[1], Value: args[2]}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			if strings.TrimSpace(args[2]) == "" {
				shell.StdOutF("%s: %s back to the default\n", a.Name, args[1])
			} else {
				shell.StdOutF("%s: %s = %s\n", a.Name, args[1], args[2])
			}
			return nil
		})
}
