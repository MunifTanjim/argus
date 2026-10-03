# Introduction

**Watch and control all your AI agents.**

When you run several AI coding agents, their sessions end up in different tmux
panes and on different machines. Argus shows them in one list: which are
working, which are waiting on a prompt, and which have finished. You can read
transcripts, watch and type into a session, answer prompts, and spawn,
interrupt, or kill sessions without leaving the TUI.

You can use Argus in a terminal on one machine, across several machines, or
from your phone, which gets a push notification when a session needs you.

Argus supports **Claude Code**, **Codex**, and **Antigravity**. It lists their
sessions together. Support for more agents can come later.

## Highlights

- **Multi-agent:** Claude Code, Codex, and Antigravity in one session list.
- **Zero-setup discovery:** Finds agent sessions in tmux. No per-session config.
- **Don't use tmux?** `argus spawn` runs an agent inside tmux for you.
- **Live status:** working, waiting, idle, or dead, based on each agent's hooks.
- **Transcripts:** The full conversation, with foldable sections and tool-call details.
- **Live screen:** Watch a session's terminal and type into it.
- **Lifecycle control:** Spawn, interrupt, or kill sessions and answer prompts directly.
- **Multi-machine:** Collect sessions from several machines and watch them in one TUI.
- **Mobile app:** An Android app with **push notifications**.

## How it fits together

Argus runs as a **node** on each machine. The node discovers your agent
sessions in tmux, tracks their status, and serves a local API. The TUI and the
agents' hooks use that API.

- On a **single machine**, run the [TUI](/guide/tui). It connects to the local
  node automatically.
- Across **several machines**, one node acts as a
  [gateway](/guide/multi-machine) and the other nodes connect to it. The TUI and
  the [mobile app](/guide/mobile-app) connect to the gateway.
