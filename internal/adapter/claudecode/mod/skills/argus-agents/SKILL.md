---
name: argus-agents
description: Use when you need to spawn other agent sessions, give them tasks, message other sessions, answer a worker's permission prompts, or report a task result through argus
---

# Working with other agent sessions

The argus tools let you spawn agent sessions, message them, and answer their prompts. Session IDs have the form `<node>:<id>`. Messages are asynchronous: `send` and `reply` return a message ID and never wait.

If the argus tools are not available to you, use the `argus session` CLI. Each tool below lists its CLI form.

## Tools

- `whoami`: your own session ID (`argus session whoami`)
- `list`: sessions you can see (`argus session list`)
- `show`: a session, its pending prompt, and its queued messages (`argus session show <id>`)
- `spawn`: start a child session; requires `agent` and an absolute `cwd`, optionally `name`, `model`, `prompt`; returns the session ID, and with `prompt` also a task ID (`argus session spawn --agent <agent> [--cwd <dir>, default: the current directory] [--name <name>] [--model <model>] [--prompt <text>]`)
- `send`: message a session (`argus session send <id> <text>`)
- `reply`: answer a message by its `message_id` (`argus session reply <msg-id> <text>`)
- `respond`: answer a permission or question notice by `notice_id`, with `behavior` allow or deny (and optional `reason`), `option_value`, or `answers` (`argus session respond <notice-id> allow|deny [--reason "<text>"]`, `--option <value>`, `--answer "<question>=<option label>"`)
- `output`: the session's last assistant reply (`argus session output <id>`)
- `transcript`: conversation transcript, optionally the last N entries (`argus session transcript <id> [--last N]`)
- `kill`: kill a child session, or your own (`argus session kill <id>`)

## Messages arrive as prompts

A delivered message starts with `[argus message <id> from <label> (<session id>)]`. After you `send` or `spawn` with a prompt, end your turn. The reply arrives later as a new prompt.

## Manager pattern

1. `spawn` a worker with a `prompt` and note the task ID.
2. End your turn. Do not poll.
3. The worker's result arrives as a prompt. If the worker ends a turn without replying, argus sends you a turn notice instead.
4. When a worker did not report, read its `transcript` (last 20 entries) or `output`, then `send` a follow-up.
5. If a worker asks for permission or an answer, you receive a prompt notice that names the notice ID to `respond` to.
6. `kill` the worker when it is done.

## Worker pattern

A task message ends with a line that tells you how to report the result with the `reply` tool, using the message's ID. When the task is done, call it with your result. Keep the result self-contained, since the sender reads only the reply.
