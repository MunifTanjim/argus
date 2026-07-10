# Installation

Argus runs on **macOS** and **Linux**. Install the prebuilt binary with the
script below, or build from source.

## Prerequisites

These are needed to *run* Argus, however you install it:

- [tmux](https://github.com/tmux/tmux) — Argus discovers agent sessions running in tmux
- At least one supported AI coding agent for Argus to supervise:
  - [Claude Code](https://www.claude.com/product/claude-code) — run as `claude`
  - [Codex](https://developers.openai.com/codex/cli) — the OpenAI Codex CLI, run as `codex`
  - [Antigravity](https://antigravity.google/) — Google's terminal agent, run as `agy`

Argus watches whichever of these you have installed — you don't need all three.

## Install Pre-built Binary

The script downloads the right binary for your platform from the latest
[GitHub release](https://github.com/MunifTanjim/argus/releases). It needs only
`curl` (or `wget`), and uses the [GitHub CLI](https://cli.github.com/) (`gh`)
instead when it's installed:

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

Install Argus's hooks so it can track each session's status live:

```sh
argus hooks install
```

This installs hooks for every supported agent you've set up — Claude Code, Codex,
and Antigravity — and skips any agent that isn't installed. It's safe to re-run and
only touches its own entries. Without it, status still works but is less precise.
