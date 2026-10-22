package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func newSessionCmd() *cobra.Command { return sessionCmd(localCallerFor) }

// callerInfo identifies this process to the node by PID and agent identity variables.
func callerInfo() api.CallerInfo {
	info := api.CallerInfo{PID: os.Getpid()}
	for _, name := range callerEnvNames() {
		if v, ok := os.LookupEnv(name); ok {
			if info.Env == nil {
				info.Env = map[string]string{}
			}
			info.Env[name] = v
		}
	}
	return info
}

func callerEnvNames() []string {
	var names []string
	for _, a := range adapters.All() {
		if ci, ok := a.(adapter.CallerIdentity); ok {
			names = append(names, ci.CallerEnv())
		}
		if pe, ok := a.(adapter.AgentProcessEnv); ok {
			names = append(names, pe.AgentProcessEnv()...)
		}
	}
	return names
}

type sessionRun func(cmd *cobra.Command, call caller, args []string, asJSON bool) error

func sessionSub(dial callerFor, use, short, example string, args cobra.PositionalArgs, setup func(*cobra.Command), run sessionRun) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:           use,
		Short:         short,
		Example:       example,
		Args:          args,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			call, closeConn, err := dial(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			defer closeConn()
			if err := run(cmd, call, args, asJSON); err != nil {
				return fail(cmd, err)
			}
			return nil
		},
	}
	addSocketFlag(cmd.Flags())
	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	if setup != nil {
		setup(cmd)
	}
	return cmd
}

