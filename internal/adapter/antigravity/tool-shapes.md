# Antigravity tool shapes

Empirical shapes of Antigravity (`agy`) tool calls and results, captured from
`~/.gemini/antigravity-cli/brain/*/.system_generated/logs/transcript_full.jsonl` — not an official spec; treat unlisted fields as possible.

Renderers: `internal/tui/tooldetail_antigravity.go`.

## Envelope

A `PLANNER_RESPONSE` line carries `tool_calls: [{name, args}]` (agy emits exactly
one per line). A tool's **result** is the immediately following line that is not a
`USER_INPUT`/`PLANNER_RESPONSE`; its `type` is a RESULT kind (below) and `content`
is a string. An errored call's result line has `type: "ERROR_MESSAGE"`.

## Conventions shared by all tools

- **Every** `args` object also carries `toolAction` and `toolSummary` (short
  human strings, e.g. `"Searching codebase"`). `toolSummary` (fallback
  `toolAction`) drives the collapsed-row preview via `toolPreview`; the detail
  renderers ignore both. Omitted from the per-tool `args` below.
- **Most** result `content` begins with a timestamp preamble:
  ```
  Created At: 2026-07-04T22:06:13+06:00
  Completed At: 2026-07-04T22:06:16+06:00
  ```
  `Completed At:` is absent for async/background tasks. `agyResultBody` strips
  this preamble.
- Some results append boilerplate instruction sentences that carry no reader
  signal (stripped by `stripAgyBoilerplate`):
  - `If relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission.`
  - `Do not output the path of this image to show to the user …`
- `CODE_ACTION` result diffs are wrapped in `[diff_block_start]` … `[diff_block_end]`
  markers around a unified `@@`-diff.

---

## run_command — result `RUN_COMMAND`

```json
{ "CommandLine": "git status", "Cwd": "/repo", "WaitMsBeforeAsync": 5000 }
```

Result body (after preamble), tab-indented; `Output:` may instead be
`Stdout:`/`Stderr:`:

```
				The command completed successfully.      (or: The command failed with exit code: N)
				Output:
				<command output, verbatim>
```

Background variant (long-running; no `Completed At:`):

```
Tool is running as a background task with task id: <conv>/task-39
Task Description: <cmd>
Task logs are available at: file://…/task-39.log
```

## grep_search — result `GREP_SEARCH`

```json
{ "Query": "probe", "SearchPath": "/repo", "IsRegex": false,
  "CaseInsensitive": true, "MatchPerLine": true }
```

Result body: one JSON object per line —
`{"File": "...", "LineNumber": 41, "LineContent": "..."}`.

## list_dir — result `LIST_DIRECTORY`

```json
{ "DirectoryPath": "/repo" }
```

Result body: one JSON object per line — `{"name": "...", "isDir": true}` for
directories, `{"name": "...", "sizeBytes": "2365"}` for files (`sizeBytes` is a
**string**).

## view_file — result `VIEW_FILE` (or `ERROR_MESSAGE`)

```json
{ "AbsolutePath": "/a.go", "IsSkillFile": false }
```

Result body:

```
File Path: `file:///a.go`
Total Lines: 21
Total Bytes: 17924
Showing lines 1 to 21
The following code has been modified to include a line number before every line, …
1: package main
2: func main() {}
```

## write_to_file — result `CODE_ACTION`

```json
{ "TargetFile": "/a.md", "CodeContent": "# Title\n…", "Description": "…",
  "Overwrite": true,
  "ArtifactMetadata": { "RequestFeedback": false, "Summary": "…", "UserFacing": false } }
```

Result body: `Created file file:///a.md with requested content.` (+ boilerplate).

## replace_file_content — result `CODE_ACTION`

```json
{ "TargetFile": "/a.md", "Description": "…", "Instruction": "…",
  "StartLine": 6, "EndLine": 11, "AllowMultiple": false,
  "TargetContent": "…old…", "ReplacementContent": "…new…" }
```

Result body: `The following changes were made … to: /a.md.` then a
`[diff_block_start]`-wrapped unified diff.

## multi_replace_file_content — result `CODE_ACTION`

