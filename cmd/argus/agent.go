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
	agentWaitPoll              = time.Second
	agentWaitTimeout           = 10 * time.Minute
	agentStdin       io.Reader = os.Stdin
	agentStdinIsTTY            = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) }
)

func newAgentCmd() *cobra.Command { return agentCmd(localCallerFor) }

func agentCmd(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Create and talk to persistent agents",
		Long: `Create and talk to persistent agents.

An agent is a directory with an agent.yaml, instructions and memory. Agents
are registered by name; where two nodes have an agent with the same name, use
<name>@<node-label>.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(
		agentCreate(dial), agentAdd(dial), agentList(dial), agentShow(dial),
		agentRename(dial), agentForget(dial), agentPrimary(dial), agentChat(dial), agentThreads(dial), agentMemory(dial),
	)
	return cmd
}

func agentLabel(a api.PAgentInfo) string {
	if a.NodeLabel != "" {
		return a.NodeLabel
	}
	return a.NodeID
}

// resolveAgent finds the registered agent that ref names: "<name>" or
// "<name>@<node>".
func resolveAgent(call caller, ref string) (api.PAgentInfo, error) {
	var res api.PAgentListResult
	if err := call(api.MethodPAgentList, api.PAgentListParams{}, &res); err != nil {
		return api.PAgentInfo{}, err
	}
	name, node, hasNode := strings.Cut(ref, "@")
	var matches []api.PAgentInfo
	for _, a := range res.Agents {
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
		return api.PAgentInfo{}, fmt.Errorf("unknown agent %q", ref)
	case 1:
		return matches[0], nil
	}
	return api.PAgentInfo{}, fmt.Errorf("agent %q is on several nodes; use %s@<node-label>", name, name)
}

func agentFields(a api.PAgentInfo) [][2]string {
	primary := ""
	if a.Primary {
		primary = "yes"
	}
	return [][2]string{
		{"id", a.ID}, {"dir", a.Dir}, {"description", a.Description},
		{"agent", a.Agent}, {"model", a.Model}, {"status", a.Status},
		{"node", agentLabel(a)}, {"primary", primary},
	}
}

func agentCreate(dial callerFor) *cobra.Command {
	var p api.PAgentCreateParams
	return sessionSub(dial, "create [name]", "Create an agent directory and register it",
		"  argus agent create scout --agent claude", cobra.MaximumNArgs(1),
		func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.StringVar(&p.Dir, "dir", "", "agent directory (default: under the agents directory)")
			f.StringVar(&p.Agent, "agent", "", "coding agent to run, for example claude")
			f.StringVar(&p.Model, "model", "", "model")
			f.StringVar(&p.From, "from", "", "copy agent.yaml, skills and routines from this agent")
			f.StringVar(&p.NodeID, "node", "", "node to create the agent on")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			if len(args) > 0 {
				p.Name = args[0]
			}
			var info api.PAgentInfo
			if err := call(api.MethodPAgentCreate, p, &info); err != nil {
				return err
			}
			if asJSON {
				return printJSON(info)
			}
			printFields("created agent "+info.Name, agentFields(info))
			return nil
		})
}

func agentAdd(dial callerFor) *cobra.Command {
	var p api.PAgentAddParams
	return sessionSub(dial, "add <path>", "Register an existing agent directory",
		"  argus agent add ~/agents/scout", cobra.ExactArgs(1),
		func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.StringVar(&p.Name, "name", "", "register under this name, rewriting agent.yaml")
			f.BoolVar(&p.ImportInstructions, "import-instructions", false, "import an existing CLAUDE.md or AGENTS.md into the agent's instructions")
			f.StringVar(&p.NodeID, "node", "", "node holding the directory")
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
			var res api.PAgentAddResult
			if err := call(api.MethodPAgentAdd, p, &res); err != nil {
				return err
			}
			if res.FoundInstructions && !p.ImportInstructions {
				if !agentStdinIsTTY() {
					return errors.New("the directory has an existing CLAUDE.md or AGENTS.md; use --import-instructions to import it")
				}
				if !agentConfirm("import existing CLAUDE.md or AGENTS.md into instructions? [y/N] ") {
					return errors.New("not registered; the directory has an existing instructions file")
				}
				p.ImportInstructions = true
				res = api.PAgentAddResult{}
				if err := call(api.MethodPAgentAdd, p, &res); err != nil {
					return err
				}
			}
			if asJSON {
				return printJSON(res)
			}
			printFields("added agent "+res.Agent.Name, agentFields(res.Agent))
			return nil
		})
}

func agentConfirm(prompt string) bool {
	shell.StdErrF("%s", prompt)
	line, _ := bufio.NewReader(agentStdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func agentList(dial callerFor) *cobra.Command {
	var all bool
	return sessionSub(dial, "list", "List agents",
		"  argus agent list", cobra.NoArgs,
		func(cmd *cobra.Command) { cmd.Flags().BoolVar(&all, "all", false, "include forgotten agents") },
		func(cmd *cobra.Command, call caller, _ []string, asJSON bool) error {
			var res api.PAgentListResult
			if err := call(api.MethodPAgentList, api.PAgentListParams{IncludeArchived: all}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(res)
			}
			rows := make([][]string, 0, len(res.Agents))
			for _, a := range res.Agents {
				status := a.Status
				if a.Archived {
					status = "forgotten"
				}
				primary := ""
				if a.Primary {
					primary = "yes"
				}
				rows = append(rows, []string{a.Name, primary, status, a.Agent, a.Model, agentLabel(a), a.Dir})
			}
			printTable([]string{"NAME", "PRIMARY", "STATUS", "AGENT", "MODEL", "NODE", "DIR"}, rows)
			return nil
		})
}

func agentShow(dial callerFor) *cobra.Command {
	return sessionSub(dial, "show <name>", "Show an agent",
		"  argus agent show scout", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveAgent(call, args[0])
			if err != nil {
				return err
			}
			var info api.PAgentInfo
			if err := call(api.MethodPAgentShow, api.PAgentRef{AgentID: a.ID}, &info); err != nil {
				return err
			}
			if asJSON {
				return printJSON(info)
			}
			printFields(info.Name, agentFields(info))
			return nil
		})
}

func agentRename(dial callerFor) *cobra.Command {
	return sessionSub(dial, "rename <name> <new>", "Rename an agent",
		"  argus agent rename scout scout2", cobra.ExactArgs(2), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveAgent(call, args[0])
			if err != nil {
				return err
			}
			var info api.PAgentInfo
			if err := call(api.MethodPAgentRename, api.PAgentRenameParams{AgentID: a.ID, Name: args[1]}, &info); err != nil {
				return err
			}
			if asJSON {
				return printJSON(info)
			}
			printFields("renamed agent "+info.ID, [][2]string{{"from", a.Name}, {"to", info.Name}})
			return nil
		})
}

func agentForget(dial callerFor) *cobra.Command {
	var purge bool
	return sessionSub(dial, "forget <name>", "Unregister an agent; its files stay",
		"  argus agent forget scout", cobra.ExactArgs(1),
		func(cmd *cobra.Command) {
			cmd.Flags().BoolVar(&purge, "purge", false, "also delete the agent's threads and records")
		},
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveAgent(call, args[0])
			if err != nil {
				return err
			}
			if err := call(api.MethodPAgentForget, api.PAgentForgetParams{AgentID: a.ID, Purge: purge}, nil); err != nil {
				return err
			}
			if asJSON {
				return printJSON(map[string]any{"id": a.ID, "name": a.Name, "purged": purge})
			}
			shell.StdOutF("forgot agent %s\n", a.Name)
			return nil
		})
}

func agentPrimary(dial callerFor) *cobra.Command {
	var unset bool
	return sessionSub(dial, "primary [name]", "Set or clear the primary agent",
		"  argus agent primary scout\n  argus agent primary --unset", cobra.MaximumNArgs(1),
		func(cmd *cobra.Command) { cmd.Flags().BoolVar(&unset, "unset", false, "clear the primary agent") },
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			if unset == (len(args) == 1) {
				return errors.New("give an agent name or --unset")
			}
			var p api.PAgentPrimaryParams
			name := ""
			if !unset {
				a, err := resolveAgent(call, args[0])
				if err != nil {
					return err
				}
				p.AgentID, name = a.ID, a.Name
			}
			if err := call(api.MethodPAgentSetPrimary, p, nil); err != nil {
				return err
			}
			if asJSON {
				return printJSON(map[string]any{"primary": p.AgentID})
			}
			if unset {
				shell.StdOutF("cleared the primary agent\n")
			} else {
				shell.StdOutF("primary agent is %s\n", name)
			}
			return nil
		})
}

// chatTarget picks the agent for chat: the named one, else the primary, else
// the only one.
func chatTarget(call caller, name string) (api.PAgentInfo, error) {
	if name != "" {
		return resolveAgent(call, name)
	}
	var res api.PAgentListResult
	if err := call(api.MethodPAgentList, api.PAgentListParams{}, &res); err != nil {
		return api.PAgentInfo{}, err
	}
	for _, a := range res.Agents {
		if res.Primary != "" && a.ID == res.Primary {
			return a, nil
		}
	}
	switch len(res.Agents) {
	case 0:
		return api.PAgentInfo{}, errors.New("no agents; create one with: argus agent create")
	case 1:
		return res.Agents[0], nil
	}
	return api.PAgentInfo{}, errors.New("several agents; name one")
}

type agentChatResult struct {
	ThreadID  string `json:"thread_id"`
	SessionID string `json:"session_id"`
	Reply     string `json:"reply,omitempty"`
	Waiting   bool   `json:"waiting,omitempty"`
}

func agentChat(dial callerFor) *cobra.Command {
	var (
		newThread bool
		wait      bool
		message   string
	)
	return sessionSub(dial, "chat [name] [message]", "Send a message to an agent's chat thread",
		"  argus agent chat scout \"summarize today\" --wait\n  argus agent chat -m \"hello\"", cobra.MaximumNArgs(2),
		func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.BoolVar(&newThread, "new", false, "start a new thread instead of continuing the latest")
			f.BoolVar(&wait, "wait", false, "wait for the reply and print it")
			f.StringVarP(&message, "message", "m", "", "message to send, for use without naming an agent")
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
				const hint = "open the TUI and choose the agent: argus"
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
			var sent api.PAgentSendResult
			if err := call(api.MethodPAgentThreadSend, api.PAgentSendParams{AgentID: a.ID, Text: message, New: newThread}, &sent); err != nil {
				return err
			}
			res := agentChatResult{ThreadID: sent.ThreadID, SessionID: sent.SessionID}
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
					shell.StdOutF("agent is waiting for you\n")
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
func chatBaseline(call caller, agentID string, newThread bool) (string, int, error) {
	if newThread {
		return "", 0, nil
	}
	var threads api.PAgentThreadsResult
	if err := call(api.MethodPAgentThreadList, api.PAgentRef{AgentID: agentID}, &threads); err != nil {
		return "", 0, err
	}
	var latest *api.PAgentThreadInfo
	for i, t := range threads.Threads {
		if t.Kind == "chat" && t.Status == "active" && (latest == nil || t.LastActiveAt.After(latest.LastActiveAt)) {
			latest = &threads.Threads[i]
		}
	}
	if latest == nil {
		return "", 0, nil
	}
	var tl api.PAgentTimeline
	if err := call(api.MethodPAgentThreadTimeline, api.PAgentTimelineParams{ThreadID: latest.ID}, &tl); err != nil {
		return "", 0, err
	}
	return latest.ID, userEntries(tl.Entries), nil
}

// waitForReply polls the thread until its session has stopped and the
// timeline holds text after the first user entry beyond baseline user
// entries. A session waiting on the user returns what is there, with waiting
// set.
func waitForReply(ctx context.Context, call caller, agentID, threadID string, baseline int) (reply string, waiting bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, agentWaitTimeout)
	defer cancel()
	for {
		var threads api.PAgentThreadsResult
		if err := call(api.MethodPAgentThreadList, api.PAgentRef{AgentID: agentID}, &threads); err != nil {
			return "", false, err
		}
		status := ""
		for _, t := range threads.Threads {
			if t.ID == threadID {
				status = t.SessionStatus
			}
		}
		if status != "working" && status != "starting" {
			var tl api.PAgentTimeline
			if err := call(api.MethodPAgentThreadTimeline, api.PAgentTimelineParams{ThreadID: threadID}, &tl); err != nil {
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
		case <-time.After(agentWaitPoll):
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

func agentThreads(dial callerFor) *cobra.Command {
	return sessionSub(dial, "threads <name>", "List an agent's threads",
		"  argus agent threads scout", cobra.ExactArgs(1), nil,
		func(cmd *cobra.Command, call caller, args []string, asJSON bool) error {
			a, err := resolveAgent(call, args[0])
			if err != nil {
				return err
			}
			var res api.PAgentThreadsResult
			if err := call(api.MethodPAgentThreadList, api.PAgentRef{AgentID: a.ID}, &res); err != nil {
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
