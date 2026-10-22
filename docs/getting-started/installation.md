# Installation

Argus runs on **macOS** and **Linux**. Install the prebuilt binary with the
script below, or build from source.

## Prerequisites

You need these to *run* Argus, however you install it:

- [tmux](https://github.com/tmux/tmux): Argus uses it to spawn and resume sessions
  and to show the live screen of a session
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

## Upgrade

```sh
argus upgrade
```

This downloads the latest release, checks its SHA-256 checksum, and replaces the
binary. If Argus is running on this machine, the node and the gateway restart in
place, one after the other. The PID of `argus start` does not change, so a
`systemd` or `launchd` service keeps tracking it. If you installed shell
completion, the upgrade updates it too.

- `--no-restart` replaces the binary, but does not restart the running argus.
- `--no-download` restarts the running argus with the binary that is already on
  disk.
- `--beta` upgrades to the newest release, betas included. A beta build keeps
  getting betas until a stable release is newer.

Argus must be able to write to the directory that holds its binary.

In the TUI, `upgrade node` upgrades the selected node, or this machine, and
`upgrade gateway` upgrades the gateway.

## Connect Your Agents

Argus connects to your agents when it starts, so it can track each session's
status in real time. It sets up each supported agent that you ran at least once
and skips the others.

If an agent is not connected, run this to retry and see what went wrong:

```sh
argus hooks install
```

`argus hooks uninstall` removes the integrations until Argus starts again.

For Claude Code it installs the Argus plugin. Without it, Argus still lists your
Claude sessions with a status that it reads from the transcript, but the status
can lag. Argus also cannot answer permission requests, and it can send prompts
only to sessions in tmux.

To install the plugin, Argus runs the `claude` command, so `claude` must be on
the `PATH` of Argus. If Argus runs as a `systemd` or `launchd` service, set
`PATH` in the service, or run `argus hooks install` from a shell.

Claude Code loads the plugin when a session starts. After you install or
upgrade Argus, restart the Claude sessions that are running.

For Antigravity it installs hooks and changes only its own entries. The hooks
run `argus`, so it must be on Antigravity's `PATH`. If it is not, set
[`bin`](/getting-started/configuration#agent-hooks) to the full path of the
binary.

OpenCode needs no hooks.

Codex needs no hooks. Argus connects to Codex's background app-server daemon,
which `codex` starts automatically. Sessions started with `codex --no-daemon` are
not visible to Argus.

### Agent tools

Argus gives Claude Code, Codex, and OpenCode a set of MCP tools. With these
tools, an agent can start other agent sessions, send messages to them, read
their output, answer their prompts, and stop them. Antigravity does not get the
tools.

The node serves the tools on `127.0.0.1`, on the port that
[`mcp.port`](/getting-started/configuration#agent-tools) sets. Each request
must carry a token that Argus creates on first use. When Argus starts, it
writes the server address and the token into the config of each agent:

- Claude Code: the settings of the Argus plugin. Argus also adds an allow rule
  for the tools to the Claude Code `settings.json`.
- Codex: the `[mcp_servers.argus]` table in the Codex `config.toml`.
- OpenCode: the `argus` server and an allow rule for its tools in the global
  OpenCode config.

Any local user who can read the token can start agents as you. Before Argus
writes the token, it removes the group and other permissions from the Codex and
OpenCode config files.

Claude Code gets the tools when a session starts, and Codex gets them at once.
OpenCode gets them when its server starts again.
