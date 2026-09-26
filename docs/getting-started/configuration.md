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

In the TUI, `n` creates a git worktree for a new workspace. Two templates set the
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
  remove (`X`, or `--force`) continues and shows the failure as a warning. If
  the workspace has uncommitted changes, a remove that is not forced stops
  before teardown runs.

Make both scripts safe to run more than one time.

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
