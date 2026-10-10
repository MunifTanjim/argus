# Codex tool shapes

Empirical shapes of Codex (`codex`) tool calls and results, captured from
`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` rollout files — not an official spec; treat unlisted fields as possible.

Parser: `internal/adapter/codex/parse.go`. Renderers: `internal/tui/tooldetail.go` (registered in `internal/tui/toolreg.go`).

## Envelope

Each line is `{timestamp, type, payload}`. Top-level `type` is one of `session_meta`,
`turn_context`, `event_msg`, `response_item`. Tool calls live in `response_item`
payloads, whose `payload.type` is one of:

- `function_call` — `{name, arguments, call_id}` (`arguments` is a JSON **string**)
- `function_call_output` — `{call_id, output}` (`output` is usually a string)
- `custom_tool_call` — `{name, input, call_id}` (`input` is a raw string, not JSON)
- `custom_tool_call_output` — `{call_id, output}`
- `web_search_call` — `{id, status, action}` (no paired output line)
- `message`, `reasoning` — assistant/user content (not tools)
- `tool_search_call` / `tool_search_output` — Codex's dynamic tool discovery; the
  parser ignores these (no tool item emitted)

A call pairs with its output by `call_id`. `spawn_agent`, and v1's
`wait_agent`/`close_agent`, become `ItemSubagent`; the rest become `ItemTool`.

A `function_call` may carry a `namespace`. The recorded tool name keeps it:
default-namespace tools (none or `functions`) stay bare, MCP namespaces join the
way Codex flattens them (`mcp__server__` + `tool`), `multi_agent_v1` tools stay
bare (older rollouts record them without it), and any other namespace is a
`namespace.` prefix (`collaboration.send_message`, `clock.sleep`). The prefix keeps a Codex tool from colliding with
another agent's tool of the same bare name.

## Multi-agent v2 and clock tools

| Tool | Input | Output |
|---|---|---|
| `collaboration.wait_agent` | `{timeout_ms}` | `{message, timed_out}` |
| `collaboration.send_message`, `collaboration.followup_task` | `{target, message}` (message may be encrypted) | empty |
| `collaboration.interrupt_agent` | `{target}` | `{previous_status}` |
| `collaboration.list_agents` | `{path_prefix?}` | `{agents: [{agent_name, agent_status}]}` |
| `clock.sleep` | `{duration_ms}` | `Wall time: …\nSleep completed.` |
| `wait` (code mode) | `{cell_id, yield_time_ms}` | exec-style result of the cell |

The parent's rollout records each subagent transition as an `event_msg`
`item_completed` whose item is `{type: "SubAgentActivity", id, kind, agent_thread_id,
agent_path}`. `id` is the call that caused it (the spawn, or a `send_message` /
`followup_task`), so a `started` item links a spawn to its child without any lookup.
`kind` is `started`, `interacted`, `completed` or `interrupted`; the latest one is the
child's status (running, completed, interrupted). The state DB's spawn edge stays
`open` until the agent is closed, so only its `closed` overrides that status.

The message that starts a subagent's turn is a `response_item` of type `agent_message`
(`author`, `recipient`, `content`): a readable header (`Message Type: NEW_TASK`,
`Sender: /root`, … `Payload:`) and usually an `encrypted_content` payload. It renders as
a user row naming the type and sender.

`target` and `agent_name` are agent paths (`/root/<task>`), though the model may pass a
bare task name (`delayed_print`). An agent status is a bare
state (`"running"`) or a one-key `{state: message}` object (`{"completed": "…"}`).

## Conventions

- `exec_command` and `apply_patch` results share a scaffolding preamble ending in
  `Output:\n`; everything after it is the command's own output, shown verbatim
  (the renderer splits there — a real output line may contain a colon).
- Agent status objects (`wait_agent`/`close_agent`) have the shape
  `{"state": "message"}` (exactly one key, e.g. `{"completed": "…"}`).

---

## exec_command — `function_call`

```json
{ "cmd": "pwd", "workdir": "/repo", "yield_time_ms": 1000, "max_output_tokens": 2000 }
```

Output (`function_call_output.output`, a string):

```
Chunk ID: ec45e3
Wall time: 0.0000 seconds
Process exited with code 0
Original token count: 12
Output:
/repo
```

## apply_patch — `custom_tool_call`

`input` is a raw patch string (not JSON):

```
*** Begin Patch
*** Update File: /path/to/file
@@
+added line
*** End Patch
```

Also `*** Add File:` / `*** Delete File:`. Output (`custom_tool_call_output.output`):

```
Exit code: 0
Wall time: 0.1 seconds
Output:
Success. Updated the following files:
M /path/to/file
```

## update_plan — `function_call`

```json
{ "plan": [ { "step": "Do the thing", "status": "in_progress" } ] }
```

`status` ∈ `pending` | `in_progress` | `completed`. Output: `Plan updated`.

