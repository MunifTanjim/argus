# Single Machine

After you [install Argus](/getting-started/installation), supervising your agents
on one machine takes a few commands. This page explains how the node and discovery
work, so you can choose how to run Argus.

## Nodes

A **node** is the background process that discovers agent sessions, controls their
tmux panes, and serves a local API (a unix socket) to the [TUI](/guide/tui). The
TUI talks to the node, so you don't use the node directly.

A local node runs in one of two ways:

- **Ephemeral:** if no node is running, `argus` offers to spawn one. The node is
  tied to that TUI and exits when you quit.

  ```sh
  argus          # open the TUI (can spawn an ephemeral node if none is running)
  ```

- **Persistent:** `argus start` runs a node in the foreground. It keeps running
  whether or not a TUI is open.

  ```sh
  argus start    # run a persistent node
  ```

## Discovery

Argus finds agent sessions by scanning your tmux panes. No per-session setup is
needed. Start a supported agent inside a tmux session and Argus discovers it:

```sh
tmux new -s work
cd ~/code/my-project
claude          # or codex, or agy
```

OpenCode sessions do not need tmux. Start `opencode` as usual and Argus discovers
it.

## Don't use tmux? Let Argus wrap it

Argus can watch the live screen and type into a session only when the agent runs
inside tmux. Outside tmux, a session still appears and works, but without those two
features. `argus spawn` starts an agent inside tmux and attaches you to it with tmux
hidden, so it feels like running the agent directly:

```sh
cd ~/code/my-project
argus spawn claude          # or: argus spawn codex, argus spawn antigravity
```

You can also set a shell alias:

```sh
alias claude='argus spawn claude'
```

## Keep an always-on node

Running `argus` spawns an ephemeral node that exits when you quit. To keep Argus
watching your sessions with no TUI open, so status is current when you reopen it,
run a persistent node:

```sh
argus start
```

Leave it running (in its own tmux window, a `systemd`/`launchd` service, and so on),
then run `argus` to open the TUI against it.
