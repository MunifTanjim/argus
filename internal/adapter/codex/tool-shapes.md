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

A call pairs with its output by `call_id`. `spawn_agent`/`wait_agent`/`close_agent`
become `ItemSubagent`; the rest become `ItemTool`.

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