## view_image — `function_call`

```json
{ "path": "/tmp/image.ppm", "detail": "high" }
```

Output is a content-part array, e.g.
`[{"type":"input_text","text":"image content omitted because it could not be processed"}]`.

## web_search — `web_search_call`

No `function_call`/output pair; the whole call is one payload:

```json
{ "type": "web_search_call", "id": "ws_…", "status": "completed",
  "action": { "type": "search", "query": "example domain", "queries": ["example domain"] } }
```

The parser stores `action` as the tool input (so `query` drives the header). There is
no separate result.

## spawn_agent — `function_call` → `ItemSubagent`

```json
{ "agent_type": "default", "message": "…task description…" }
```

Output:

```json
{ "agent_id": "019f278e-…", "nickname": "Volta" }
```

The child's `agent_id` and `nickname` are stamped onto the spawn item; the drill uses
`agent_id` to load the child transcript.

Multi-agent v2 (`namespace: "collaboration"`) names each task and may encrypt the
message (a `gAAAAA…` token):

```json
{ "task_name": "sleep_demo", "fork_turns": "all", "message": "gAAAAAB…" }
```

Output is the agent path, with `nickname` unless Codex hides it:

```json
{ "task_name": "/root/sleep_demo" }
```

The path resolves to the child thread through the daemon first: `thread/items/list`
for the spawn's turn (`internal_chat_message_metadata_passthrough.turn_id`) holds a
`subAgentActivity` item whose `id` is the spawn's `call_id` and whose `agentThreadId`
is the child; `thread/read` on the child gives its rollout `path` and `agentNickname`.
Without the daemon it falls back to Codex's state DB (`threads.agent_path` joined with
`thread_spawn_edges` on the spawning thread). Both describe only the live Codex home,
so a bundle root resolves by searching its own sessions directory. Found links are
cached; the open/closed status comes from `thread_spawn_edges` on each refresh. The task name stands in for
an encrypted message. A forked child's rollout starts with a copy of the parent's
history, from its second `session_meta` up to its own `thread_settings_applied` event;
the subagent view skips it. v2's `wait_agent` takes only `timeout_ms` and returns
`{ "message": "…", "timed_out": false }`.

## wait_agent — `function_call` → `ItemSubagent`

```json
{ "targets": ["019f278e-…"], "timeout_ms": 30000 }
```

Output:

```json
{ "status": { "019f278e-…": { "completed": "role=subagent\naction=…\nresult=ok" } },
  "timed_out": false }
```

`status` maps each target id to a `{state: message}` object.

## close_agent — `function_call` → `ItemSubagent`

```json
{ "target": "019f278e-…" }
```

Output:

```json
{ "previous_status": { "completed": "role=subagent\naction=…\nresult=ok" } }
```

`previous_status` is a single `{state: message}` object for the closed agent.

## exec — `custom_tool_call` (code mode)

`input` is raw JavaScript that calls Codex tools:

```js
const r = await tools.exec_command({cmd: "ls", workdir: "/repo", yield_time_ms: 10000});
text(r.output);
```

Output is a content-part array whose text joins to
`Script completed\nWall time 0.2 seconds\nOutput:\n<text>`. A failure reads
`Script failed\n…\nOutput:\nScript error:\n…`, and an abort is the bare string
`aborted by user after 1.0s`.

Commands the script runs are recorded between the call and its output as
`event_msg` `item_completed` items of type `CommandExecution`
(`command` argv, `cwd` as a `file://` URL, `aggregated_output`, `exit_code`,
`duration {secs, nanos}`, `source: "unified_exec_startup"`). The parser turns
each into an `exec_command` row and drops a successful exec row whose script
calls only `exec_command`. Other tools a script calls are not recorded; their
exec row stays, summarized by the `tools.<name>(` calls in the script.

## request_user_input — `function_call` (Plan mode only)

```json
{ "questions": [ { "header": "Size", "id": "size", "question": "Pick a size",
    "options": [ { "label": "Small", "description": "Choose Small." } ] } ] }
```

Output keys answers by question `id`. A note is an extra entry prefixed
`user_note: `:

```json
{ "answers": { "size": { "answers": ["Large", "user_note: but extra large please"] } } }
```

## request_user_input_async — `function_call`

```json
{ "questions": [ { "title": "Pick a color", "options": ["Red", "Blue"] } ] }
```

Output: `{"accepted":true}`. The user answers in a later message. The parser
turns an accepted call into assistant text (bold title, bulleted options), the
way the Codex TUI shows it.

A call whose arguments Codex rejects gets the output
`failed to parse function arguments: …` (any tool); the parser marks it as an
error. The model usually retries with fixed arguments.

## MCP tools — `function_call`

Expected as `name: "mcp__<server>__<tool>"` with JSON arguments; not yet seen in a
real rollout. Renderers display `server › tool`.
