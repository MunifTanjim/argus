# Pi (`pi`) tool shapes

Empirical shapes of Pi tool calls and results, captured from
`~/.pi/agent/sessions/--<cwd>--/*_<id>.jsonl` session files (format v3) and the
RPC `get_entries` payloads — plus the pi docs for roles that need a live model
to observe (no provider auth in this environment yet).

Parser: `internal/adapter/pi/parse.go`. Renderers: `internal/tui/toolreg.go`
(`agentPi` entries reuse the opencode renderers) and
`app/lib/ui/tool_registry.dart` (`pi` entries).

## Tool call (assistant content block, type `toolCall`)

```json
{"type": "toolCall", "id": "call_abc", "name": "bash",
 "arguments": {"command": "ls -la"}, "namespace": "mcp__srv__tool"}
```

- `name` is the bare tool name; `namespace` (optional) qualifies MCP /
  dynamically loaded tools. Displayed as `namespace.name`.
- `arguments` is a JSON object; common keys per tool below.

## Tool result (role `toolResult` message, joined by `toolCallId`)

```json
{"role": "toolResult", "toolCallId": "call_abc", "toolName": "bash",
 "content": [{"type": "text", "text": "total 0"}], "isError": false,
 "timestamp": 1791741604000}
```

- `content` is text/image blocks; `details` (tool-specific structured data)
  exists on some results and is currently folded into the text rendering.
- Nested tool calls (codemode scripts calling other tools) appear only as
  bounded `nestedCalls` metadata on the calling result — never as transcript
  entries.

## Built-in tools and their identifying input keys

| Tool  | Preview key(s)         | Notes                              |
|-------|------------------------|------------------------------------|
| read  | `path`, `filePath`     | text + image reads                 |
| bash  | `command` (first line) | shell; no `shell` alias in pi     |
| edit  | `path`, `filePath`     | exact-match replacement            |
| write | `filePath`, `path`     | create/overwrite                   |
| grep  | `pattern`              | content search                     |
| find  | `path`, `pattern`      | glob search                        |
| ls    | `path`                 | directory listing                  |

Extension tools (`codemode`, `tool_search`, MCP `mcp__*`) keep their raw names;
their arguments render as generic JSON until observed shapes justify renderers.

## Non-tool message roles

- `bashExecution` (direct `bash` RPC command, not an LLM tool): rendered as a
  shell row — `Text` is the command (+cancelled marker), `Detail` the output,
  error on nonzero exit.
- `branchSummary` / `branch_summary` entry: system row "Branch summary".
- `compaction` entry / `compactionSummary`: compact boundary row.
- `model_change` / `thinking_level_change`: system rows.
- `custom` (state) entries: skipped. `custom_message` with `display: true`:
  text row; `display: false`: skipped.
- `context_edit`: `null` replacement omits the target; otherwise folds the
  replacement content in place of the original.
- `usage` entries: folded into token totals only, no rows.
- `system` messages (prompt sections / tool loadout): skipped, not conversation.
