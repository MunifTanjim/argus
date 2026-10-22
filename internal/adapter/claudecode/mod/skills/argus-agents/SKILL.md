---
name: argus-agents
description: Use when you need to spawn other agent sessions, give them tasks, message other sessions, answer a worker's permission prompts, or report a task result through argus
---

# Working with other agent sessions

`argus session` lets you spawn agent sessions, message them, and answer their prompts. Session IDs have the form `<node>:<id>`. Messages are asynchronous: `send` and `reply` return a message ID and never wait.

## Commands

- `argus session whoami`: your own session ID
- `argus session list`: sessions you can see
- `argus session show <id>`: a session, its pending prompt, and its queued messages
- `argus session spawn --agent <agent> [--cwd <dir>] [--name <name>] [--model <model>] [--prompt <text>]`: start a child session; prints the session ID, and with `--prompt` also `task <task-id>`
- `argus session send <id> <text>`: message a session
- `argus session reply <msg-id> <text>`: answer a message by its ID
- `argus session respond <notice-id> allow|deny [--reason "<text>"]`: answer a permission notice
- `argus session respond <notice-id> --option <value>`: pick an option a notice offers
- `argus session respond <notice-id> --answer "<question>=<option label>"`: answer a question; repeat per question
- `argus session output <id>`: the session's last assistant reply
- `argus session transcript <id> [--last N]`: conversation transcript
- `argus session kill <id>`: kill a child session, or your own

## Messages arrive as prompts

A delivered message starts with `[argus message <id> from <label> (<session id>)]`. After you `send` or `spawn --prompt`, end your turn. The reply arrives later as a new prompt.

## Manager pattern

1. `argus session spawn --agent claude --name <name> --prompt "<task>"` and note the task ID.
2. End your turn. Do not poll.
3. The worker's result arrives as a prompt. If the worker ends a turn without replying, argus sends you a turn notice instead.
4. When a worker did not report, read `argus session transcript <id> --last 20` or `argus session output <id>`, then `send` a follow-up.
5. If a worker asks for permission or an answer, you receive a prompt notice that names the exact `respond` form to use.
6. `argus session kill <id>` when the worker is done.

## Worker pattern

A task message ends with `When the task is done, report the result: argus session reply <id> "<result>"`. When the task is done, run that command with your result. Keep the result self-contained, since the sender reads only the reply.
