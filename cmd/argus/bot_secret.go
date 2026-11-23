package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
)

func botSecret(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage a bot's secrets",
		Long: `Manage a bot's secrets.

A secret is given to the bot's sessions as an environment variable. The bot
and the tools it runs can read it; it is kept out of the bot's files, memory
and history.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(botSecretSet(dial), botSecretList(dial), botSecretRm(dial))
	return cmd
}

// readSecretValue reads a value from the terminal without echo, or all of
// standard input when it is not a terminal.
var readSecretValue = func(name string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		shell.StdErrF("Value for %s: ", name)
		v, err := term.ReadPassword(fd)
		shell.StdErrLn()
		return string(v), err
	}
	return readPipedSecret(os.Stdin)
}

// readPipedSecret reads a piped value whole, so multi-line values (PEM keys,
// JSON credentials) stay intact, and drops one trailing newline.
func readPipedSecret(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	v := strings.TrimSuffix(string(data), "\n")
	return strings.TrimSuffix(v, "\r"), nil
}

func botSecretSet(dial callerFor) *cobra.Command {
	var description string
	return sessionSub(dial, "set <bot> <NAME>", "Store a secret; the value is read from standard input",
		"  argus bot secret set scout GH_TOKEN --description \"GitHub token for issues\"\n  printf %s \"$TOKEN\" | argus bot secret set scout GH_TOKEN", cobra.ExactArgs(2),
		func(cmd *cobra.Command) {
			cmd.Flags().StringVar(&description, "description", "", "what the secret is for; the bot sees this")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			value, err := readSecretValue(args[1])
			if err != nil {
				return fmt.Errorf("read value: %w", err)
			}
			if value == "" {
				return errors.New("no value given")
			}
			if err := call(api.MethodBotSecretSet, api.BotSecretSetParams{BotID: a.ID, Name: args[1], Description: description, Value: value}, nil); err != nil {
				return err
			}
			if asJSON {
				return printJSON(map[string]string{"name": args[1]})
			}
			shell.StdOutF("stored %s for %s\n", args[1], a.Name)
			return nil
		})
}

func botSecretList(dial callerFor) *cobra.Command {
	return sessionSub(dial, "list <bot>", "List a bot's secrets (names only)",
		"  argus bot secret list scout", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			var res api.BotSecretListResult
			if err := call(api.MethodBotSecretList, api.BotRef{BotID: a.ID}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			rows := make([][]string, 0, len(res.Secrets))
			for _, s := range res.Secrets {
				rows = append(rows, []string{s.Name, s.UpdatedAt.Local().Format("2006-01-02 15:04"), s.Description})
			}
			printTable([]string{"NAME", "UPDATED", "DESCRIPTION"}, rows)
			return nil
		})
}

func botSecretRm(dial callerFor) *cobra.Command {
	return sessionSub(dial, "rm <bot> <NAME>", "Remove a secret",
		"  argus bot secret rm scout GH_TOKEN", cobra.ExactArgs(2), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			if err := call(api.MethodBotSecretRm, api.BotSecretRef{BotID: a.ID, Name: args[1]}, nil); err != nil {
				return err
			}
			if asJSON {
				return printJSON(map[string]string{"removed": args[1]})
			}
			shell.StdOutF("removed %s from %s\n", args[1], a.Name)
			return nil
		})
}
