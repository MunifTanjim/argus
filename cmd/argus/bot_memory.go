package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
)

func botMemory(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "memory",
		Short:        "Inspect and manage a bot's memory",
		SilenceUsage: true,
	}
	cmd.AddCommand(
		botMemoryShow(dial), botMemoryLog(dial), botMemoryUndo(dial),
		botMemoryReview(dial), botMemoryImport(dial),
	)
	return cmd
}

func botMemoryShow(dial callerFor) *cobra.Command {
	var profile bool
	return sessionSub(dial, "show <bot>", "Show a bot's memory",
		"  argus bot memory show scout\n  argus bot memory show scout --profile", cobra.ExactArgs(1),
		func(cmd *cobra.Command) { cmd.Flags().BoolVar(&profile, "profile", false, "show the shared profile") },
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			var res api.BotMemoryShowResult
			if err := call(api.MethodBotMemoryShow, api.BotRef{BotID: a.ID}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			if profile {
				shell.StdOutF("%s\n", strings.TrimRight(res.Profile, "\n"))
				return nil
			}
			shell.StdOutF("MEMORY.md %d KB / %d KB\n", (res.Usage+1023)/1024, res.Budget/1024)
			printMemoryFile("MEMORY.md", res.Memory)
			printMemoryFile("USER.md", res.User)
			for _, t := range res.Topics {
				shell.StdOutF("topic: %s\n", t)
			}
			return nil
		})
}

func printMemoryFile(name, content string) {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return
	}
	shell.StdOutF("\n== %s ==\n%s\n", name, content)
}

func botMemoryLog(dial callerFor) *cobra.Command {
	var (
		limit   int
		profile bool
	)
	return sessionSub(dial, "log <bot>", "List memory changes, newest first",
		"  argus bot memory log scout --limit 5", cobra.ExactArgs(1),
		func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.IntVar(&limit, "limit", 20, "number of changes to list; 0 for all")
			f.BoolVar(&profile, "profile", false, "list the shared profile's changes")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			var res api.BotMemoryLogResult
			if err := call(api.MethodBotMemoryLog, api.BotMemoryLogParams{BotID: a.ID, Limit: limit, Profile: profile}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			rows := make([][]string, 0, len(res.Commits))
			for _, c := range res.Commits {
				hash := c.Hash
				if len(hash) > 8 {
					hash = hash[:8]
				}
				rows = append(rows, []string{hash, c.When.Local().Format("2006-01-02 15:04"), c.Source, c.Subject})
			}
			printTable([]string{"HASH", "WHEN", "SOURCE", "SUBJECT"}, rows)
			return nil
		})
}

func botMemoryUndo(dial callerFor) *cobra.Command {
	var profile bool
	return sessionSub(dial, "undo <bot> <commit>", "Revert a memory change",
		"  argus bot memory undo scout 1a2b3c4d", cobra.ExactArgs(2),
		func(cmd *cobra.Command) {
			cmd.Flags().BoolVar(&profile, "profile", false, "revert a change to the shared profile")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			var res api.BotMemoryUndoResult
			if err := call(api.MethodBotMemoryUndo, api.BotMemoryUndoParams{BotID: a.ID, Commit: args[1], Profile: profile}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			shell.StdOutF("reverted %s\n", args[1])
			return nil
		})
}

func proposalSummary(p api.BotMemoryProposal) string {
	if p.Kind == "routine" {
		s := p.Summary
		if s == "" {
			s = p.Slug
		}
		return "routine: " + s
	}
	if p.Kind == "instructions" {
		s := strings.TrimSpace(p.Reason)
		if s == "" {
			s = firstLine(strings.TrimSpace(p.Text))
		}
		return "instructions: " + s
	}
	if len(p.Ops) == 0 {
		return ""
	}
	o := p.Ops[0]
	text := o.Text
	if text == "" {
		text = o.Match
	}
	s := fmt.Sprintf("%s %s", o.Action, firstLine(strings.TrimSpace(text)))
	if len(p.Ops) > 1 {
		s += fmt.Sprintf(" (+%d more)", len(p.Ops)-1)
	}
	return s
}

func botMemoryReview(dial callerFor) *cobra.Command {
	var accept, reject string
	return sessionSub(dial, "review <bot>", "List staged memory changes, or accept or reject one",
		"  argus bot memory review scout\n  argus bot memory review scout --accept <id>", cobra.ExactArgs(1),
		func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.StringVar(&accept, "accept", "", "accept the proposal with this id")
			f.StringVar(&reject, "reject", "", "reject the proposal with this id")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			if accept != "" && reject != "" {
				return errors.New("give --accept or --reject, not both")
			}
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			if accept != "" || reject != "" {
				id := accept
				if id == "" {
					id = reject
				}
				var res api.BotMemoryDecideResult
				if err := call(api.MethodBotMemoryDecide, api.BotMemoryDecideParams{ProposalID: id, Accept: accept != ""}, &res); err != nil {
					return err
				}
				if asJSON {
					return printJSON(res)
				}
				if accept == "" {
					shell.StdOutF("rejected %s\n", id)
					return nil
				}
				shell.StdOutF("accepted %s: applied %d\n", id, res.Applied)
				for _, r := range res.Rejected {
					warn("not applied: " + r)
				}
				return nil
			}
			var res api.BotMemoryProposalsResult
			if err := call(api.MethodBotMemoryProposals, api.BotRef{BotID: a.ID}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			rows := make([][]string, 0, len(res.Proposals))
			for _, p := range res.Proposals {
				kind := p.Kind
				if kind == "" {
					kind = "memory"
				}
				rows = append(rows, []string{p.ID, kind, proposalSummary(p)})
			}
			printTable([]string{"ID", "KIND", "SUMMARY"}, rows)
			return nil
		})
}

func botMemoryImport(dial callerFor) *cobra.Command {
	return sessionSub(dial, "import <bot> <file|->", "Stage memory exported from another assistant for review",
		"  argus bot memory import scout memories.txt\n  cat memories.txt | argus bot memory import scout -", cobra.ExactArgs(2), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			var content []byte
			if args[1] == "-" {
				content, err = io.ReadAll(botStdin)
			} else {
				content, err = os.ReadFile(args[1])
			}
			if err != nil {
				return err
			}
			var res api.BotMemoryImportResult
			if err := call(api.MethodBotMemoryImport, api.BotMemoryImportParams{BotID: a.ID, Content: string(content)}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			shell.StdOutF("staged for review: %d\n", res.Staged)
			return nil
		})
}
