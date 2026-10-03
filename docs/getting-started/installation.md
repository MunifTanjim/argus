# Installation

Argus runs on **macOS** and **Linux**. Install the prebuilt binary with the
script below, or build from source.

## Prerequisites

You need these to *run* Argus, however you install it:

- [tmux](https://github.com/tmux/tmux): Argus discovers agent sessions running in tmux
- At least one supported AI coding agent for Argus to supervise:
  - [Claude Code](https://www.claude.com/product/claude-code): run as `claude`
  - [Codex](https://developers.openai.com/codex/cli): the OpenAI Codex CLI, run as `codex`
  - [Antigravity](https://antigravity.google/): Google's terminal agent, run as `agy`
  - [OpenCode](https://opencode.ai/) 2 or later: run as `opencode`

Argus watches the agents you have installed. You don't need all four.

## Install Pre-built Binary

The script downloads the right binary for your platform from the latest
[GitHub release](https://github.com/MunifTanjim/argus/releases). It needs only
`curl` (or `wget`). If the [GitHub CLI](https://cli.github.com/) (`gh`) is
installed, the script uses it instead:

```sh
curl -fsSL https://argus.muniftanjim.dev/install.sh | bash
```

This installs the binary in `~/.local/bin/argus`. To install elsewhere, set `INSTALL_DIR`:

```sh
curl -fsSL https://argus.muniftanjim.dev/install.sh | INSTALL_DIR=/usr/local/bin bash
```

Make sure the install directory is on your `PATH`.

## Compile from Source

Requires [Go](https://go.dev/) 1.26 or later.

```sh
go install github.com/MunifTanjim/argus/cmd/argus@latest   # -> $(go env GOPATH)/bin
```

Or clone and install with `make`:

```sh
git clone https://github.com/MunifTanjim/argus
cd argus

make install                          
```


## Install Hooks

Install the Argus hooks so it can track each session's status in real time:

```sh
argus hooks install
```

This installs hooks for every supported agent you have installed (Claude Code
and Antigravity) and skips the others. It is safe to re-run and changes
only its own entries. Without the hooks, status still works but is less precise.

OpenCode needs no hooks.

Codex needs no hooks. Argus connects to Codex's background app-server daemon,
which `codex` starts automatically. Sessions started with `codex --no-daemon` are
not visible to Argus.
