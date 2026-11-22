package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

func agentActivity(dial callerFor) *cobra.Command {
	var (
		unattended bool
		limit      int
	)
	return sessionSub(dial, "activity <agent>", "List an agent's approval decisions, newest first",
		"  argus agent activity scout\n  argus agent activity scout --unattended", cobra.ExactArgs(1),
		func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.BoolVar(&unattended, "unattended", false, "only work you did not start")
			f.IntVar(&limit, "limit", 50, "number of decisions to list")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveAgent(call, args[0])
			if err != nil {
				return err
			}
			var res api.PAgentActivityResult
			if err := call(api.MethodPAgentActivity, api.PAgentActivityParams{AgentID: a.ID, Unattended: unattended, Limit: limit}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			rows := make([][]string, 0, len(res.Activity))
			for _, r := range res.Activity {
				action := strings.TrimSpace(r.Tool + " " + r.Target)
				if len(r.Class) > 0 {
					action += " [" + strings.Join(r.Class, ",") + "]"
				}
				rows = append(rows, []string{
					r.CreatedAt.Local().Format("2006-01-02 15:04"), r.StartedBy, action,
					r.Decision, r.DecidedBy, r.Reason,
				})
			}
			printTable([]string{"WHEN", "STARTED BY", "ACTION", "DECISION", "BY", "REASON"}, rows)
			return nil
		})
}
