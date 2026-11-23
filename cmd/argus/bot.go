package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/transcript"
)

var (
	botWaitPoll              = time.Second
	botWaitTimeout           = 10 * time.Minute
	botStdin       io.Reader = os.Stdin
	botStdinIsTTY            = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) }
)

func newBotCmd() *cobra.Command { return botCmd(localCallerFor) }

func botCmd(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bot",
		Short: "Create and talk to bots",
		Long: `Create and talk to bots.

A bot is a directory with a bot.yaml, instructions and memory. Bots
are registered by name on a node. These commands work with the bots of the
node this command talks to; manage bots on other nodes from those nodes, or
from the TUI or app.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(
		botCreate(dial), botAdd(dial), botList(dial), botShow(dial),
		botRename(dial), botForget(dial), botPrimary(dial), botChat(dial), botThreads(dial), botMemory(dial),
		botUsage(dial), botSet(dial),
		botActivity(dial), botSecret(dial),
	)
	return cmd
}

func botNodeLabel(a api.BotInfo) string {
	if a.NodeLabel != "" {
		return a.NodeLabel
	}
	return a.NodeID
}

// resolveBot finds the registered bot that ref names: "<name>" or
// "<name>@<node>".
func resolveBot(call caller, ref string) (api.BotInfo, error) {
	var res api.BotListResult
	if err := call(api.MethodBotList, api.BotListParams{}, &res); err != nil {
		return api.BotInfo{}, err
	}
	name, node, hasNode := strings.Cut(ref, "@")
	var matches []api.BotInfo
	for _, a := range res.Bots {
		if a.Name != name {
			continue
		}
		if hasNode && a.NodeLabel != node && a.NodeID != node {
			continue
		}
		matches = append(matches, a)
	}
	switch len(matches) {
	case 0:
		return api.BotInfo{}, fmt.Errorf("unknown bot %q", ref)
	case 1:
		return matches[0], nil
	}
	return api.BotInfo{}, fmt.Errorf("bot %q is on several nodes; use %s@<node-label>", name, name)
}

func botFields(a api.BotInfo) [][2]string {
	primary := ""
	if a.Primary {
		primary = "yes"
	}
	return [][2]string{
		{"id", a.ID}, {"dir", a.Dir}, {"description", a.Description},
		{"agent", a.Agent}, {"model", a.Model}, {"status", a.Status},
		{"node", botNodeLabel(a)}, {"primary", primary},
	}
}

func botCreate(dial callerFor) *cobra.Command {
	var p api.BotCreateParams
	return sessionSub(dial, "create [name]", "Create a bot directory and register it",
		"  argus bot create scout --agent claude", cobra.MaximumNArgs(1),
		func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.StringVar(&p.Dir, "dir", "", "bot directory (default: under the bots directory)")
			f.StringVar(&p.Agent, "agent", "", "coding agent to run, for example claude")
			f.StringVar(&p.Model, "model", "", "model")
			f.StringVar(&p.From, "from", "", "copy bot.yaml, skills and routines from this bot")
			f.StringVar(&p.NodeID, "node", "", "node to create the bot on; manage it from that node afterwards")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			if len(args) > 0 {
				p.Name = args[0]
			}
			var info api.BotInfo
			if err := call(api.MethodBotCreate, p, &info); err != nil {
				return err
			}
			if asJSON {
				return printJSON(info)
			}
			printFields("created bot "+info.Name, botFields(info))
			return nil
		})
}

func botAdd(dial callerFor) *cobra.Command {
	var p api.BotAddParams
	return sessionSub(dial, "add <path>", "Register an existing bot directory",
		"  argus bot add ~/bots/scout", cobra.ExactArgs(1),
		func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.StringVar(&p.Name, "name", "", "register under this name, rewriting bot.yaml")
			f.BoolVar(&p.ImportInstructions, "import-instructions", false, "import an existing CLAUDE.md or AGENTS.md into the bot's instructions")
			f.StringVar(&p.NodeID, "node", "", "node holding the directory; manage the bot from that node afterwards")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			p.Dir = args[0]
			if p.NodeID == "" {
				abs, err := filepath.Abs(p.Dir)
				if err != nil {
					return err
				}
				p.Dir = abs
			}
			var res api.BotAddResult
			if err := call(api.MethodBotAdd, p, &res); err != nil {
				return err
			}
			if res.FoundInstructions && !p.ImportInstructions {
				if !botStdinIsTTY() {
					return errors.New("the directory has an existing CLAUDE.md or AGENTS.md; use --import-instructions to import it")
				}
				if !botConfirm("import existing CLAUDE.md or AGENTS.md into instructions? [y/N] ") {
					return errors.New("not registered; the directory has an existing instructions file")
				}
				p.ImportInstructions = true
				res = api.BotAddResult{}
				if err := call(api.MethodBotAdd, p, &res); err != nil {
					return err
				}
			}
			if asJSON {
				return printJSON(res)
			}
			printFields("added bot "+res.Bot.Name, botFields(res.Bot))
			return nil
		})
}

func botConfirm(prompt string) bool {
	shell.StdErrF("%s", prompt)
	line, _ := bufio.NewReader(botStdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func botList(dial callerFor) *cobra.Command {
	var all bool
	return sessionSub(dial, "list", "List bots",
		"  argus bot list", cobra.NoArgs,
		func(cmd *cobra.Command) { cmd.Flags().BoolVar(&all, "all", false, "include forgotten bots") },
		func(cmd *cobra.Command, call caller, _ []string, asJSON bool) error {
			var res api.BotListResult
			if err := call(api.MethodBotList, api.BotListParams{IncludeArchived: all}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			rows := make([][]string, 0, len(res.Bots))
			for _, a := range res.Bots {
				status := a.Status
				if a.Archived {
					status = "forgotten"
				}
				primary := ""
				if a.Primary {
					primary = "yes"
				}
				rows = append(rows, []string{a.Name, primary, status, a.Agent, a.Model, botNodeLabel(a), a.Dir})
			}
			printTable([]string{"NAME", "PRIMARY", "STATUS", "AGENT", "MODEL", "NODE", "DIR"}, rows)
			return nil
		})
}

func botShow(dial callerFor) *cobra.Command {
	return sessionSub(dial, "show <name>", "Show a bot",
		"  argus bot show scout", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			var info api.BotInfo
			if err := call(api.MethodBotShow, api.BotRef{BotID: a.ID}, &info); err != nil {
				return err
			}
			if asJSON {
				return printJSON(info)
			}
			printFields(info.Name, botFields(info))
			return nil
		})
}

func botRename(dial callerFor) *cobra.Command {
	return sessionSub(dial, "rename <name> <new>", "Rename a bot",
		"  argus bot rename scout scout2", cobra.ExactArgs(2), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			var info api.BotInfo
			if err := call(api.MethodBotRename, api.BotRenameParams{BotID: a.ID, Name: args[1]}, &info); err != nil {
				return err
			}
			if asJSON {
				return printJSON(info)
			}
			printFields("renamed bot "+info.ID, [][2]string{{"from", a.Name}, {"to", info.Name}})
			return nil
		})
}

func botForget(dial callerFor) *cobra.Command {
	var purge bool
	return sessionSub(dial, "forget <name>", "Unregister a bot; its files stay",
		"  argus bot forget scout", cobra.ExactArgs(1),
		func(cmd *cobra.Command) {
			cmd.Flags().BoolVar(&purge, "purge", false, "also delete the bot's threads and records")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			if err := call(api.MethodBotForget, api.BotForgetParams{BotID: a.ID, Purge: purge}, nil); err != nil {
				return err
			}
			if asJSON {
				return printJSON(map[string]any{"id": a.ID, "name": a.Name, "purged": purge})
			}
			shell.StdOutF("forgot bot %s\n", a.Name)
			return nil
		})
}

func botPrimary(dial callerFor) *cobra.Command {
	var unset bool
	return sessionSub(dial, "primary [name]", "Set or clear the primary bot",
		"  argus bot primary scout\n  argus bot primary --unset", cobra.MaximumNArgs(1),
		func(cmd *cobra.Command) { cmd.Flags().BoolVar(&unset, "unset", false, "clear the primary bot") },
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			if unset == (len(args) == 1) {
				return errors.New("give a bot name or --unset")
			}
			var p api.BotPrimaryParams
			name := ""
			if !unset {
				a, err := resolveBot(call, args[0])
				if err != nil {
					return err
				}
				p.BotID, name = a.ID, a.Name
			}
			if err := call(api.MethodBotSetPrimary, p, nil); err != nil {
				return err
			}
			if asJSON {
				return printJSON(map[string]any{"primary": p.BotID})
			}
			if unset {
				shell.StdOutF("cleared the primary bot\n")
			} else {
				shell.StdOutF("primary bot is %s\n", name)
			}
			return nil
		})
}

// chatTarget picks the bot for chat: the named one, else the primary, else
// the only one.
func chatTarget(call caller, name string) (api.BotInfo, error) {
	if name != "" {
		return resolveBot(call, name)
	}
	var res api.BotListResult
	if err := call(api.MethodBotList, api.BotListParams{}, &res); err != nil {
		return api.BotInfo{}, err
	}
	for _, a := range res.Bots {
		if res.Primary != "" && a.ID == res.Primary {
			return a, nil
		}
	}
	switch len(res.Bots) {
	case 0:
		return api.BotInfo{}, errors.New("no bots; create one with: argus bot create")
	case 1:
		return res.Bots[0], nil
	}
	return api.BotInfo{}, errors.New("several bots; name one")
}

type botChatResult struct {
	ThreadID  string `json:"thread_id"`
	SessionID string `json:"session_id"`
	Reply     string `json:"reply,omitempty"`
	Waiting   bool   `json:"waiting,omitempty"`
}

func botChat(dial callerFor) *cobra.Command {
	var (
		newThread bool
		wait      bool
		message   string
	)
	return sessionSub(dial, "chat [name] [message]", "Send a message to a bot's chat thread",
		"  argus bot chat scout \"summarize today\" --wait\n  argus bot chat -m \"hello\"", cobra.MaximumNArgs(2),
		func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.BoolVar(&newThread, "new", false, "start a new thread instead of continuing the latest")
			f.BoolVar(&wait, "wait", false, "wait for the reply and print it")
			f.StringVarP(&message, "message", "m", "", "message to send, for use without naming a bot")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			if len(args) > 1 {
				if message != "" {
					return errors.New("give the message once")
				}
				message = args[1]
			}
			a, err := chatTarget(call, name)
			if err != nil {
				return err
			}
			if message == "" {
				if wait {
					return errors.New("--wait needs a message")
				}
				const hint = "open the TUI and choose the bot: argus"
				if asJSON {
					return printJSON(map[string]string{"hint": hint})
				}
				shell.StdOutF("%s\n", hint)
				return nil
			}
			baseThread, baseline := "", 0
			if wait {
				if baseThread, baseline, err = chatBaseline(call, a.ID, newThread); err != nil {
					return err
				}
			}
			var sent api.BotSendResult
			if err := call(api.MethodBotThreadSend, api.BotSendParams{BotID: a.ID, Text: message, New: newThread}, &sent); err != nil {
				return err
			}
			res := botChatResult{ThreadID: sent.ThreadID, SessionID: sent.SessionID}
			if wait {
				if sent.ThreadID != baseThread {
					baseline = 0
				}
				if res.Reply, res.Waiting, err = waitForReply(cmd.Context(), call, a.ID, sent.ThreadID, baseline); err != nil {
					return err
				}
			}
			if asJSON {
				return printJSON(res)
			}
			if wait {
				if res.Reply != "" {
					shell.StdOutF("%s\n", res.Reply)
				}
				if res.Waiting {
					shell.StdOutF("bot is waiting for you\n")
				}
			} else {
				shell.StdOutF("%s\n", sent.ThreadID)
			}
			return nil
		})
}

// userEntries counts the user entries of a thread's timeline.
func userEntries(entries []transcript.Entry) int {
	n := 0
	for _, e := range entries {
		if e.Kind == transcript.EntryUser {
			n++
		}
	}
	return n
}

// chatBaseline returns the thread a send without --new continues and how many
// user entries its timeline holds, so a reply can be told from earlier ones.
func chatBaseline(call caller, botID string, newThread bool) (string, int, error) {
	if newThread {
		return "", 0, nil
	}
	var threads api.BotThreadsResult
	if err := call(api.MethodBotThreadList, api.BotRef{BotID: botID}, &threads); err != nil {
		return "", 0, err
	}
	var latest *api.BotThreadInfo
	for i, t := range threads.Threads {
		if t.Kind == "chat" && t.Status == "active" && (latest == nil || t.LastActiveAt.After(latest.LastActiveAt)) {
			latest = &threads.Threads[i]
		}
	}
	if latest == nil {
		return "", 0, nil
	}
	var tl api.BotTimeline
	if err := call(api.MethodBotThreadTimeline, api.BotTimelineParams{ThreadID: latest.ID}, &tl); err != nil {
		return "", 0, err
	}
	return latest.ID, userEntries(tl.Entries), nil
}

// waitForReply polls the thread until its session has stopped and the
// timeline holds text after the first user entry beyond baseline user
// entries. A session waiting on the user returns what is there, with waiting
// set.
func waitForReply(ctx context.Context, call caller, botID, threadID string, baseline int) (reply string, waiting bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, botWaitTimeout)
	defer cancel()
	for {
		var threads api.BotThreadsResult
		if err := call(api.MethodBotThreadList, api.BotRef{BotID: botID}, &threads); err != nil {
			return "", false, err
		}
		status := ""
		for _, t := range threads.Threads {
			if t.ID == threadID {
				status = t.SessionStatus
			}
		}
		if status != "working" && status != "starting" {
			var tl api.BotTimeline
			if err := call(api.MethodBotThreadTimeline, api.BotTimelineParams{ThreadID: threadID}, &tl); err != nil {
				return "", false, err
			}
			text, ok := replyAfterUser(tl.Entries, baseline)
			if status == "awaiting_input" {
				return text, true, nil
			}
			if ok {
				return text, false, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", false, errors.New("timed out waiting for the reply")
		case <-time.After(botWaitPoll):
		}
	}
}

// replyAfterUser returns the text entries following the first user entry
// beyond the first baseline ones.
func replyAfterUser(entries []transcript.Entry, baseline int) (string, bool) {
	seen, start := 0, -1
	for i, e := range entries {
		if e.Kind != transcript.EntryUser {
			continue
		}
		if seen == baseline {
			start = i
			break
		}
		seen++
	}
	if start < 0 {
		return "", false
	}
	var parts []string
	for _, e := range entries[start+1:] {
		if e.Kind == transcript.EntryText && strings.TrimSpace(e.Text) != "" {
			parts = append(parts, strings.TrimSpace(e.Text))
		}
	}
	return strings.Join(parts, "\n\n"), len(parts) > 0
}

func botThreads(dial callerFor) *cobra.Command {
	return sessionSub(dial, "threads <name>", "List a bot's threads",
		"  argus bot threads scout", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveBot(call, args[0])
			if err != nil {
				return err
			}
			var res api.BotThreadsResult
			if err := call(api.MethodBotThreadList, api.BotRef{BotID: a.ID}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			rows := make([][]string, 0, len(res.Threads))
			for _, t := range res.Threads {
				unread := ""
				if t.Unread {
					unread = "yes"
				}
				rows = append(rows, []string{t.ID, t.Kind, t.StartedBy, t.Status, unread, t.LastActiveAt.Local().Format("2006-01-02 15:04"), t.Title})
			}
			printTable([]string{"ID", "KIND", "STARTED BY", "STATUS", "UNREAD", "LAST ACTIVE", "TITLE"}, rows)
			return nil
		})
}
