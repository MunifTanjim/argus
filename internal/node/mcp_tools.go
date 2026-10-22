package node

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/mcphttp"
	"github.com/MunifTanjim/argus/internal/session"
)

type mcpBackend struct{ d *Node }

type mcpTool struct {
	mcphttp.Tool
	props       []mcpProp
	keys        []string
	required    []string
	needsCaller bool
	needsMesh   bool
	run         func(ctx context.Context, d *Node, caller session.Session, args json.RawMessage) (any, error)
}

type mcpProp struct {
	name, kind string
	required   bool
}

func required(name, kind string) mcpProp { return mcpProp{name: name, kind: kind, required: true} }
func optional(name, kind string) mcpProp { return mcpProp{name: name, kind: kind} }

const idNote = "IDs are composite, like home:argus:%3 (node id, then session id)."

const asyncNote = "Messages are asynchronous: the call returns once the message is queued, and the recipient's reply arrives later as a new prompt in your session."

// build derives the input schema, allowed keys and required list from props.
func (t *mcpTool) build() {
	props := make(map[string]any, len(t.props))
	t.keys = make([]string, 0, len(t.props))
	t.required = []string{}
	for _, p := range t.props {
		props[p.name] = map[string]any{"type": p.kind}
		t.keys = append(t.keys, p.name)
		if p.required {
			t.required = append(t.required, p.name)
		}
	}
	t.InputSchema, _ = json.Marshal(map[string]any{
		"type": "object", "properties": props, "required": t.required, "additionalProperties": false,
	})
}

