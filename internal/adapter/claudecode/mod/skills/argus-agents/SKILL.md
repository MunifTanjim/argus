---
name: argus-agents
description: Use when you need to spawn other agent sessions, give them tasks, message other sessions, answer a worker's questions, or report a task result through argus
---

# Working with other agent sessions

The argus tools let you spawn agent sessions, message them, and answer their questions. Permission prompts and plan approvals always go to the user in the worker's own session. Session IDs have the form `<node>:<agent>:<agent session id>`. Messages are asynchronous: `send_message` and `reply_to_message` return a message ID and never wait.

If the argus tools are not available to you, use the `argus session` CLI. Each tool below lists its CLI form.

## When to spawn

Prefer your native subagent tool when it can do the task. Use `spawn_session` when the task needs a different agent, or when the user asks for an argus session.

## Tools

- `get_current_session`: your own session ID (`argus session whoami`)
- `list_sessions`: sessions you can see (`argus session list`)
- `get_session`: a session, its pending interaction, and its queued messages (`argus session show <id>`)
- `spawn_session`: start a child session; requires `agent` and an absolute `cwd`, optionally `name`, `model`, `prompt`, `request_id`; returns the session ID, and with `prompt` also a task ID (`argus session spawn --agent <agent> [--cwd <dir>, default: the current directory] [--name <name>] [--model <model>] [--prompt <text>] [--request-id <id>]`)
- `send_message`: message a session, optionally with `request_id` (`argus session send <id> <text> [--request-id <id>]`)
- `reply_to_message`: answer a message by its `message_id` (`argus session reply <msg-id> <text>`)
- `respond_to_interaction`: answer a question by its `message_id`, with `answers` or `behavior` deny (and optional `reason`) (`argus session respond <message-id> --answer "<question>=<option label>"`, or `deny [--reason "<text>"]`). It cannot answer permission prompts or plans.
- `get_session_output`: the session's last assistant reply (`argus session output <id>`)
- `get_session_transcript`: conversation transcript, optionally the last N entries (`argus session transcript <id> [--last N]`)
- `kill_session`: kill a child session, or your own; the session's children, and their children, are killed too; returns the killed session IDs (`argus session kill <id>`)
- `rename_session`: rename any session, not only your children; returns the name, and `applied` is false when the agent takes the name later (`argus session rename <id> <name>`)

## Retries

Give `spawn_session` and `send_message` a `request_id` that is unique for each action, for example `review-api-1`. If a call fails or times out and you are not sure that it ran, call it again with the same `request_id` and the same parameters. A repeat returns the first result and does not spawn or send again. A `request_id` used again with different parameters is an error.

## Messages arrive as prompts

A delivered message is an `<argus-message>` block: its `id`, `kind`, and sender are attributes, and one prompt can hold several blocks. After you `send_message` or `spawn_session` with a prompt, end your turn. The reply arrives later as a new prompt.

## Manager pattern

1. `spawn_session` a worker with a `prompt` and note the task ID.
2. End your turn. Do not poll.
3. The worker's result arrives as a prompt. If the worker ends a turn without replying, argus sends you a `turn_ended` message instead.
4. When a worker did not report, read its `get_session_transcript` (last 20 entries) or `get_session_output`, then `send_message` a follow-up.
5. If a worker asks a question, you receive an `interaction` message. Answer it with `respond_to_interaction` and its `message_id`. If a worker waits on a permission prompt or a plan, the message says the user must answer it: the worker waits for the user, so tell the user if they are not watching.
6. `kill_session` the worker when it is done.

## Worker pattern

A task arrives as a block with `kind="task"`; the prompt ends with a line that tells you how to report the result with the `reply_to_message` tool, using the task's `id`. Do the task yourself. Spawn no argus sessions unless the task says to. When the task is done, call `reply_to_message` with your result. Keep the result self-contained, since the sender reads only the reply.
