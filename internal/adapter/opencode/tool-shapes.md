# OpenCode tool shapes

Empirical shapes of OpenCode tool calls, captured from the running service API at
`/api/session/<id>/message` (OpenCode v2). Not an official spec; treat unlisted fields as possible.

Parser: `internal/adapter/opencode/parse.go`. Service client: `internal/adapter/opencode/service.go`.

## Message structure

`GET /api/session/<id>/message` returns `{"data": [{message}], "cursor": {...}}`.

Each message:

```json
{
  "id": "msg_…",
  "time": { "created": 1789…, "streamed": 1789…, "completed": 1789… },
  "type": "assistant",
  "agent": "build",
  "model": { "id": "…", "providerID": "…", "variant": "…" },
  "content": [ … parts … ]
}
```

`content` holds mixed part types: `text`, `reasoning`, `step-start`, `step-finish`, `tool`, etc.

## Tool part envelope

```json
{
  "type": "tool",
  "id": "call_… or toolu_…",
  "name": "<tool-name>",
  "executed": false,
  "state": { … },
  "time": { "created": 1789…, "ran": 1789…, "completed": 1789… }
}
```

`state.status` ∈ `pending` | `running` | `completed` | `error`.

Completed state:

```json
{
  "status": "completed",
  "input": { … tool-specific … },
  "content": [ { "type": "text", "text": "…" } ],
  "metadata": { … tool-specific … }
}
```

Error state:

```json
{
  "status": "error",
  "input": { … },
  "error": { "type": "aborted", "message": "…" }
}
```

---

## shell

Runs a shell command with an explicit working directory.

```json
{ "command": "git log --oneline -5", "workdir": "/path/to/repo" }
```

Content: one or two text parts — command stdout/stderr, then an exit message.

```
[
  { "type": "text", "text": "…stdout…" },
  { "type": "text", "text": "Command exited with code 0." }
]
```

Metadata:

```json
{ "status": "completed", "exit": 0, "truncated": false }
```

## bash

Runs a bash command; carries a description but no explicit workdir.

```json
{ "command": "go build ./...", "description": "Build all packages" }
```

Content: text with command output or `"(no output)"`.

Metadata:

```json
{ "output": "…", "exit": 0, "description": "Build all packages", "truncated": false }
```

## execute

Runs a code snippet, not a shell command (unlike `bash`/`shell`): the input is code, rendered
without a shell prompt.

```json
{ "code": "print(2 + 2)" }
```

Content: the execution output as text.

## read

Reads a file, optionally with line range.

```json
{ "path": "internal/foo/bar.go", "limit": 50, "offset": 10 }
```

`limit` and `offset` are optional. Content:

```
Read file internal/foo/bar.go, lines 10-59
10: package foo
…
[Output truncated. Continue reading with offset: 60]
```

Metadata:

```json
{ "truncated": true }
```

## edit

Replaces `oldString` with `newString` in a file. `path` field name varies by agent model —
some emit `"path"`, others `"filePath"`.

```json
{ "path": "internal/foo/bar.go", "oldString": "…before…", "newString": "…after…" }
```

Content: `"Edited internal/foo/bar.go (1 replacement)"`.

Metadata:

```json
{
  "files": [
    {
      "file": "internal/foo/bar.go",
      "patch": "Index: …\n--- …\n+++ …\n@@…",
      "status": "modified",
      "additions": 3,
      "deletions": 2
    }
  ],
  "truncated": false
}
```

## write

Writes (creates or overwrites) a file.

```json
{ "filePath": "/absolute/path/to/file.go", "content": "package foo\n…" }
```

Content: `"Wrote file successfully."`.

Metadata:

```json
{ "diagnostics": {}, "filepath": "/absolute/path/to/file.go", "exists": false, "truncated": false }
```

`exists` is `false` when the file was newly created, `true` when overwritten.

## grep

Searches files for a pattern.

```json
{ "path": "internal", "pattern": "http\\.Client", "include": "*.go" }
```

`include` is optional. Content: match list or `"No matches found"`.

Metadata:

```json
{ "matches": 16, "truncated": false }
```

## glob

Finds files matching a glob pattern.

```json
{ "pattern": "internal/transport/*.go" }
```

Content: newline-separated absolute paths.

Metadata:

```json
{ "count": 12, "truncated": false }
```

## webfetch

Fetches a URL and converts it to text.

```json
{ "url": "https://opencode.ai/v2/docs/config", "format": "markdown" }
```

Content: the page body in the requested format.

Metadata:

```json
{ "contentType": "text/html", "truncated": false }
```

## websearch

Searches the web for a query.

```json
{ "query": "opencode config schema" }
```

Content: the search results as text.

## question

Presents one or more multiple-choice questions to the user. Each question has a header, a
prompt, and labelled options.

```json
{
  "questions": [
    {
      "header": "Scheme rule",
      "question": "Allow http:// in private mode?",
      "options": [
        { "label": "Allow http + https (Recommended)", "description": "…" },
        { "label": "Keep https-only", "description": "…" }
      ]
    }
  ]
}
```

When the user answers, `state.status` becomes `"completed"` with a content part showing
the selected option. When dismissed, `state.status` is `"error"` with:

```json
{ "type": "aborted", "message": "The user dismissed this question" }
```

## todowrite

Writes the agent's task list.

```json
{
  "todos": [
    { "content": "Fix the bug", "priority": "high", "status": "in_progress" },
    { "content": "Write tests", "priority": "medium", "status": "pending" }
  ]
}
```

`priority` ∈ `high` | `medium` | `low`. `status` ∈ `pending` | `in_progress` | `completed`.

Content: JSON-serialised todo array (same as input).

Metadata:

```json
{ "todos": [ … same array … ] }
```

## skill

Loads a named skill and injects its content into context.

```json
{ "id": "skill-name" }
```

Content: the full skill XML wrapped in `<skill_content>…</skill_content>`.

Metadata:

```json
{ "name": "SkillName", "directory": "/builtin", "truncated": false }
```

## task

Dispatches parallel sub-tasks (multi-agent). Input carries an array of task descriptors;
the exact shape varies by agent.

```json
{ "tasks": [ … ] }
```

On error (e.g. spawning fails), `state.status` is `"error"` with no content or metadata.

## subagent

Dispatches a single sub-agent (the single-agent counterpart to `task`). The child runs in its
own session, drillable via `state.metadata.sessionID`.

```json
{ "agent": "explore", "description": "Explore APNs credential handling", "prompt": "…" }
```

`agent` may instead be `subagent_type`. On completion, `state.metadata`:

```json
{ "sessionID": "ses_…", "status": "completed", "truncated": false }
```
