# Introduction

**Watch and control all your AI coding sessions — from one place.**

Run more than one AI coding agent and they scatter across tmux panes and
machines. Argus pulls them into one view: which are working, which are stuck on a
prompt, which just finished. Read transcripts, watch and type into a session,
answer prompts, spawn/interrupt/kill — without leaving the TUI.

Start local in a terminal. Scale to a fleet across machines. Get a push
notification on your phone when a session needs you.

Argus supports **Claude Code**, **Codex**, and **Antigravity** — supervise them
side by side in one session list, with room for more agents over time.

## Highlights

- **Multi-agent** — Claude Code, Codex, and Antigravity, in one session list.
- **Zero-setup discovery** — finds agent sessions in tmux. No per-session config.
- **Don't use tmux?** — `argus spawn` wraps an agent in tmux for you.
- **Live status** — working / waiting / idle / dead, from each agent's hooks.
- **Transcripts** — full conversation, foldable, drill into tool calls.
- **Live screen** — watch a session's terminal and type into it.
- **Lifecycle control** — spawn, interrupt, kill, answer prompts in place.
- **Fleet mode** — aggregate machines, watch them all from one TUI.
- **Mobile app** — Android companion with **Push Notification**.

## How it fits together

Argus runs as a **node** on each machine: it discovers your agent sessions in
tmux, tracks their status, and serves a local API that the TUI and the
agents' hooks talk to.

- On a **single machine**, you just run the [TUI](/guide/tui); it connects to
  that machine's local node automatically.
- Across **several machines**, one node acts as a [gateway](/guide/multi-machine) that the
  others dial into, and the TUI — or your [phone](/guide/mobile-app) — connects
  to that single endpoint.
