package main

import (
	"bufio"
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

func agentSecret(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage an agent's secrets",
		Long: `Manage an agent's secrets.

A secret is given to the agent's sessions as an environment variable. The agent
and the tools it runs can read it; it is kept out of the agent's files, memory
and history.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(agentSecretSet(dial), agentSecretList(dial), agentSecretRm(dial))
	return cmd
}

// readSecretValue reads a value from the terminal without echo, or the first
// line of standard input when it is not a terminal.
var readSecretValue = func(name string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		shell.StdErrF("Value for %s: ", name)
		v, err := term.ReadPassword(fd)
		shell.StdErrLn()
		return string(v), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func agentSecretSet(dial callerFor) *cobra.Command {
	var description string
	return sessionSub(dial, "set <agent> <NAME>", "Store a secret; the value is read from standard input",
		"  argus agent secret set scout GH_TOKEN --description \"GitHub token for issues\"\n  printf %s \"$TOKEN\" | argus agent secret set scout GH_TOKEN", cobra.ExactArgs(2),
		func(cmd *cobra.Command) {
			cmd.Flags().StringVar(&description, "description", "", "what the secret is for; the agent sees this")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveAgent(call, args[0])
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
			if err := call(api.MethodPAgentSecretSet, api.PAgentSecretSetParams{AgentID: a.ID, Name: args[1], Description: description, Value: value}, nil); err != nil {
				return err
			}
			if asJSON {
				return printJSON(map[string]string{"name": args[1]})
			}
			shell.StdOutF("stored %s for %s\n", args[1], a.Name)
			return nil
		})
}

func agentSecretList(dial callerFor) *cobra.Command {
	return sessionSub(dial, "list <agent>", "List an agent's secrets (names only)",
		"  argus agent secret list scout", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveAgent(call, args[0])
			if err != nil {
				return err
			}
			var res api.PAgentSecretListResult
			if err := call(api.MethodPAgentSecretList, api.PAgentRef{AgentID: a.ID}, &res); err != nil {
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

func agentSecretRm(dial callerFor) *cobra.Command {
	return sessionSub(dial, "rm <agent> <NAME>", "Remove a secret",
		"  argus agent secret rm scout GH_TOKEN", cobra.ExactArgs(2), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveAgent(call, args[0])
			if err != nil {
				return err
			}
			if err := call(api.MethodPAgentSecretRm, api.PAgentSecretRef{AgentID: a.ID, Name: args[1]}, nil); err != nil {
				return err
			}
			if asJSON {
				return printJSON(map[string]string{"removed": args[1]})
			}
			shell.StdOutF("removed %s from %s\n", args[1], a.Name)
			return nil
		})
}