```json
{ "TargetFile": "/a.md", "Description": "…", "Instruction": "…",
  "ArtifactMetadata": { "RequestFeedback": false, "Summary": "…", "UserFacing": false },
  "ReplacementChunks": [
    { "StartLine": 3, "EndLine": 4, "AllowMultiple": false,
      "TargetContent": "…", "ReplacementContent": "…" }
  ] }
```

Result body: same as `replace_file_content`.

## search_web — result `SEARCH_WEB`

```json
{ "query": "antigravity probe hook", "domain": "" }
```

Result body: `The search for "…" returned the following summary:` then a markdown
summary.

## generate_image — result `GENERATE_IMAGE`

```json
{ "Prompt": "A sleek … logo", "ImageName": "antigravity_logo", "AspectRatio": "1:1" }
```

Result body: `Using prompt: …` / `Generated image is saved at /…jpg.` (+ boilerplate).

## invoke_subagent — result `INVOKE_SUBAGENT` (or `ERROR_MESSAGE`)

```json
{ "Subagents": [ { "name": "test_subagent", "TypeName": "…" } ] }
```

`TypeName` is required (an omitted one yields an `ERROR_MESSAGE`:
`Subagents[0].TypeName is required`). Result body:
`Created the following subagents:` then a JSON object
`{conversationId, logAbsoluteUri, workspaceUris: [...]}`. Converted to `ItemSubagent`
(see `subagent.go`).

## define_subagent — result `GENERIC`

```json
{ "name": "test_subagent", "description": "…", "system_prompt": "…",
  "enable_write_tools": true, "enable_mcp_tools": false, "enable_subagent_tools": false }
```

Result body: `Subagent "test_subagent" defined successfully. It can now be invoked via invoke_subagent.`

## manage_subagents — result `GENERIC`

```json
{ "Action": "list", "ConversationIds": ["…"] }
```

Result body (free-form): e.g. `You have 1 active subagent(s):` then a JSON spec.

## manage_task — result `GENERIC` (may be followed by `SYSTEM_MESSAGE`)

```json
{ "Action": "status", "TaskId": "<conv>/task-132" }
```

Result body:

```
Task: <conv>/task-132
Status: RUNNING
Log: /…/task-132.log
Last progress: never

REMINDER: Do not call this tool again to poll …
```

(`stripAgyReminder` cuts at `REMINDER:`.) A completed task later surfaces a
`SYSTEM_MESSAGE` line — a conversation-flow event, not this tool's own result.

## ask_question — result `ASK_QUESTION`

```json
{ "questions": [
    { "question": "Which types …?", "is_multi_select": true,
      "options": ["(Recommended) Run all …", "Only run …"] } ] }
```

`options` are **plain strings** (which may themselves contain commas). Result body:
one `A<n>: <answer>` line per answered question (`A1:` = first question).

## ask_permission — result `GENERIC`

```json
{ "Action": "command", "Target": "echo", "Reason": "…" }
```

Result body: `Permission for command(echo) was granted. Reason provided by agent: …`

## list_permissions — result `GENERIC`

```json
{ }
```

Result body: free-form text — workspace access list then ordered grant lines
(`- command(*): ask`, `- read_file(/path): allowed`, …).

## send_message — result `GENERIC` (may be followed by `SYSTEM_MESSAGE`)

```json
{ "Recipient": "<conversationId>", "Message": "…" }
```

Result body: `Message sent to "<id>".`

## schedule — result `GENERIC` (background task)

```json
{ "DurationSeconds": "5", "Prompt": "…", "TimerCondition": "never" }
```

`DurationSeconds` is a **string**. Result body (no `Completed At:`):

```
Tool is running as a background task with task id: <conv>/task-39
Task Description: Timer: 5s, Prompt: …
Task logs are available at: file://…/task-39.log
```

---

## Conversation-flow result kinds (not tool-specific)

- **`CHECKPOINT`** — context-truncation summary injected by agy; begins
  `{{ CHECKPOINT N }}`.
- **`SYSTEM_MESSAGE`** — inter-agent/system notifications wrapped in
  `<SYSTEM_MESSAGE>` tags (e.g. a subagent reporting `Task completed.`).
