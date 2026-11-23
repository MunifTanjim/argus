package main

import (
	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
)

func botTrust(dial callerFor) *cobra.Command {
	return sessionSub(dial, "trust <name>", "Accept changes made outside Argus to a bot's protected files",
		"  argus bot trust scout", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, call, err := botOn(cmd, call, args[0])
			if err != nil {
				return err
			}
			var res api.BotTrustResult
			if err := call(api.MethodBotTrust, api.BotRef{BotID: a.ID}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			if len(res.Accepted) == 0 {
				shell.StdOutF("%s: nothing had changed\n", a.Name)
				return nil
			}
			shell.StdOutF("%s: accepted\n", a.Name)
			for _, c := range res.Accepted {
				shell.StdOutF("  %s\n", c)
			}
			return nil
		})
}
