# Configuration

The defaults work out of the box — no config needed to start.

When you do want to tweak something, every setting is available three ways, in
priority order: a **command-line flag** (e.g. `--token`), an **`ARGUS_*`
environment variable**, or a key in the **YAML config file** — otherwise the
built-in default applies. A set flag wins over an env var, which wins over the
file.

Run `argus <command> --help` for the settings each command accepts — that's the
authoritative list.

## Config file

Argus reads `$XDG_CONFIG_HOME/argus/config.yaml` by default (typically
`~/.config/argus/config.yaml`). Point at a different file with `--config` or
`$ARGUS_CONFIG`. A missing default file is fine; a missing explicit `--config`
path is an error.

`argus config dir` prints the config directory path — handy in scripts or when you
can't remember where it lives:

```sh
argus config dir
```

A minimal example:

```yaml
# ~/.config/argus/config.yaml
token: shared-secret    # gateway token — see Multi Machine

push:
  desktop:
    enabled: true       # native desktop notifications on this node (macOS, opt-in)

log:
  level: info           # trace | debug | info | warn | error | fatal
  format: pretty        # pretty | json
```

## Workspaces

In the TUI, `a` creates a git worktree for a new workspace. Two templates set the
names. Both use Go template syntax.

::: v-pre
| Key | Default | Variables |
|---|---|---|
| `workspace.worktree-dir-template` | `.worktrees/{{.Branch}}` | `.Repo`, `.Branch` |
| `workspace.issue-branch-template` | `issue-{{.Number}}-{{.Slug}}` | `.Number`, `.Title`, `.Slug` |
:::

A relative worktree path starts at the project's main worktree. `.Repo` is the
name of the main worktree's directory. `.Slug` is the issue title in lowercase
with dashes, cut to 40 characters. A workspace from a PR uses the local branch
`pr-<number>`, and `.Branch` is that name.

The node checks both templates when it starts. A template with an unknown
variable stops the start with an error that names the key.

```yaml
workspace:
  worktree-dir-template: ../{{.Repo}}-{{.Branch}}
  issue-branch-template: issue-{{.Number}}
```

The environment variables are `ARGUS_WORKSPACE_WORKTREE_DIR_TEMPLATE` and
`ARGUS_WORKSPACE_ISSUE_BRANCH_TEMPLATE`.

The default path is inside the repository. Add `.worktrees/` to `.gitignore` or
to `.git/info/exclude`. If you do not, git shows the worktrees as untracked files.

Workspaces from a PR or an issue need these:

- The GitHub CLI (`gh`), logged in with `gh auth login`.
- An `origin` remote on `github.com`.

## Workspace scripts

A project can run a script after argus creates a workspace (`setup`) and
before argus removes one (`teardown`). Put them in the repository:

```toml
# .argus/settings.toml (commit it)
[scripts]
setup    = "pnpm install && ln -s \"$ARGUS_ROOT_PATH/.env\" .env"
teardown = "docker compose down --volumes"
```

`.argus/settings.local.toml` holds personal changes. Add it to `.gitignore`.
A key in the local file replaces the same key in the shared file. An empty
value (`setup = ""`) turns a script off. Argus reads both files from the
project's main worktree each time it runs a script. If a settings file does not
parse, setup fails with the parse error, and a remove that is not forced stops.

Scripts run with `$SHELL -c` (or `/bin/sh`) in the workspace directory, with
these environment variables:

| Variable | Value |
|---|---|
| `ARGUS_WORKSPACE_PATH` | The workspace directory |
| `ARGUS_ROOT_PATH` | The main worktree |
| `ARGUS_WORKSPACE_NAME` | The base name of the workspace directory |
| `ARGUS_BRANCH` | The workspace branch (empty for a detached HEAD) |
| `ARGUS_TARGET_BRANCH` | The target branch |

The shell is not interactive. It does not read rc files such as `.zshrc` or
`.bashrc`, and `PATH` starts as the `PATH` of the argus node. The shell still
reads the files that every shell reads, for example `~/.zshenv` for zsh and
the file in `$BASH_ENV` for bash. When the node runs as a service, the script
does not find tools that an rc file adds to `PATH` (for example, nvm or pnpm).
Use full paths, or set `PATH` in the script.

- **Setup** runs in the background after the workspace exists. It stops after
  15 minutes. If it fails, the workspace stays. Run it again with `S` in the
  TUI or `argus workspace setup <workspace>`.
- **Teardown** runs before argus deletes the worktree. It stops after 20
  seconds. If it fails, the remove stops and the worktree stays. A forced
  remove (`D`, or `--force`) continues and shows the failure as a warning. If
  the workspace has uncommitted changes, a remove that is not forced stops
  before teardown runs.

