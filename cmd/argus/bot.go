package main

import (
	"bufio"
	"context"
	"encoding/json"
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
are registered by name on a node. These commands reach the bots of every
connected node: name a bot as <name>@<node>, or pass --node, when the name
is on more than one node.`,
		SilenceUsage: true,
	}
	cmd.PersistentFlags().String("node", "", "node (id or label) of the bot")
	cmd.AddCommand(
		botCreate(dial), botAdd(dial), botList(dial), botShow(dial),
		botRename(dial), botForget(dial), botPrimary(dial), botChat(dial), botThreads(dial), botMemory(dial),
		botUsage(dial), botSet(dial),
		botActivity(dial), botSecret(dial), botTrust(dial),
	)
	return cmd
}

func botNodeLabel(a api.BotInfo) string {
	if a.NodeLabel != "" {
		return a.NodeLabel
	}
	return a.NodeID
}

// nodeParams are request params addressed to a node. The node the CLI talks
// to forwards them there.
type nodeParams struct {
	Node   string
	Params any
}

func (p nodeParams) MarshalJSON() ([]byte, error) {
	fields := map[string]json.RawMessage{}
	if p.Params != nil {
		raw, err := json.Marshal(p.Params)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
	}
	node, err := json.Marshal(p.Node)
	if err != nil {
		return nil, err
	}
	fields[api.BotNodeField] = node
	return json.Marshal(fields)
}

// onNode returns a caller whose requests run on node; an empty node is the
// node the CLI talks to.
func onNode(call caller, node string) caller {
	if node == "" {
		return call
	}
	return func(method string, params, out any) error {
		return call(method, nodeParams{Node: node, Params: params}, out)
	}
}

// nodeFlag returns the --node flag, or "".
func nodeFlag(cmd *cobra.Command) string {
	node, _ := cmd.Flags().GetString("node")
	return node
}

// withNodeFlag applies --node to a bot name that names no node.
func withNodeFlag(cmd *cobra.Command, ref string) (string, error) {
	node := nodeFlag(cmd)
	if node == "" {
		return ref, nil
	}
	if _, n, ok := strings.Cut(ref, "@"); ok {
		if n != node {
			return "", fmt.Errorf("%s names node %s, but --node is %s", ref, n, node)
		}
		return ref, nil
	}
	return ref + "@" + node, nil
}

// listBots lists the bots of node, or of every node when node is empty.
func listBots(call caller, node string, archived bool) (api.BotListResult, error) {
	var res api.BotListResult
	err := onNode(call, node)(api.MethodBotList, api.BotListParams{IncludeArchived: archived, All: node == ""}, &res)
	return res, err
}

// resolveBot finds the registered bot that ref names, "<name>" or
// "<name>@<node>", on any node.
func resolveBot(call caller, ref string) (api.BotInfo, error) {
	res, err := listBots(call, "", false)
	if err != nil {
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
		if len(res.Unreachable) > 0 {
			return api.BotInfo{}, fmt.Errorf("unknown bot %q; unreachable nodes: %s", ref, strings.Join(res.Unreachable, ", "))
		}
		return api.BotInfo{}, fmt.Errorf("unknown bot %q", ref)
	case 1:
		return matches[0], nil
	}
	nodes := make([]string, len(matches))
	for i, a := range matches {
		nodes[i] = botNodeLabel(a)
	}
	return api.BotInfo{}, fmt.Errorf("bot %q is on several nodes (%s); use %s@<node> or --node", name, strings.Join(nodes, ", "), name)
}

// botsFor returns the bot args names, else the bots of --node, else of every
// node, leaving out bots of unreachable nodes.
func botsFor(cmd *cobra.Command, call caller, args []string) ([]api.BotInfo, error) {
	if len(args) == 1 {
		a, _, err := botOn(cmd, call, args[0])
		return []api.BotInfo{a}, err
	}
	res, err := listBots(call, nodeFlag(cmd), false)
	if err != nil {
		return nil, err
	}
	if len(res.Unreachable) > 0 {
		warn("unreachable nodes, their bots are not listed: " + strings.Join(res.Unreachable, ", "))
	}
	var bots []api.BotInfo
	for _, a := range res.Bots {
		if a.Status != "unreachable" {
			bots = append(bots, a)
		}
	}
	return bots, nil
}

// botOn resolves the bot ref names, applying --node, and returns a caller
// whose requests run on the bot's node.
func botOn(cmd *cobra.Command, call caller, ref string) (api.BotInfo, caller, error) {
	ref, err := withNodeFlag(cmd, ref)
	if err != nil {
		return api.BotInfo{}, nil, err
	}
	a, err := resolveBot(call, ref)
	if err != nil {
		return a, nil, err
	}
	return a, onNode(call, a.NodeID), nil
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
			f.StringVar(&p.NodeID, "node", "", "node (id or label) to create the bot on")
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
			f.StringVar(&p.NodeID, "node", "", "node (id or label) holding the directory")
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
	return sessionSub(dial, "list", "List the bots of every node",
		"  argus bot list\n  argus bot list --node laptop", cobra.NoArgs,
		func(cmd *cobra.Command) { cmd.Flags().BoolVar(&all, "all", false, "include forgotten bots") },
		func(cmd *cobra.Command, call caller, _ []string, asJSON bool) error {
			res, err := listBots(call, nodeFlag(cmd), all)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			if len(res.Unreachable) > 0 {
				warn("unreachable nodes, their bots are not listed: " + strings.Join(res.Unreachable, ", "))
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
			a, call, err := botOn(cmd, call, args[0])
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
			a, call, err := botOn(cmd, call, args[0])
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
			a, call, err := botOn(cmd, call, args[0])
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
	return sessionSub(dial, "primary [name]", "Set or clear a node's primary bot",
		"  argus bot primary scout\n  argus bot primary --unset --node laptop", cobra.MaximumNArgs(1),
		func(cmd *cobra.Command) {
			cmd.Flags().BoolVar(&unset, "unset", false, "clear the primary bot of --node, or of this node")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			if unset == (len(args) == 1) {
				return errors.New("give a bot name or --unset")
			}
			var p api.BotPrimaryParams
			name := ""
			if unset {
				call = onNode(call, nodeFlag(cmd))
			} else {
				a, botCall, err := botOn(cmd, call, args[0])
				if err != nil {
					return err
				}
				p.BotID, name, call = a.ID, a.Name, botCall
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

// chatTarget picks the bot for chat and returns a caller for its node: the
// named one, else the primary of --node or this node, else that node's only
// bot.
func chatTarget(cmd *cobra.Command, call caller, name string) (api.BotInfo, caller, error) {
	if name != "" {
		return botOn(cmd, call, name)
	}
	call = onNode(call, nodeFlag(cmd))
	var res api.BotListResult
	if err := call(api.MethodBotList, api.BotListParams{}, &res); err != nil {
		return api.BotInfo{}, nil, err
	}
	for _, a := range res.Bots {
		if res.Primary != "" && a.ID == res.Primary {
			return a, call, nil
		}
	}
	switch len(res.Bots) {
	case 0:
		return api.BotInfo{}, nil, errors.New("no bots; create one with: argus bot create")
	case 1:
		return res.Bots[0], call, nil
	}
	return api.BotInfo{}, nil, errors.New("several bots; name one")
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
			a, call, err := chatTarget(cmd, call, name)
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
			a, call, err := botOn(cmd, call, args[0])
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
