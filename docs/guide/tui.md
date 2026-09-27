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

## Projects and workspaces

The TUI opens on the projects screen. The screen has three columns and a dock:

- **Left sidebar**: Home, then each project and its workspaces. A project is a git
  repository or a plain directory. A workspace is one git worktree.
- **Main pane**: the Home tabs, the sessions of the selected workspace, or an
  open session, file, or diff.
- **Right sidebar**: the **Files** tree and the **Changes** list of the current
  workspace.
- **Session dock**: under the main pane, while an open session waits for input.
  It holds the reply, the question, or the permission prompt.

Argus adds a project when a session starts in it. Press `g?` to see every key. The
main keys are:

| Key | Action |
|---|---|
| `<C-w>h` / `<C-w>l` | Move focus left or right |
| `<C-w>j` | In a session, move focus to the session dock |
| `<C-w>w` / `<C-w>W` | Cycle focus forward or backward |
| `s` | Start a session in the selected workspace |
| `a` | Create a workspace from a new branch, a branch, a PR, or an issue |
| `dd` / `D` | Remove a workspace, or remove it and discard its uncommitted changes |
| `T` | Change the target branch of a workspace |
| `r` / `H` / `P` | Rename, hide, or pin a project |
| `F` | Forget a project. Its files stay. |
| `<Leader>o` / `<Leader>e` | Show or hide the left sidebar, or the right sidebar |
| `S` | Run the workspace's setup script again |
| `L` | Show the output of the workspace's last setup run |

`<Leader>` is the leader key. The default leader is `<Space>`, so `<Leader>o` is `<Space>o` and `<Leader>e` is `<Space>e`. You can change these keys. See [Keymaps](../getting-started/configuration.md#keymaps).

A workspace with live sessions cannot be removed. Kill its sessions first.

While a setup script runs, the workspace row shows "setting up…". If it
fails, the row shows "setup failed", and the workspace pane shows the last
lines of its output. See
[Configuration](/getting-started/configuration#workspace-scripts).

The **Changes** list shows the uncommitted changes. Press `t` to show all changes
since the target branch. The list also shows the commits since the target branch.
Press `enter` on a file to open its diff, and `]f` or `[f` to open the next or
previous file.

Workspaces from a PR or an issue need the GitHub CLI. See
[Configuration](/getting-started/configuration#workspaces).

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
