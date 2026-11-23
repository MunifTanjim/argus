package main

import (
	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
)

func newSecretCmd() *cobra.Command { return secretCmd(localCallerFor) }

func secretCmd(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Read the secrets of the bot running this command",
		Long: `Read the secrets of the bot running this command.

Only a command a bot's session runs can read a secret, and only one of that
bot's own.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(secretGet(dial))
	return cmd
}

func secretGet(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "get <NAME>",
		Short:         "Print a secret's value, with no trailing newline",
		Example:       "  GH_TOKEN=$(argus secret get GH_TOKEN) gh issue list",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			call, closeConn, err := dial(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			defer closeConn()
			var res api.BotSecretGetResult
			if err := call(api.MethodBotSecretGet, api.BotSecretGetParams{Caller: callerInfo(), Name: args[0]}, &res); err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("%s", res.Value)
			return nil
		},
	}
	addSocketFlag(cmd.Flags())
	return cmd
}