Make both scripts safe to run more than one time.

## Keymaps

The default keys follow Vim. The command table lists them. You can change any key in `tui.keymap`. A mapping has a key sequence on the left and a command name on the right. `argus view` also uses these keymaps.

A command name is one of these:

- A verb and an argument that change the view or the focus, for example `focus left` or `toggle line-wrap`. `open` alone opens the selected item.
- An entity and an action on the selected entity, for example `project pin` or `session kill`.
- A single word: `back`, `quit`, `help`, `refresh`, or `filter-projects`.

Extra spaces in a command name do not matter.

```yaml
tui:
  leader-key: "<Space>"
  key-timeout: 1s
  keymap:
    global:
      "<Leader>x": toggle show-gone
      "<C-q>": quit
    transcript:
      "j": next card
      "gg": ""
```

- A section names a part of the screen: `project-tree`, `file-tree`, `changes`, `home`, `history`, `logs`, `workspace`, `file`, `transcript` (with its card detail), `session-dock`, or `global`.
- Priority, from highest to lowest: the section, then `global`, then the defaults.
- A mapping adds a key to the command. The default keys stay, unless you remove a key with `""` or map it to a different command.
- A mapping in `global` applies only in sections that have its command.
- Some pairs of commands share a default key: `P` for `project pin` and `project unpin`, `H` for `project hide` and `project unhide`, `<Space>` for `option select` and `option unselect`, and `<Tab>` for `focus prompt` and `focus transcript`. The key runs the command that applies. For example, `P` unpins a pinned project. If you map the shared key to one command of a pair, the other command loses that key. To move the key, map both commands.
- A command that does not apply does nothing. For example, a key mapped only to `project unpin` does nothing on a project that is not pinned.
- `transcript` also covers the card detail and the redaction list, so a key that you map there leaves its default command in all three views. For example, `"k": prev card` also removes `k` from `prev` in the card detail and the redaction list.
- `projects`, `session`, and `detail` are not section names. Argus skips them and shows a hint at startup. Use `project-tree`, `workspace`, `file`, `file-tree`, or `changes` for `projects`. Use `transcript` or `session-dock` for `session`. Use `transcript` for `detail`.
- In a text input, sequences do not work. Single keys of text input commands, for example `workspace pick-target` and `answer submit`, use the keymaps.
- y/n prompts and the live screen do not use keymaps.
- Environment variables cannot set keymaps. `key-timeout` has `ARGUS_TUI_KEY_TIMEOUT`.

### Leader

`tui.leader-key` sets the key that `<Leader>` stands for. The default is `<Space>`. The environment variable is `ARGUS_TUI_LEADER_KEY`.

The leader must be one key. A leader that is not valid notation or is more than one key triggers a startup warning that names it. Argus uses `<Space>` instead.

In text inputs, the leader has no meaning. A printable leader such as `<Space>` types itself.

### Key notation

::: v-pre
| Form | Meaning |
|---|---|
| `g`, `G`, `.`, `?` | A plain character. Case matters. |
| `g.`, `gg`, `g<C-b>` | A sequence: the keys in order. |
| `<C-x>` | ctrl+x |
| `<M-x>`, `<A-x>` | alt+x |
| `<S-x>` | shift+x |
| `<D-x>` | super+x |
| `<C-S-b>`, `<M-C-t>` | More than one modifier, in any order. |
| `<CR>`, `<Enter>`, `<Return>` | enter |
| `<Esc>` | escape |
| `<Space>` | space |
| `<Tab>`, `<S-Tab>` | tab, shift+tab |
| `<BS>` | backspace |
| `<Up>`, `<Down>`, `<Left>`, `<Right>` | arrows |
| `<PageUp>`, `<PageDown>`, `<Home>`, `<End>` | paging keys |
| `<F1>` to `<F12>` | function keys |
| `<lt>` | the `<` character |
| `<Leader>` | the leader key (set by `tui.leader-key`, default `<Space>`) |
:::

Modifier letters and key names are not case-sensitive: `<cr>` equals `<CR>`, and `<c-b>` equals `<C-b>`. With ctrl, the letter case does not matter: `<C-B>` equals `<C-b>`. Write shift as `S-`. With alt, the letter case matters: `<M-a>` and `<M-A>` are different keys.

### Sequences

If a key maps to a command and also starts a longer sequence, the TUI waits `key-timeout` for the next key. If the timer ends, the mapped command runs. If a key only starts a longer sequence and the timer ends, the key is dropped.

