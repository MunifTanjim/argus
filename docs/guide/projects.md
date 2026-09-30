# Projects & Workspaces

Argus groups sessions by project and workspace.

- A **project** is a git repository or a plain directory.
- A **workspace** is one git worktree of a project. A plain directory has one
  workspace.

The TUI and the mobile app show the sessions, files, and changes of each
workspace.

## Projects

You can rename, hide, pin, or forget a project. A pinned project shows at the
top. A hidden project does not show in the list. Forget removes a project from
the list. Its files stay.

```sh
argus project list
argus project pin <project>
argus project forget <project>
```

### Automatic Project Adoption

When a session starts, the node records the project and workspace of its
directory. `workspace.auto-adopt-dirs` limits this to a list of directories. The
default is `["~"]`, your home directory.

A directory is adopted if it is inside a listed directory, or is one. For a git
project, the project root must also be inside a listed directory. A leading `~`
is your home directory. Other entries must be absolute paths. An empty list
turns automatic adoption off. Workspaces that you create from argus are always
recorded.

```yaml
workspace:
  auto-adopt-dirs:
    - ~/Dev
    - ~/work
```

If you remove a directory from the list, the projects that argus already
recorded stay. Remove one with `argus project forget <project>`.

## Workspaces

A new workspace is a new git worktree. You can create it from one of these:

- a new branch
- a branch that exists
- a GitHub pull request
- a GitHub issue

```sh
argus workspace create <project> <name> --source new|branch|pr|issue
```

Each workspace has a target branch. The default is the default branch of the
repository. The changes view compares the workspace with its target branch.

```sh
argus workspace target <workspace> <branch>
```

### Worktree path and branch names

::: v-pre
Two templates set the names. Both use Go template syntax.

- `workspace.worktree-dir-template` sets the worktree path. The default is
  `.worktrees/{{.Branch.Slug}}`.
- `workspace.issue-branch-template` sets the branch name of a workspace from an
  issue. The default is `issue-{{.Issue.Number}}-{{.Issue.Slug}}`.

The worktree path template has these variables:

| Variable | Value |
|---|---|
| `.Repo.Name` | The name of the directory of the main worktree |
| `.Branch.Name` | The branch name. A workspace from a PR uses the branch `pr-<number>`. |
| `.Branch.Slug` | The branch name with each `/` replaced by `-` |

The issue branch template has these variables:

| Variable | Value |
|---|---|
| `.Issue.Number` | The issue number |
| `.Issue.Title` | The issue title |
| `.Issue.Slug` | The issue title in lowercase with dashes, cut to 40 characters |

A relative worktree path starts at the main worktree. If the branch name
contains `/`, `.Branch.Name` makes nested directories, and `.Branch.Slug` makes
one directory.

The node checks both templates when it starts. A template with an unknown
variable stops the start with an error that names the key.

```yaml
workspace:
  worktree-dir-template: .worktrees/{{.Branch.Slug}}
  issue-branch-template: issue-{{.Issue.Number}}-{{.Issue.Slug}}
```
:::

The default path is inside the repository. Add `.worktrees/` to `.gitignore` or
to `.git/info/exclude`. If you do not, git shows the worktrees as untracked files.

### PR and Issue Workspaces

Workspaces from a PR or an issue need these:

- The GitHub CLI (`gh`), logged in with `gh auth login`.
- An `origin` remote on `github.com`.

## Setup and Teardown

After Argus creates a workspace, setup runs in the background:

1. Argus copies the ignored files that `.worktreeinclude` names.
2. Argus runs the setup script of the project.

Setup stops after 15 minutes. If setup fails, the workspace stays. You can read
the output and run setup again:

```sh
argus workspace setup-log <workspace>
argus workspace setup <workspace>
```

Before Argus removes a workspace, the teardown script of the project runs.

### Copy Ignored Files

A new worktree does not have the ignored files of the main worktree, for
example `.env`. To copy them, put a `.worktreeinclude` file at the root of the
repository. It uses `.gitignore` syntax:

```gitignore
# .worktreeinclude
.env
config/*.local.json
.venv/
```

Argus copies each file from the main worktree that matches a pattern and that
git ignores. A directory pattern, for example `.venv/`, copies each file in it.
Symlinks are copied as symlinks, and file modes are kept. Tracked files are
never copied. If a file already exists in the new worktree, argus keeps it.

If a file does not copy, the script still runs, and setup fails. The setup log
names each file that did not copy. Run setup again to copy the missing files.

### Scripts

Put the scripts in the repository:

```toml
# .argus/settings.toml (commit it)
[scripts]
setup = '''
set -e
pnpm install
ln -s "$ARGUS_ROOT_PATH/.env" .env
'''
teardown = "docker compose down --volumes"
```

A multiline script runs as one shell script. The exit status of the last
command is the result. Put `set -e` on the first line to stop at the first
command that fails.

`.argus/settings.local.toml` holds personal changes. Add it to `.gitignore`.
A key in the local file replaces the same key in the shared file. An empty
value (`setup = ""`) turns a script off. Argus reads both files from the
project's main worktree each time it runs a script.

If Argus cannot read a settings file, setup fails and shows the parse error. A
remove also stops, because Argus cannot find the teardown script. To remove the
workspace anyway, use `--force`.

Scripts run with `$SHELL -c` (or `/bin/sh`) in the workspace directory, with
these environment variables:

| Variable | Value |
|---|---|
| `ARGUS_WORKSPACE_PATH` | The workspace directory |
| `ARGUS_ROOT_PATH` | The main worktree |
| `ARGUS_WORKSPACE_NAME` | The base name of the workspace directory |
| `ARGUS_TARGET_BRANCH` | The target branch |

The shell is not interactive. It does not read rc files such as `.zshrc` or
`.bashrc`, and `PATH` starts as the `PATH` of the argus node. The shell still
reads the files that every shell reads, for example `~/.zshenv` for zsh and
the file in `$BASH_ENV` for bash. When the node runs as a service, the script
does not find tools that an rc file adds to `PATH` (for example, nvm or pnpm).
Use full paths, or set `PATH` in the script.

Make both scripts safe to run more than one time.

## Remove a Workspace

Remove deletes the worktree. The branch stays.

```sh
argus workspace remove <workspace>
```

- A workspace with live sessions cannot be removed. Kill its sessions first.
- If the workspace has uncommitted changes, the remove stops before teardown
  runs. To discard the changes, use `--force`.
- Teardown stops after 20 seconds. If it fails, the remove stops and the
  worktree stays. A forced remove continues and shows the failure as a warning.
