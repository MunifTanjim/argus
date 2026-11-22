package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent/routine"
	"github.com/MunifTanjim/argus/internal/shell"
)

func agentUsage(dial callerFor) *cobra.Command {
	var since string
	return sessionSub(dial, "usage [name]", "Show agents' sessions, cost and tokens by who started the work",
		"  argus agent usage\n  argus agent usage scout --since 7d", cobra.MaximumNArgs(1),
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
			var agents []api.PAgentInfo
			if len(args) == 1 {
				a, err := resolveAgent(call, args[0])
				if err != nil {
					return err
				}
				agents = []api.PAgentInfo{a}
			} else {
				var res api.PAgentListResult
				if err := call(api.MethodPAgentList, api.PAgentListParams{}, &res); err != nil {
					return err
				}
				for _, a := range res.Agents {
					if a.Status != "unreachable" {
						agents = append(agents, a)
					}
				}
			}
			var all []api.PAgentUsageResult
			var rows [][]string
			for _, a := range agents {
				var res api.PAgentUsageResult
				if err := call(api.MethodPAgentUsage, api.PAgentUsageParams{AgentID: a.ID, Since: from}, &res); err != nil {
					return fmt.Errorf("%s: %w", a.Name, err)
				}
				all = append(all, res)
				for _, r := range append(res.Rows, res.Total) {
					rows = append(rows, []string{a.Name, r.StartedBy, strconv.FormatInt(r.Sessions, 10),
						"$" + strconv.FormatFloat(r.Cost, 'f', 2, 64), strconv.FormatInt(r.Tokens, 10)})
				}
			}
			if asJSON {
				return printJSON(map[string]any{"usage": all})
			}
			printTable([]string{"AGENT", "STARTED BY", "SESSIONS", "COST", "TOKENS"}, rows)
			return nil
		})
}

func agentSet(dial callerFor) *cobra.Command {
	return sessionSub(dial, "set <name> <setting> <value>", "Change one setting in an agent's agent.yaml",
		`  argus agent set scout notify all
  argus agent set scout proactivity low
  argus agent set scout memory.mode review
  argus agent set scout quiet-hours 22:00-07:00
  argus agent set scout brief.enabled true
  argus agent set scout notify ""        # back to the default`, cobra.ExactArgs(3), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveAgent(call, args[0])
			if err != nil {
				return err
			}
			var res api.PAgentSetResult
			if err := call(api.MethodPAgentSet, api.PAgentSetParams{AgentID: a.ID, Key: args[1], Value: args[2]}, &res); err != nil {
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
