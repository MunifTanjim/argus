# Claude Code tool shapes

Empirical shapes of Claude Code tool calls and results, captured from
`~/.claude/projects/*/*.jsonl` session transcripts — not an official spec; treat unlisted fields as possible.

Parser: `internal/adapter/claudecode/parser/`. Renderers: `internal/tui/toolreg.go` (TUI), `app/lib/ui/tool_registry.dart` (app).

## Envelope

Each line is a JSON object with a `type` (`user`, `assistant`, `system`, `summary`).
An `assistant` line's `message.content` array carries tool calls:

```json
{ "type": "tool_use", "id": "toolu_…", "name": "Bash", "input": { … } }
```

The matching result arrives in the next `user` line's `message.content`:

```json
{ "type": "tool_result", "tool_use_id": "toolu_…", "content": "…", "is_error": false }
```

`content` is a string or an array of `{type:"text", text}` parts. `Task` and
`Agent` become `ItemSubagent` (drillable into the child trace); `Skill` becomes
`ItemSkill`; the rest become `ItemTool`.

## A note on the task tools

Claude Code uses the **Task suite** (`TaskCreate`, `TaskUpdate`,
`TaskList`, `TaskGet`, `TaskOutput`, `TaskStop`) for todo/subtask management, and
`Agent` to spawn subagents. `TodoWrite` is legacy. `ToolSearch`, `LSP`, and `mcp__*`
tools also appear; `mcp__*` names are dynamic and render generically.

---

## Bash — file/shell

```json
{ "command": "ls -la", "description": "Check dir" }
```

Result: raw stdout/stderr text. `BashOutput`/`KillShell` take a `shell_id` (generic
body).

## Read

```json
{ "file_path": "/abs/path.go", "offset": 140, "limit": 110 }
```

Result: `cat -n` numbered file content (already carries line numbers).

## Edit / MultiEdit / Write / NotebookEdit

```json
{ "file_path": "/abs/path.go", "old_string": "…", "new_string": "…", "replace_all": false }
```

`MultiEdit`: `{file_path, edits:[{old_string,new_string,replace_all}]}`. `Write`:
`{file_path, content}`. `NotebookEdit`: `{notebook_path, new_source, …}`. Rendered as
a colored diff.

## Grep

```json
{ "pattern": "isCopyOnly", "output_mode": "content", "path": "/abs", "glob": "*.go" }
```

## Glob / LS

```json
{ "pattern": "**/*seek*", "path": "/abs" }
```

## WebFetch / WebSearch

```json
{ "url": "https://…", "prompt": "…" }
```

`WebSearch`: `{query}`. Result is markdown.

## AskUserQuestion

```json
{ "questions": [ { "question": "…", "header": "Test target", "multiSelect": false,
  "options": [ { "label": "…", "description": "…", "preview": "…" } ] } ] }
```

The answered result carries `"question"="answer"` pairs; the renderer marks the
chosen option(s).

## ExitPlanMode / EnterPlanMode

```json
{ "plan": "## markdown plan…" }
```

`plan` may be absent (`{}`). The app renders the plan as markdown.

## Agent / Task — subagent spawn (`ItemSubagent`)

```json
{ "subagent_type": "Explore", "description": "Explore encryption handling", "prompt": "…" }
```

`subagent_type` drives the row/trace label; the drill loads the child agent's trace.

## Skill — `ItemSkill`

```json
{ "skill": "superpowers:brainstorming" }
```

A skill load into the current context (not a subagent spawn). The `skill`
identifier drives the row label; the tool_result carries only "Launching
skill…", so the skill file body — injected as a meta text turn right after the
call — is attached as the item's result and surfaces on drill.

## TaskCreate

```json
{ "subject": "Make AES reader seekable", "description": "…", "activeForm": "Implementing …" }
```

## TaskUpdate

```json
{ "taskId": "5", "status": "in_progress" }
```

Other fields: `subject`, `description`, `activeForm`, `owner`, `addBlocks`,
`addBlockedBy`, `metadata`. Rendered as `taskId` + changed key/value lines.

## TaskGet / TaskStop / TaskOutput / TaskList

```json
{ "taskId": "10" }            // TaskGet
{ "task_id": "bb8235x1g" }    // TaskStop
{ "task_id": "afe…", "block": true, "timeout": 180000 }  // TaskOutput
{}                             // TaskList
```

Generic body.

## ToolSearch

```json
{ "query": "select:ExitPlanMode", "max_results": 1 }
```

## LSP

```json
{ "operation": "goToDefinition", "filePath": "/abs.go", "line": 64, "character": 29 }
```