func withArgs[T any](run func(ctx context.Context, d *Node, caller session.Session, p T) (any, error)) func(context.Context, *Node, session.Session, json.RawMessage) (any, error) {
	return func(ctx context.Context, d *Node, caller session.Session, args json.RawMessage) (any, error) {
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
				Name:        "whoami",
				Description: "Return the argus session this agent is running in: its composite ID, agent, name, status, working directory, and parent session if it was spawned by another agent. Use it to learn your own ID before sharing it with other agents. " + idNote,
			},
			needsCaller: true,
			run: func(_ context.Context, d *Node, caller session.Session, _ json.RawMessage) (any, error) {
				return d.composite(caller), nil
			},
		},
		{
			Tool: mcphttp.Tool{
				Name:        "list",
				Description: "List every session known to this node, agent and shell sessions alike, with composite IDs, agent, name, status, working directory, and parent. Use it to find the session to message. " + idNote,
			},
			run: func(_ context.Context, d *Node, _ session.Session, _ json.RawMessage) (any, error) {
				return d.list(), nil
			},
		},
		{
			Tool: mcphttp.Tool{
				Name:        "show",
				Description: "Show one agent session and the messages still queued for it (not yet delivered). " + idNote,
			},
			props: []mcpProp{required("session_id", "string")}, needsMesh: true,
			run: withArgs(func(ctx context.Context, d *Node, _ session.Session, p api.AgentTargetParams) (any, error) {
				return d.show(ctx, p.SessionID)
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name: "spawn",
				Description: "Start a new agent session in a working directory. agent is claude, codex, or opencode; cwd is an absolute directory path and is required. name and model are optional. " +
					"If prompt is given, it is delivered to the new session as a task message from you, and the new session reports back to you with the reply tool; the result's task_id is the ID of that task message. The new session becomes your child: you can answer its permission prompts with respond and stop it with kill. " +
					asyncNote + " The result's session_id is composite. " + idNote,
			},
			props: []mcpProp{
				required("agent", "string"), required("cwd", "string"),
				optional("name", "string"), optional("model", "string"), optional("prompt", "string"),
			}, needsCaller: true, needsMesh: true,
			run: withArgs(func(ctx context.Context, d *Node, caller session.Session, p api.AgentSpawnParams) (any, error) {
				return d.spawnFor(ctx, caller, p)
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name:        "send",
				Description: "Send a text message to another agent session. " + asyncNote + " The result carries the message_id; the recipient answers it with the reply tool. " + idNote,
			},
			props: []mcpProp{required("session_id", "string"), required("text", "string")}, needsCaller: true, needsMesh: true,
			run: withArgs(func(ctx context.Context, d *Node, caller session.Session, p api.AgentSendParams) (any, error) {
				return d.send(ctx, caller, p.SessionID, p.Text)
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name: "reply",
				Description: "Reply to a message that was delivered to you, such as a task from the agent that spawned you or a message from another agent. message_id is the ID shown in that message. " +
					"Only the message's recipient can reply. " + asyncNote,
			},
			props: []mcpProp{required("message_id", "string"), required("text", "string")}, needsCaller: true, needsMesh: true,
			run: withArgs(func(ctx context.Context, d *Node, caller session.Session, p api.AgentReplyParams) (any, error) {
				return d.reply(ctx, caller, p.MessageID, p.Text)
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name: "respond",
				Description: "Answer a permission or question prompt of a child session you spawned. The prompt arrives in your session as a notice message with a notice_id. " +
					"behavior is allow or deny (with an optional reason); option_value picks one of the notice's listed options; answers maps each question text to the chosen option label. Give exactly one of behavior, option_value, or answers. Only the parent can respond.",
			},
			props: []mcpProp{
				required("notice_id", "string"), optional("behavior", "string"), optional("reason", "string"),
				optional("option_value", "string"), optional("answers", "object"),
			}, needsCaller: true, needsMesh: true,
			run: withArgs(func(ctx context.Context, d *Node, caller session.Session, p api.AgentRespondParams) (any, error) {
				if err := d.respond(ctx, caller, p); err != nil {
					return nil, err
				}
				return struct{}{}, nil
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name:        "output",
				Description: "Return the text of the session's latest answer: the assistant text after its last user message. " + idNote,
			},
			props: []mcpProp{required("session_id", "string")},
			run: withArgs(func(_ context.Context, d *Node, _ session.Session, p api.AgentTargetParams) (any, error) {
				return d.output(p.SessionID)
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name:        "transcript",
				Description: "Return the session's transcript entries (user, assistant text, and tool activity). last limits the result to the final N entries; omit it for all. " + idNote,
			},
			props: []mcpProp{required("session_id", "string"), optional("last", "integer")},
			run: withArgs(func(_ context.Context, d *Node, _ session.Session, p api.AgentTranscriptParams) (any, error) {
				return d.transcript(p.SessionID, p.Last)
			}),
		},
		{
			Tool: mcphttp.Tool{
				Name:        "kill",
				Description: "Stop an agent session and fail its queued messages. Allowed on a session you spawned or on yourself. " + idNote,
			},
			props: []mcpProp{required("session_id", "string")}, needsCaller: true, needsMesh: true,
			run: withArgs(func(ctx context.Context, d *Node, caller session.Session, p api.AgentTargetParams) (any, error) {
				if err := d.kill(ctx, caller, p.SessionID); err != nil {
					return nil, err
				}
				return struct{}{}, nil
			}),
		},
	}
	for i := range tools {
		tools[i].build()
	}
	return tools
}

var mcpToolSet = mcpTools()

func (b mcpBackend) Tools() []mcphttp.Tool {
	out := make([]mcphttp.Tool, len(mcpToolSet))
	for i, t := range mcpToolSet {
		out[i] = t.Tool
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
	if t.needsMesh && b.d.mesh == nil {
		return nil, errMeshDisabled
	}
	var caller session.Session
	if t.needsCaller {
		var err error
		if caller, err = b.d.resolveMCPCaller(ctx, agent, meta); err != nil {
			return nil, err
		}
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
		if !slices.Contains(t.keys, k) {
			return fmt.Errorf("unknown argument: %s", k)
		}
	}
	for _, k := range t.required {
		if v, ok := m[k]; !ok || string(v) == "null" || string(v) == `""` {
			return fmt.Errorf("missing required argument: %s", k)
		}
	}
	return nil
}
