package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
)

func newRoutineCmd() *cobra.Command { return routineCmd(localCallerFor) }

func routineCmd(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "routine",
		Short: "Inspect and run bots' routines and goals",
		Long: `Inspect and run bots' routines and goals.

Routines live in files under a bot's routines/ folder; edit those to change
them. A routine is named <bot>/<slug>, for example scout/inbox.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(
		routineList(dial), routineShow(dial), routineRun(dial), routinePause(dial),
		routineResume(dial), routineRuns(dial), routineHookSecret(dial),
	)
	return cmd
}

// resolveRoutine splits "<bot>/<slug>" and resolves the bot.
func resolveRoutine(call caller, ref string) (api.BotInfo, string, error) {
	i := strings.LastIndex(ref, "/")
	if i <= 0 || i == len(ref)-1 {
		return api.BotInfo{}, "", fmt.Errorf("name a routine as <bot>/<slug>, got %q", ref)
	}
	a, err := resolveBot(call, ref[:i])
	return a, ref[i+1:], err
}

func whenText(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

func usedText(used, limit float64, money bool) string {
	f := func(v float64) string {
		if money {
			return "$" + strconv.FormatFloat(v, 'f', 2, 64)
		}
		return strconv.FormatInt(int64(v), 10)
	}
	if limit <= 0 {
		return f(used)
	}
	return f(used) + "/" + f(limit)
}

func routineState(r api.BotRoutineInfo) string {
	if r.State == "paused" && r.PausedReason != "" {
		return "paused (" + r.PausedReason + ")"
	}
	return r.State
}

func routineList(dial callerFor) *cobra.Command {
	return sessionSub(dial, "list [bot]", "List routines",
		"  argus routine list\n  argus routine list scout", cobra.MaximumNArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			var bots []api.BotInfo
			if len(args) == 1 {
				a, err := resolveBot(call, args[0])
				if err != nil {
					return err
				}
				bots = []api.BotInfo{a}
			} else {
				var res api.BotListResult
				if err := call(api.MethodBotList, api.BotListParams{}, &res); err != nil {
					return err
				}
				for _, a := range res.Bots {
					if a.Status != "unreachable" {
						bots = append(bots, a)
					}
				}
			}
			all := api.BotRoutineListResult{Routines: []api.BotRoutineInfo{}}
			var rows [][]string
			for _, a := range bots {
				var res api.BotRoutineListResult
				if err := call(api.MethodBotRoutineList, api.BotRef{BotID: a.ID}, &res); err != nil {
					return fmt.Errorf("%s: %w", a.Name, err)
				}
				all.Routines = append(all.Routines, res.Routines...)
				for _, r := range res.Routines {
					rows = append(rows, []string{
						a.Name + "/" + r.Slug, routineState(r), strings.Join(r.Starts, "; "), whenText(r.NextDue), r.LastResult,
						usedText(float64(r.Used.Sessions), float64(r.Budget.Sessions), false),
						usedText(r.Used.Cost, r.Budget.Cost, true),
					})
				}
			}
			if asJSON {
				return printJSON(all)
			}
			printTable([]string{"ROUTINE", "STATE", "STARTS", "NEXT", "LAST", "SESSIONS", "COST"}, rows)
			return nil
		})
}

func findRoutine(call caller, ref string) (api.BotInfo, api.BotRoutineInfo, error) {
	a, slug, err := resolveRoutine(call, ref)
	if err != nil {
		return a, api.BotRoutineInfo{}, err
	}
	var res api.BotRoutineListResult
	if err := call(api.MethodBotRoutineList, api.BotRef{BotID: a.ID}, &res); err != nil {
		return a, api.BotRoutineInfo{}, err
	}
	for _, r := range res.Routines {
		if r.Slug == slug {
			return a, r, nil
		}
	}
	return a, api.BotRoutineInfo{}, fmt.Errorf("unknown routine %q", ref)
}

func routineFields(r api.BotRoutineInfo) [][2]string {
	last := r.LastResult
	if r.ConsecutiveFailures > 0 {
		last += fmt.Sprintf(" (%d failures in a row)", r.ConsecutiveFailures)
	}
	return [][2]string{
		{"what", r.What}, {"starts", strings.Join(r.Starts, "; ")}, {"does", r.Does}, {"until", r.Until},
		{"outcome", r.Outcome}, {"verify", r.Verify}, {"state", routineState(r)}, {"next", whenText(r.NextDue)},
		{"last run", whenText(r.LastRunAt)}, {"last result", strings.TrimSpace(last)},
		{"sessions", usedText(float64(r.Used.Sessions), float64(r.Budget.Sessions), false)},
		{"cost", usedText(r.Used.Cost, r.Budget.Cost, true)},
		{"tokens", usedText(float64(r.Used.Tokens), float64(r.Budget.Tokens), false)},
		{"error", r.Error},
	}
}

func routineShow(dial callerFor) *cobra.Command {
	return sessionSub(dial, "show <bot>/<slug>", "Show a routine",
		"  argus routine show scout/inbox", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			_, r, err := findRoutine(call, args[0])
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(r)
			}
			printFields(args[0], routineFields(r))
			return nil
		})
}

func routineRun(dial callerFor) *cobra.Command {
	return sessionSub(dial, "run <bot>/<slug>", "Run a routine now, as a test run",
		"  argus routine run scout/inbox", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, slug, err := resolveRoutine(call, args[0])
			if err != nil {
				return err
			}
			var res api.BotRoutineRunResult
			if err := call(api.MethodBotRoutineRun, api.BotRoutineRef{BotID: a.ID, Slug: slug}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			shell.StdOutF("started %s (run %s); see `argus routine runs %s`\n", args[0], res.RunID, args[0])
			return nil
		})
}

func routineToggle(dial callerFor, use, short, method, done string) *cobra.Command {
	return sessionSub(dial, use+" <bot>/<slug>", short,
		"  argus routine "+use+" scout/inbox", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, slug, err := resolveRoutine(call, args[0])
			if err != nil {
				return err
			}
			var res api.BotRoutineInfo
			if err := call(method, api.BotRoutineRef{BotID: a.ID, Slug: slug}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			shell.StdOutF("%s %s: %s\n", done, args[0], routineState(res))
			return nil
		})
}

func routinePause(dial callerFor) *cobra.Command {
	return routineToggle(dial, "pause", "Pause a routine without editing its file", api.MethodBotRoutinePause, "paused")
}

func routineResume(dial callerFor) *cobra.Command {
	return routineToggle(dial, "resume", "Resume a paused routine from now", api.MethodBotRoutineResume, "resumed")
}

func routineRuns(dial callerFor) *cobra.Command {
	var limit int
	return sessionSub(dial, "runs <bot>/<slug>", "List a routine's runs, newest first",
		"  argus routine runs scout/inbox --limit 5", cobra.ExactArgs(1),
		func(cmd *cobra.Command) { cmd.Flags().IntVar(&limit, "limit", 20, "number of runs to list") },
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, slug, err := resolveRoutine(call, args[0])
			if err != nil {
				return err
			}
			var res api.BotRoutineRunsResult
			if err := call(api.MethodBotRoutineRuns, api.BotRoutineRunsParams{BotID: a.ID, Slug: slug, Limit: limit}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			rows := make([][]string, 0, len(res.Runs))
			for _, r := range res.Runs {
				by := r.StartedBy
				if r.Test {
					by += " (test)"
				}
				summary := r.Summary
				if summary == "" {
					summary = r.Error
				}
				started := r.StartedAt
				rows = append(rows, []string{whenText(&started), by, r.State, r.Result, firstLine(summary)})
			}
			printTable([]string{"STARTED", "BY", "STATE", "RESULT", "SUMMARY"}, rows)
			return nil
		})
}

func routineHookSecret(dial callerFor) *cobra.Command {
	return sessionSub(dial, "hook-secret <bot>/<slug>", "Make a new webhook secret for a routine and print it",
		"  argus routine hook-secret scout/alerts", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, slug, err := resolveRoutine(call, args[0])
			if err != nil {
				return err
			}
			var res api.BotRoutineHookSecretResult
			if err := call(api.MethodBotRoutineHookSecret, api.BotRoutineRef{BotID: a.ID, Slug: slug}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			shell.StdOutF("secret: %s\n", res.Secret)
			shell.StdOutF("path:   %s\n", res.Path)
			if res.Listen == "" {
				shell.StdOutF("webhooks are off on this node; set bots.webhook.listen-addr to accept them\n")
			} else {
				shell.StdOutF("listen: %s\n", res.Listen)
			}
			shell.StdOutF("The secret is shown only now; the previous one no longer works.\n")
			return nil
		})
}
