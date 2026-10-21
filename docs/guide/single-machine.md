# Single Machine

This page explains how the node and discovery work on one machine, so you can
choose how to run Argus.

## Nodes

A **node** is the background process that discovers agent sessions, controls their
tmux panes, and serves a local API (a unix socket) to the [TUI](/guide/tui). The
TUI talks to the node, so you don't use the node directly.

A local node runs in one of two ways:

- **Ephemeral:** if no node is running, `argus` offers to spawn one. The node is
  tied to that TUI and exits when you quit. With `--gateway`, `argus` spawns a
  node that connects to that gateway, and does not ask.

  ```sh
  argus          # open the TUI (can spawn an ephemeral node if none is running)
  ```

- **Persistent:** `argus start` runs a node in the foreground. It keeps running
  whether or not a TUI is open, so the status is current when you open the TUI.
  Run it in its own tmux window, or as a `systemd` or `launchd` service.

  ```sh
  argus start    # run a persistent node
  ```

## Discovery

Argus finds running agent sessions automatically, inside or outside tmux. No
per-session setup is needed. Start a supported agent as usual and Argus
discovers it:

```sh
cd ~/code/my-project
claude          # or agy, codex, opencode
```

For a Claude Code or Antigravity session, run the agent inside tmux to see its
live screen and type into it. Outside tmux, the session still appears with its
status and transcript. A Claude Code session outside tmux also takes prompts
and permission answers if the
[Argus plugin](/getting-started/installation#connect-your-agents) is installed.

Codex and OpenCode sessions do not need tmux. To open a terminal for a Codex
session, Argus starts a `codex resume <id>` viewer.

Pi sessions do not need tmux either. Argus spawns `pi --mode rpc` sessions
itself; opening a terminal for one attaches a `pi --session <id>` viewer.

## Don't use tmux? Let Argus wrap it

`argus spawn` starts an agent inside tmux and attaches you to it with tmux
hidden, so it feels like running the agent directly:

```sh
cd ~/code/my-project
argus spawn claude          # or: argus spawn antigravity
```

You can also set a shell alias:

```sh
alias claude='argus spawn claude'
```
