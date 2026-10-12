# Installation

Argus runs on **macOS** and **Linux**. Install the prebuilt binary with the
script below, or build from source.

## Prerequisites

You need these to *run* Argus, however you install it:

- [tmux](https://github.com/tmux/tmux): Argus discovers agent sessions running in tmux
- At least one supported AI coding agent for Argus to supervise:
  - [Claude Code](https://www.claude.com/product/claude-code) 2.1.293 or later: run as `claude`
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


## Connect Your Agents

Connect Argus to your agents so it can track each session's status in real time:

```sh
argus hooks install
```

This sets up every supported agent you have installed and skips the others. It
is safe to re-run.

For Claude Code it installs the Argus plugin. Without it, Argus still lists your
Claude sessions but cannot show their live status or answer their permission
requests. It can send prompts only to sessions in tmux.

Claude Code loads the plugin when a session starts. After you install or
upgrade Argus, restart the Claude sessions that are running.

For Antigravity it installs hooks and changes only its own entries.

OpenCode needs no hooks.

Codex needs no hooks. Argus connects to Codex's background app-server daemon,
which `codex` starts automatically. Sessions started with `codex --no-daemon` are
not visible to Argus.