func sessionCmd(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "List, spawn, message and control agent sessions",
		Long: `List, spawn, message and control agent sessions from inside an agent session.

Session IDs have the form <node>:<id>, for example home:argus:%3 or
home:opencode:ses_01J..., and are printed by list, spawn and whoami.

Messages are asynchronous. send and reply return a message ID immediately and
never wait. The recipient receives the message as a new prompt once it can take
one, and any reply arrives later as a new prompt in your own session.

reply answers a message by its ID: use the ID shown in the prompt you received.
respond answers a prompt notice from a child session that is waiting for a
permission decision or an answer; the notice names the exact form to use.

Tasks: spawn --prompt sends the prompt to the child as a task message and prints
its ID on a "task <id>" line. The child reports its result with
  argus session reply <task-id> "<result>"
and the result arrives in the parent as a new prompt.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(
		sessionWhoami(dial), sessionList(dial), sessionShow(dial), sessionSpawn(dial),
		sessionSend(dial), sessionReply(dial), sessionRespond(dial),
		sessionOutput(dial), sessionTranscript(dial), sessionKill(dial),
	)
	return cmd
}

func sessionWhoami(dial callerFor) *cobra.Command {
	return sessionSub(dial, "whoami", "Print this session's ID",
		"  argus session whoami", cobra.NoArgs, nil,
		func(cmd *cobra.Command, call caller, _ []string, asJSON bool) error {
			var s session.Session
			if err := call(api.MethodAgentWhoami, api.CallerParams{Caller: callerInfo()}, &s); err != nil {
				return err
			}
			if asJSON {
				return printJSON(s)
			}
			shell.StdOutF("%s\n", s.ID)
			return nil
		})
}

func sessionList(dial callerFor) *cobra.Command {
	return sessionSub(dial, "list", "List the sessions you can see",
		"  argus session list", cobra.NoArgs, nil,
		func(cmd *cobra.Command, call caller, _ []string, asJSON bool) error {
			var list []session.Session
			if err := call(api.MethodAgentList, api.CallerParams{Caller: callerInfo()}, &list); err != nil {
				return err
			}
			if asJSON {
				return printJSON(list)
			}
			rows := make([][]string, 0, len(list))
			for _, s := range list {
				rows = append(rows, []string{s.ID, dash(s.Name), s.Agent, string(s.Status), parentLabel(s.Parent), dash(s.Cwd)})
			}
			printTable([]string{"ID", "NAME", "AGENT", "STATUS", "PARENT", "CWD"}, rows)
			return nil
		})
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func parentLabel(p *session.ParentRef) string {
	switch {
	case p == nil:
		return "-"
	case p.Gone:
		return "gone"
	}
	return dash(p.SessionID)
}

func sessionShow(dial callerFor) *cobra.Command {
	return sessionSub(dial, "show <id>", "Show a session, its pending prompt and its queued messages",
		"  argus session show home:argus:%3", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			var res api.AgentShowResult
			if err := call(api.MethodAgentShow, api.AgentTargetParams{Caller: callerInfo(), SessionID: args[0]}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			s := res.Session
			var prompt, tool string
			if s.Interaction != nil {
				prompt, tool = string(s.Interaction.Kind), s.Interaction.ToolName
			}
			printFields(s.ID, [][2]string{
				{"id", s.ID}, {"name", s.Name}, {"agent", s.Agent}, {"status", string(s.Status)},
				{"cwd", s.Cwd}, {"parent", parentLabel(s.Parent)}, {"prompt", prompt}, {"tool", tool},
			})
			for _, m := range res.Queued {
				shell.StdOutF("%s  from %s  %s\n", m.ID, m.From.Key, firstLine(m.Body))
			}
			return nil
		})
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func sessionSpawn(dial callerFor) *cobra.Command {
	var p api.AgentSpawnParams
	return sessionSub(dial, "spawn --agent <agent> [--cwd <dir>] [--name <name>] [--model <model>] [--prompt <text>]",
		"Start a new agent session as your child",
		`  argus session spawn --agent claude --cwd /path/to/repo --name reviewer --prompt "Review the diff on this branch"`,
		cobra.NoArgs,
		func(cmd *cobra.Command) {
			cmd.Long = `Start a new agent session as your child.

Prints the child's session ID. With --prompt, the prompt becomes the child's task
message and a second line "task <id>" prints that message's ID. The child reports
with argus session reply <task-id> "<result>", and the result arrives in your
session as a new prompt.`
			f := cmd.Flags()
			f.StringVar(&p.Agent, "agent", "", "agent to run: claude, codex or opencode")
			f.StringVar(&p.Cwd, "cwd", "", "working directory (default: the current directory)")
			f.StringVar(&p.Name, "name", "", "session name")
			f.StringVar(&p.Model, "model", "", "model to use")
			f.StringVar(&p.Prompt, "prompt", "", "task for the new session, sent as its task message")
			_ = cmd.MarkFlagRequired("agent")
		},
		func(cmd *cobra.Command, call caller, _ []string, asJSON bool) error {
			if p.Cwd == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				p.Cwd = wd
			}
			abs, err := filepath.Abs(p.Cwd)
			if err != nil {
				return err
			}
			p.Cwd = abs
			p.Caller = callerInfo()
			var res api.AgentSpawnResult
			if err := call(api.MethodAgentSpawn, p, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			shell.StdOutF("%s\n", res.SessionID)
			if res.TaskID != "" {
				shell.StdOutF("task %s\n", res.TaskID)
			}
			return nil
		})
}

func sessionSend(dial callerFor) *cobra.Command {
	return sessionSub(dial, "send <id> <text>", "Send a message to a session; it arrives as a new prompt",
		`  argus session send home:opencode:ses_01J... "Run the tests and report failures"`,
		cobra.ExactArgs(2), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			var res api.AgentSendResult
			if err := call(api.MethodAgentSend, api.AgentSendParams{Caller: callerInfo(), SessionID: args[0], Text: args[1]}, &res); err != nil {
				return err
			}
			return printMessageID(res, asJSON)
		})
}

func sessionReply(dial callerFor) *cobra.Command {
	return sessionSub(dial, "reply <msg-id> <text>", "Reply to a message by its ID",
		`  argus session reply m-7f3a09c1 "Done, all tests pass"`,
		cobra.ExactArgs(2), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			var res api.AgentSendResult
			if err := call(api.MethodAgentReply, api.AgentReplyParams{Caller: callerInfo(), MessageID: args[0], Text: args[1]}, &res); err != nil {
				return err
			}
			return printMessageID(res, asJSON)
		})
}

func printMessageID(res api.AgentSendResult, asJSON bool) error {
	if asJSON {
		return printJSON(res)
	}
	shell.StdOutF("%s\n", res.MessageID)
	return nil
}

func sessionRespond(dial callerFor) *cobra.Command {
	var reason, option string
	var answers []string
	return sessionSub(dial, "respond <notice-id> [allow|deny]", "Answer a child session's prompt notice",
		`  argus session respond m-7f3a09c1 allow
  argus session respond m-7f3a09c1 deny --reason "Use the read-only command instead"
  argus session respond m-7f3a09c1 --option accept-edits
  argus session respond m-7f3a09c1 --answer "Which database?=Postgres"`,
		cobra.RangeArgs(1, 2),
		func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.StringVar(&reason, "reason", "", "reason shown to the child when denying")
			f.StringVar(&option, "option", "", "option value offered by the notice")
			f.StringArrayVar(&answers, "answer", nil, `answer to a question as "<question>=<option label>"; repeat per question`)
		},
		func(cmd *cobra.Command, call caller, args []string, _ bool) error {
			p := api.AgentRespondParams{Caller: callerInfo(), NoticeID: args[0], Reason: reason, OptionValue: option}
			if len(args) == 2 {
				p.Behavior = args[1]
			}
			if len(answers) > 0 {
				p.Answers = map[string]any{}
				for _, a := range answers {
					i := strings.LastIndex(a, "=")
					if i < 0 {
						return fmt.Errorf("--answer %q must be \"<question>=<option label>\"", a)
					}
					p.Answers[a[:i]] = a[i+1:]
				}
			}
			return call(api.MethodAgentRespond, p, nil)
		})
}

func sessionOutput(dial callerFor) *cobra.Command {
	return sessionSub(dial, "output <id>", "Print the session's last assistant reply",
		"  argus session output home:argus:%3", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			var res api.AgentOutputResult
			if err := call(api.MethodAgentOutput, api.AgentTargetParams{Caller: callerInfo(), SessionID: args[0]}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			shell.StdOutF("%s", res.Text)
			if !strings.HasSuffix(res.Text, "\n") {
				shell.StdOutF("\n")
			}
			return nil
		})
}

func sessionTranscript(dial callerFor) *cobra.Command {
	var last int
	return sessionSub(dial, "transcript <id> [--last N]", "Print a session's conversation transcript",
		"  argus session transcript home:argus:%3 --last 10", cobra.ExactArgs(1),
		func(cmd *cobra.Command) {
			cmd.Flags().IntVar(&last, "last", 0, "only the last N entries (default: all)")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			var res api.AgentTranscriptResult
			if err := call(api.MethodAgentTranscript, api.AgentTranscriptParams{Caller: callerInfo(), SessionID: args[0], Last: last}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			for _, e := range res.Entries {
				shell.StdOutF("[%s] %s\n", e.Kind, entryText(e))
			}
			return nil
		})
}

func entryText(e transcript.Entry) string {
	if e.Kind == transcript.EntryTool {
		return e.ToolName + ": " + e.InputPreview
	}
	return e.Text
}

func sessionKill(dial callerFor) *cobra.Command {
	return sessionSub(dial, "kill <id>", "Kill a child session, or your own",
		"  argus session kill home:argus:%3", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, _ bool) error {
			return call(api.MethodAgentKill, api.AgentTargetParams{Caller: callerInfo(), SessionID: args[0]}, nil)
		})
}