Press `<Esc>` to cancel pending keys. While keys are pending, the footer shows the pending keys and the keys that can follow with their commands. It also shows the command that runs when the timer ends as `(wait) <command>`.

If the next key fits no sequence, the TUI runs the complete part first, if one exists. Then it handles the new key as a new sequence. For example, with `tx` mapped, pressing `t` then `j` runs `toggle diff-vs-target`, then `next`.

### Keys that need the Kitty keyboard protocol

These mappings need the Kitty keyboard protocol:

- a key with two or more modifiers
- a key with super, for example `<D-x>`
- `<C-CR>`, `<S-CR>`, `<C-Tab>`, `<C-BS>`, and `<S-BS>`
- ctrl with a character other than a letter or `@ [ \ ] ^ _ ?`, for example `<C-.>` or `<C-1>`

If the terminal did not report the protocol, the TUI shows a warning at startup:

`keymap: <C-S-b> needs a terminal with the Kitty keyboard protocol`

### Errors

A bad entry is skipped. The other entries still load. A bad entry is one of these:

- an unknown screen
- an unknown command, or an unknown argument or action
- bad notation
- a shell command
- a sequence for a command that works only in a text input (for example `workspace pick-target`)

At startup, the footer shows the first error and a count:

`keymap: transcript "gt": unknown argument "tpo" for goto (bottom, top) (+2 more)`

### Commands

The `g?` sequence shows the command name next to each key.

<!-- keymap-commands:start -->
| Command | Sections | Default keys |
|---|---|---|
| `answer submit` | session-dock | `<CR>` |
| `back` | changes, file, file-tree, history, home, logs, project-tree, session-dock, transcript, workspace | `<Esc>` |
| `filter-projects` | file, project-tree, workspace | `/` |
| `focus down` | changes, file, file-tree, history, home, logs, project-tree, transcript, workspace | `<C-w>j` |
| `focus left` | changes, file, file-tree, history, home, logs, project-tree, transcript, workspace | `<C-w>h` |
| `focus next` | changes, file, file-tree, history, home, logs, project-tree, transcript, workspace | `<C-w>w` |
| `focus prev` | changes, file, file-tree, history, home, logs, project-tree, transcript, workspace | `<C-w>W` |
| `focus prompt` | file, session-dock, transcript | `<Tab>` |
| `focus right` | changes, file, file-tree, history, home, logs, project-tree, transcript, workspace | `<C-w>l` |
| `focus up` | changes, file, file-tree, history, home, logs, project-tree, transcript, workspace | `<C-w>k` |
| `fold close` | changes, file-tree, project-tree, transcript | `<Left>` `h` `zc` |
| `fold open` | changes, file-tree, project-tree, transcript | `<Right>` `l` `zo` |
| `goto bottom` | changes, file, file-tree, history, home, logs, project-tree, transcript, workspace | `G` |
| `goto top` | changes, file, file-tree, history, home, logs, project-tree, transcript, workspace | `gg` |
| `help` | changes (over the workspace pane), file (over the workspace pane), file-tree (over the workspace pane), history (project list), home, logs, project-tree, workspace | `g?` |
| `next` | changes, file-tree, history, home, project-tree, session-dock, transcript, workspace | `<Down>` `j` |
| `next card` | transcript | `}` |
| `next diff-file` | file | `]f` |
| `open` | changes, file-tree, history, home, project-tree, transcript, workspace | `<CR>` |
| `open live-screen` | session-dock, transcript | `<C-t>` |
| `open setup-log` | file, project-tree, workspace | `L` |
| `open tmux-pane` | home, workspace | `O` |
| `option select` | session-dock | `<Space>` |
| `option unselect` | session-dock | `<Space>` |
| `prev` | changes, file-tree, history, home, project-tree, session-dock, transcript, workspace | `<Up>` `k` |
| `prev card` | transcript | `{` |
| `prev diff-file` | file | `[f` |
| `project forget` | file, project-tree, workspace | `F` |
| `project hide` | file, project-tree, workspace | `H` |
| `project pin` | file, project-tree, workspace | `P` |
| `project rename` | file, project-tree, workspace | `r` |
| `quit` | changes (over the workspace pane), file (over the workspace pane), file-tree (over the workspace pane), home, project-tree, workspace | `Q` |
| `redaction add` | transcript | `d` |
| `redaction list` | transcript | `D` |
| `redaction remove` | transcript | `u` |
| `redaction save` | transcript | `W` |
| `refresh` | changes, file, file-tree, history, home, project-tree, workspace | `gr` |
| `scroll down` | file, logs, transcript | `<Down>` `j` |
| `scroll half-page-down` | changes, file, file-tree, history, home, logs, project-tree, session-dock, transcript, workspace | `<C-d>` `<PageDown>` |
| `scroll half-page-up` | changes, file, file-tree, history, home, logs, project-tree, session-dock, transcript, workspace | `<C-u>` `<PageUp>` |
| `scroll up` | file, logs, transcript | `<Up>` `k` |
| `session kill` | home, workspace | `dd` |
| `session load-more` | history | `m` |
| `session resume` | history, transcript | `R` |
| `session spawn` | file, home, project-tree, workspace | `s` |
| `sidebar narrower` | changes, file, file-tree, project-tree, workspace | `<C-w><lt>` |
| `sidebar wider` | changes, file, file-tree, project-tree, workspace | `<C-w>>` |
| `tab next` | changes, file-tree, history, home, logs, session-dock | `<Right>` `gt` |
| `tab prev` | changes, file-tree, history, home, logs, session-dock | `<Left>` `gT` |
| `toggle diff-vs-target` | changes | `t` |
| `toggle left-sidebar` | changes, file, file-tree, history, home, logs, project-tree, transcript, workspace | `<Leader>o` |
| `toggle line-wrap` | file | `yow` |
| `toggle right-sidebar` | changes, file, file-tree, history, home, logs, project-tree, transcript, workspace | `<Leader>e` |
| `toggle show-gone` | file, project-tree, workspace | `zg` |
| `toggle show-hidden` | file, project-tree, workspace | `z.` |
| `transcript export` | history, transcript | `E` |
| `workspace change-target` | file, project-tree, workspace | `T` |
| `workspace force-remove` | file, project-tree, workspace | `D` |
| `workspace new` | file, project-tree, workspace | `a` |
| `workspace pick-target` | project-tree | `<C-t>` |
| `workspace remove` | project-tree | `dd` |
| `workspace rerun-setup` | file, project-tree, workspace | `S` |
<!-- keymap-commands:end -->

