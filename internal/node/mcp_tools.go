package node

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/mcphttp"
)

type mcpBackend struct{ d *Node }

type mcpTool struct {
	mcphttp.Tool
	props       []mcpProp
	needsCaller bool
	needsOrch   bool
	run         func(ctx context.Context, d *Node, caller callerRef, args json.RawMessage) (any, error)
}

type mcpProp struct {
	name, kind string
	required   bool
	enum       func(d *Node) []string
	values     map[string]any // schema of an object's values
}

func required(name, kind string) mcpProp { return mcpProp{name: name, kind: kind, required: true} }
func optional(name, kind string) mcpProp { return mcpProp{name: name, kind: kind} }

const idNote = "IDs name an agent conversation, like home:claude:5f1c0e2a-9b1d-4c1e-8f00-2a6d3c4b5e61 (node id, agent, then the agent's own session id). An ID can belong to any connected node, and the call goes to that node."

const requestNote = "request_id is optional: a retry with the same request_id returns the first result instead of acting again, and a request_id reused with different parameters is an error."

const asyncNote = "Messages are asynchronous: the call returns once the message is queued, and the recipient's reply arrives later as a new prompt in your session."

const (
	spawnNext = "The child's result, or a turn_ended message if it stops without one, arrives as a new prompt. End your turn. Do not wait or poll."
	sendNext  = "A reply, if the recipient sends one, arrives as a new prompt. Do not wait or poll."
)

type mcpSpawnResult struct {
	api.OrchSpawnResult
	Next string `json:"next,omitempty"`
}

type mcpSendResult struct {
	api.OrchSendResult
	Next string `json:"next"`
}

func enumOf(vs ...string) func(*Node) []string { return func(*Node) []string { return vs } }

// required returns the names of the required props.
func (t mcpTool) required() []string {
	out := []string{}
	for _, p := range t.props {
		if p.required {
			out = append(out, p.name)
		}
	}
	return out
}

func (t mcpTool) inputSchema(d *Node) json.RawMessage {
	props := make(map[string]any, len(t.props))
	for _, p := range t.props {
		prop := map[string]any{"type": p.kind}
		if p.enum != nil {
			prop["enum"] = p.enum(d)
		}
		if p.values != nil {
			prop["additionalProperties"] = p.values
		}
		props[p.name] = prop
	}
	schema, _ := json.Marshal(map[string]any{
		"type": "object", "properties": props, "required": t.required(), "additionalProperties": false,
	})
	return schema
}

