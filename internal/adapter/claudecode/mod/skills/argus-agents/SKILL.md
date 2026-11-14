---
name: argus-agents
description: Use when you need to spawn other agent sessions, give them tasks, message other sessions, answer a worker's questions, or report a task result through argus
---

# Working with other agent sessions

`argus session` lets you spawn agent sessions, message them, and answer their questions. Permission prompts and plan approvals always go to the user in the worker's own session. Session IDs have the form `<node>:<id>`. Messages are asynchronous: `argus session send` and `argus session reply` return a message ID and never wait.

## When to spawn

Prefer your native subagent tool when it can do the task. Use `argus session spawn` when the task needs a different agent, or when the user asks for an argus session.

## Commands

- `argus session whoami`: your own session ID
- `argus session list`: sessions you can see
- `argus session show <id>`: a session, its pending interaction, and its queued messages
- `argus session spawn --agent <agent> [--cwd <dir>, default: the current directory] [--name <name>] [--model <model>] [--prompt <text>] [--request-id <id>]`: start a child session; prints the session ID, and with `--prompt` also `task <task-id>`
- `argus session send <id> <text> [--request-id <id>]`: message a session
- `argus session reply <msg-id> <text>`: answer a message by its ID
- `argus session respond <message-id> deny [--reason "<text>"]`: decline a question
- `argus session respond <message-id> --answer "<question>=<option label>"`: answer a question; repeat per question
- `argus session output <id>`: the session's last assistant reply
- `argus session transcript <id> [--last N]`: conversation transcript
- `argus session kill <id>`: kill a child session, or your own; the session's children, and their children, are killed too; prints the killed session IDs
- `argus session rename <id> <name>`: rename any session, not only your children; prints the name, and notes when the agent applies it later

## Retries

Give `argus session spawn` and `argus session send` a `--request-id` that is unique for each action, for example `review-api-1`. If a command fails or times out and you are not sure that it ran, run it again with the same `--request-id` and the same arguments. A repeat returns the first result and does not spawn or send again. A request ID used again with different arguments is an error.

## Messages arrive as prompts

A delivered message is an `<argus-message>` block: its `id`, `kind`, and sender are attributes, and one prompt can hold several blocks. After you `argus session send` or `argus session spawn --prompt`, end your turn. The reply arrives later as a new prompt.

## Manager pattern

1. `argus session spawn --agent claude --name <name> --prompt "<task>"` and note the task ID.
2. End your turn. Do not poll.
3. The worker's result arrives as a prompt. If the worker ends a turn without replying, argus sends you a `turn_ended` message instead.
4. When a worker did not report, read `argus session transcript <id> --last 20` or `argus session output <id>`, then `argus session send` a follow-up.
5. If a worker asks a question, you receive an `interaction` message that names the exact `argus session respond` form to use. If a worker waits on a permission prompt or a plan, the message says the user must answer it: the worker waits for the user, so tell the user if they are not watching.
6. `argus session kill <id>` when the worker is done.

## Worker pattern

A task arrives as a block with `kind="task"`; the prompt ends with a line that tells you how to report the result: `argus session reply <id> "<result>"` with the task's `id`. Do the task yourself. Spawn no argus sessions unless the task says to. When the task is done, run that command with your result. Keep the result self-contained, since the sender reads only the reply.