## End-to-End Encryption

`e2ee.enabled` (default `false`) turns on the blind-relay encrypted transport,
and `lock.genesis` pins the install to a trust log for locked mode. See
[End-to-End Encryption](/guide/e2ee) for setup and the two modes.

## Desktop notifications

`push.desktop.enabled` (default `false`) opts this node into native **macOS**
desktop notifications: when a session starts waiting on you (permission prompt,
question, plan, or a finished turn), this machine pops a banner, and clicking it
focuses that session's tmux pane. Other platforms are a no-op.

It is config-file / env only — there is no command-line flag:

```yaml
push:
  desktop:
    enabled: true
```

or `ARGUS_PUSH_DESKTOP_ENABLED=true`.

### Renderers

Argus renders through whichever of three backends it finds, in this order — and
the experience differs a lot between them, so installing the preferred one is
worth it:

1. **[`alerter`](https://github.com/vjeantet/alerter) — preferred, best
   experience.** A self-contained binary; nothing to configure. You get a
   clickable banner branded with the Argus icon, and repeat alerts for the same
   session replace the previous one instead of stacking. Install it on `PATH`:

   ```sh
   brew install vjeantet/tap/alerter
   ```

2. **[Hammerspoon](https://www.hammerspoon.org/) — clickable, extra setup.**
   Used only if `alerter` is absent. Requires both the `hs` CLI on `PATH` **and
   the IPC module enabled** — add `require("hs.ipc")` to your
   `~/.hammerspoon/init.lua` and reload the config. Without IPC loaded, `hs -c`
   fails (exit 69, "can't access Hammerspoon message port") and argus falls back
   to the plain banner below.

3. **`osascript` — always available, not clickable.** The built-in fallback when
   neither of the above is usable. You still get a notification, but clicking it
   does nothing (no jump to the session).

So: **install `alerter` for the full click-to-focus experience.** Everything
degrades gracefully — a missing tool, a failed render, or a non-macOS host never
breaks anything, it just drops to the next best (or silently no-ops).

Enable it on each machine you sit in front of; leave it off on headless boxes.

## Mobile notifications

`push.mobile.delay` (default `0s`) sets a grace period before a mobile push
fires. With the default, mobile pushes are instant — the same moment in-app and
desktop notifications go out.

Set it to a non-zero duration to hold mobile pushes back:

```yaml
push:
  mobile:
    delay: 30s
```

When the delay elapses, the push fires only if the session is still awaiting
input or idle — so answering at your desk within the window keeps the phone
quiet. Desktop and in-app notifications are always instant.