func orchestratedAgents(d *Node) []string {
	var out []string
	for name := range d.adapters {
		if d.orchestrated(name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func withArgs[T any](run func(ctx context.Context, d *Node, caller callerRef, p T) (any, error)) func(context.Context, *Node, callerRef, json.RawMessage) (any, error) {
	return func(ctx context.Context, d *Node, caller callerRef, args json.RawMessage) (any, error) {
		p, err := api.Decode[T](args)
		if err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return run(ctx, d, caller, p)
	}
}

func mcpTools() []mcpTool {
	tools := []mcpTool{
		{
			Tool: mcphttp.Tool{
				Name:        "get_current_session",
				Description: "Return the current session, which is the argus session this agent is running in: its ID, agent, name, status, working directory, and parent session if it was spawned by another agent. Use it to learn your own ID before sharing it with other agents. " + idNote,
			},
			needsCaller: true,
			run: func(_ context.Context, d *Node, caller callerRef, _ json.RawMessage) (any, error) {
				return d.currentSession(caller)
			},
		},
		{
			Tool: mcphttp.Tool{
				Name:        "list_sessions",
				Description: "List every session on every online node (the result's unreachable lists nodes that did not answer), with IDs, agent, name, status, working directory, and parent. A session appears once its agent session id is known. Use it to find the session to message. " + idNote,
			},
			run: func(ctx context.Context, d *Node, _ callerRef, _ json.RawMessage) (any, error) {
				return d.listAll(ctx), nil
			},
		},
		{
			Tool: mcphttp.Tool{
				Name:        "get_session",
				Description: "Show one session and the messages still queued for it (not yet delivered). " + idNote,
			},
			props: []mcpProp{required("session_id", "string")}, needsOrch: true,
			run: withArgs(func(ctx context.Context, d *Node, _ callerRef, p api.OrchTargetParams) (any, error) {
				return d.getSession(ctx, p.SessionID)
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name: "spawn_session",
				Description: "Start a new agent session in a working directory. Prefer your native subagent tool when it can do the task. Use spawn_session when the task needs a different agent, or when the user asks for an argus session. " +
					"agent is the agent to run; cwd is an absolute directory path and is required. name and model are optional. " +
					"node is the ID or label of the node to start it on; it defaults to your own node, and cwd and agent refer to that node. " +
					"If prompt is given, it is delivered to the new session as a task message from you, and the new session reports back to you with the reply_to_message tool; the result's task_id is the ID of that task message. The new session becomes your child: you can answer its questions with respond_to_interaction (its permission prompts and plans go to the user) and stop it with kill_session. " +
					asyncNote + " The result's session_id is the new session's ID. A message to it waits until the session starts. " + requestNote + " " + idNote,
			},
			props: []mcpProp{
				{name: "agent", kind: "string", required: true, enum: orchestratedAgents}, required("cwd", "string"),
				optional("name", "string"), optional("model", "string"), optional("prompt", "string"),
				optional("request_id", "string"), optional("node", "string"),
			}, needsCaller: true, needsOrch: true,
			run: withArgs(func(ctx context.Context, d *Node, caller callerRef, p api.OrchSpawnParams) (any, error) {
				res, err := d.spawnFor(ctx, caller, p)
				if err != nil {
					return nil, err
				}
				out := mcpSpawnResult{OrchSpawnResult: res}
				if res.TaskID != "" {
					out.Next = spawnNext
				}
				return out, nil
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name:        "send_message",
				Description: "Send a text message to another agent session. " + asyncNote + " The result carries the message_id; the recipient answers it with the reply_to_message tool. " + requestNote + " " + idNote,
			},
			props: []mcpProp{required("session_id", "string"), required("text", "string"), optional("request_id", "string")}, needsCaller: true, needsOrch: true,
			run: withArgs(func(ctx context.Context, d *Node, caller callerRef, p api.OrchSendParams) (any, error) {
				res, err := d.send(ctx, caller, p.SessionID, p.Text, p.RequestID)
				if err != nil {
					return nil, err
				}
				return mcpSendResult{OrchSendResult: res, Next: sendNext}, nil
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name: "reply_to_message",
				Description: "Reply to a message that was delivered to you, such as a task from the agent that spawned you or a message from another agent. message_id is the ID shown in that message, such as home:m-7f3a09c1. " +
					"Only the message's recipient can reply. The call returns once the reply is queued. A task closes when you reply to it.",
			},
			props: []mcpProp{required("message_id", "string"), required("text", "string")}, needsCaller: true, needsOrch: true,
			run: withArgs(func(ctx context.Context, d *Node, caller callerRef, p api.OrchReplyParams) (any, error) {
				return d.reply(ctx, caller, p.MessageID, p.Text)
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name: "respond_to_interaction",
				Description: "Answer a question of a child session you spawned. It arrives in your session as a message of kind interaction, with a message_id such as home:m-7f3a09c1. " +
					"Permission prompts and plan approvals belong to the user: the child's own session asks the user, and this tool cannot answer them. " +
					"answers maps each question text to the chosen option label, or to a list of labels for a multi-select question; behavior deny (with an optional reason) declines the question. Give exactly one of answers or behavior. Only the parent can respond.",
			},
			props: []mcpProp{
				required("message_id", "string"), {name: "behavior", kind: "string", enum: enumOf("deny")}, optional("reason", "string"),
				{name: "answers", kind: "object", values: map[string]any{
					"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
				}},
			}, needsCaller: true, needsOrch: true,
			run: withArgs(func(ctx context.Context, d *Node, caller callerRef, p api.OrchRespondParams) (any, error) {
				if err := d.respond(ctx, caller, p); err != nil {
					return nil, err
				}
				return struct{}{}, nil
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name:        "get_session_output",
				Description: "Return the text of the session's latest answer: the assistant text after its last user message. " + idNote,
			},
			props: []mcpProp{required("session_id", "string")},
			run: withArgs(func(ctx context.Context, d *Node, _ callerRef, p api.OrchTargetParams) (any, error) {
				return d.output(ctx, p.SessionID)
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name:        "get_session_transcript",
				Description: "Return the session's transcript entries (user, assistant text, and tool activity). last limits the result to the final N entries; omit it for all. " + idNote,
			},
			props: []mcpProp{required("session_id", "string"), optional("last", "integer")},
			run: withArgs(func(ctx context.Context, d *Node, _ callerRef, p api.OrchTranscriptParams) (any, error) {
				return d.transcript(ctx, p.SessionID, p.Last)
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name:        "kill_session",
				Description: "Stop an agent session and fail its queued messages. The session's own children, and their children, are stopped too. Allowed on a session you spawned or on yourself. The result's killed lists every stopped session, deepest first; not_killed lists descendants on other nodes that could not be reached or stopped. " + idNote,
			},
			props: []mcpProp{required("session_id", "string")}, needsCaller: true, needsOrch: true,
			run: withArgs(func(ctx context.Context, d *Node, caller callerRef, p api.OrchTargetParams) (any, error) {
				return d.kill(ctx, caller, p.SessionID)
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name:        "rename_session",
				Description: "Rename any agent session, not only one you spawned. The result's applied is false when the agent takes the name later. " + idNote,
			},
			props: []mcpProp{required("session_id", "string"), required("name", "string")},
			run: withArgs(func(ctx context.Context, d *Node, _ callerRef, p api.OrchRenameParams) (any, error) {
				return d.rename(ctx, p.SessionID, p.Name)
			}),
		},
	}
	return tools
}

var mcpToolSet = mcpTools()

func (b mcpBackend) Tools() []mcphttp.Tool {
	out := make([]mcphttp.Tool, len(mcpToolSet))
	for i, t := range mcpToolSet {
		out[i] = t.Tool
		out[i].InputSchema = t.inputSchema(b.d)
	}
	return out
}

func (b mcpBackend) Call(ctx context.Context, agent, name string, meta map[string]json.RawMessage, args json.RawMessage) mcphttp.CallResult {
	out, err := b.call(ctx, agent, name, meta, args)
	if err != nil {
		return mcphttp.CallResult{Text: err.Error(), IsError: true}
	}
	text, err := json.Marshal(out)
	if err != nil {
		return mcphttp.CallResult{Text: err.Error(), IsError: true}
	}
	return mcphttp.CallResult{Text: string(text)}
}

func (b mcpBackend) call(ctx context.Context, agent, name string, meta map[string]json.RawMessage, args json.RawMessage) (any, error) {
	i := slices.IndexFunc(mcpToolSet, func(t mcpTool) bool { return t.Name == name })
	if i < 0 {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	t := mcpToolSet[i]
	if err := checkArgs(args, t); err != nil {
		return nil, err
	}
	if t.needsOrch && b.d.orch == nil {
		return nil, errOrchDisabled
	}
	var caller callerRef
	if t.needsCaller {
		s, err := b.d.resolveMCPCaller(ctx, agent, meta)
		if err != nil {
			return nil, err
		}
		caller = b.d.localCallerRef(s)
	}
	return t.run(ctx, b.d, caller, args)
}

func checkArgs(args json.RawMessage, t mcpTool) error {
	m := map[string]json.RawMessage{}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &m); err != nil {
			return fmt.Errorf("invalid arguments: %w", err)
		}
	}
	for k := range m {
		if !slices.ContainsFunc(t.props, func(p mcpProp) bool { return p.name == k }) {
			return fmt.Errorf("unknown argument: %s", k)
		}
	}
	for _, k := range t.required() {
		var str string
		if v, ok := m[k]; !ok || string(v) == "null" || json.Unmarshal(v, &str) == nil && strings.TrimSpace(str) == "" {
			return fmt.Errorf("missing required argument: %s", k)
		}
	}
	return nil
}
