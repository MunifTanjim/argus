# Single Machine

Once you've [installed Argus](/getting-started/installation), supervising Claude
Code on one machine takes a few commands. This page explains what's happening
underneath — the node and how discovery works — so you can run Argus the way that
fits you.

## Nodes

A **node** is the background process that does the real work: it discovers Claude
Code sessions, controls their tmux panes, and serves a local API (a unix socket)
that the [TUI](/guide/tui) talks to. You don't talk to the node directly — the TUI
does.

There are two ways a local node comes to life:

- **Ephemeral** — running `argus` with no node already up offers to spawn one. It's
  tied to that TUI and dies when you quit.

  ```sh
  argus          # open the TUI (can spawn an ephemeral node if none is running)
  ```

- **Persistent** — `argus start` runs a node in the foreground that keeps going
  regardless of whether a TUI is open.

  ```sh
  argus start    # run a persistent node
  ```

## Discovery

Argus finds Claude Code sessions by scanning your tmux panes — there's no
per-session setup. Just run `claude` inside a tmux session and it shows up:

```sh
tmux new -s work
cd ~/code/my-project
claude
```

## Keep an always-on node

Running `argus` spawns an ephemeral node that dies when you quit. To keep Argus
watching your sessions even with no TUI open — so status is current the moment you
reopen it — run a persistent node:

```sh
argus start
```

Leave it running (in its own tmux window, a `systemd`/`launchd` service, etc.), and
open the TUI against it whenever you want with `argus`.
