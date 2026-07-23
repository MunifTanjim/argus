# TUI

The TUI is Argus's terminal interface — open it with `argus`. It shows a live list
of your agent sessions — Claude Code, Codex, and Antigravity — and lets you read
transcripts, watch screens, and control sessions.

<DemoVideo src="/screenshots/demo-tui.mp4" alt="Argus TUI — session list, transcript, and history across a fleet" />

```sh
argus
```

See [Single Machine](/guide/single-machine) for the node it connects to,
[Multi Machine](/guide/multi-machine) for reaching a remote gateway, and run
`argus --help` for every flag.

## Views

- **Sessions** — the session list, with live status at a glance.
- **Transcript** — the full conversation, foldable, with tool-call detail.
- **Live screen** — watch a session's terminal and type into it.
- **History** — browse past projects and sessions, and resume one to pick it back up.

## Export & View

Any past session can be exported to a self-contained `.argus` bundle and opened
later with no running node, gateway, or config.

A session's transcript can be exported from **History** to a `.argus` file in the
current working directory.

View a exported session bundle:

```sh
argus view session.argus
```

`.argus` files hold the session's raw transcript — full tool input and output.
Share them only with people you trust. To strip secrets first, view with `--redact`
and save a scrubbed copy.

```sh
argus view session.argus --redact
```
