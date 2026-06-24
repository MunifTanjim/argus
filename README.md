# argus

**Watch and control all your AI coding sessions — Claude Code first — from one place.**

Run more than one AI coding agent and they scatter across tmux panes and
machines. argus pulls them into one view: which are working, which are stuck on a
prompt, which just finished. Read transcripts, watch and type into a session,
answer prompts, spawn/interrupt/kill — without leaving the dashboard.

Start local in a terminal. Scale to a fleet across machines. Get a push
notification on your phone when a session needs you.

## Highlights

- **Zero-setup discovery** — finds Claude Code sessions in tmux. No per-session config.
- **Live status** — working / waiting / idle / dead, from Claude Code hooks.
- **Transcripts** — full conversation, foldable, drill into tool calls.
- **Live screen** — watch a session's terminal and type into it.
- **Lifecycle control** — spawn, interrupt, kill, answer prompts in place.
- **Fleet mode** — aggregate machines, watch them all from one TUI.
- **Mobile app** — Android companion with **push notifications**, even when closed.

## Quick start

Supervise Claude Code on your own machine. macOS or Linux, with **Go 1.26+**,
**tmux**, and **Claude Code**.

```sh
go install github.com/MunifTanjim/argus/cmd/argus@latest   # install
argus hooks install                                        # add hooks (live status)

tmux new -s work                                           # start a tmux session
claude                                                     # …then run claude inside it
argus                                                      # open the dashboard (another terminal)
```

Your session shows up — open it to read the transcript or watch its screen live.
That's the loop; everything below is there when you need it.

## Installation

From source with Go (no prebuilt binaries yet).

```sh
go install github.com/MunifTanjim/argus/cmd/argus@latest   # -> $(go env GOPATH)/bin
```

<details>
<summary>Other ways (make install / build)</summary>

```sh
git clone https://github.com/MunifTanjim/argus && cd argus
make install                          # -> ~/.local/bin/argus
make install PREFIX=/usr/local        # -> /usr/local/bin/argus
make uninstall                        # remove (respects PREFIX/BINDIR)

make build                            # -> bin/argus (build only)
```

</details>

## Hooks (live status)

Hooks give argus authoritative status and reliable pane↔session correlation.
Without them, status stays coarse.

```sh
argus hooks install      # idempotent, non-destructive; only touches #argus-managed hooks
argus hooks uninstall    # remove (your other settings stay intact)
```

Writes command hooks into your Claude Code settings
(`$CLAUDE_CONFIG_DIR/settings.json`, default `~/.claude/settings.json`). Pin a
binary path with `--bin /path/to/argus` if argus isn't on the hooks' `PATH`.

## Using the dashboard

```sh
argus          # open the dashboard
argus start    # optional: always-on node, so hooks record status with no dashboard open
```

If no local node is running, `argus` first offers a choice: spawn a local server
(ephemeral — it dies when you quit) or connect to a gateway. Run `argus start`
for an always-on node instead, so hooks keep recording status even with no
dashboard open.

- **Dashboard** — the session list, with live status at a glance.
- **Transcript** — the full conversation, foldable, with tool-call detail.
- **Live screen** — watch a session's terminal and type into it.
- **History** — browse past projects and sessions.

If the connection drops, the dashboard shows `(reconnecting…)` and retries
automatically with backoff. Each view shows its keys in a footer.

## Mobile app

A companion **Android app** ([`app/`](app/)) mirrors the
dashboard — sessions, status, transcripts, live screen, prompts, history — and
adds **push notifications**: it pings you the moment a session needs you (a
prompt, a question, a finished turn) **even when backgrounded or killed**.

The app connects to a **gateway** (see [Fleet](#fleet-multiple-machines)), so
expose the gateway — a [tunnel](#expose-the-gateway-with-a-tunnel) is easiest —
then pair by scanning a QR:

```sh
argus pair --gateway wss://gw.example --token "$TOK"   # show QR; revoke with `argus unpair`
```

Push runs over **UnifiedPush**, so it works on any Android — Google Play devices
out of the box, de-Googled devices via any UnifiedPush distributor (e.g.
[ntfy](https://ntfy.sh)).

## Fleet (multiple machines)

Run one node as a **gateway**; each machine's node dials into it; your dashboard
or phone connects to that one endpoint.

```sh
argus start --token "$TOK"                              # gateway (always-on box)
argus start --gateway wss://gw.example --token "$TOK"   # connected node (each dev box)
argus --gateway wss://gw.example --token "$TOK"         # dashboard (anywhere)
```

A node's role comes from its flags:

| `--gateway` | `--token` | role                                                    |
| ----------- | --------- | ------------------------------------------------------- |
| set         | optional  | **connected node** — dials the gateway; doesn't listen. |
| unset       | set       | **gateway node** — listens, aggregates, serves clients. |
| unset       | unset     | **local node** — unix socket only.                      |

`--token` is the shared secret (gateway requires it; nodes/dashboards present
it). The gateway serves plain `ws://`; for a public/encrypted endpoint, front it
with a **tunnel**, an **`ssh://`** uplink, or a **reverse proxy** (`wss://`).

> **Footgun:** if `$ARGUS_TOKEN` is set and you run a plain `argus start` (no
> `--gateway`), argus starts a gateway listener. The startup banner makes this
> visible — a gateway prints `gateway listening on :8443`, a local node doesn't.

<details>
<summary>Reach the gateway over SSH</summary>

Tunnel over SSH instead of exposing a port — the gateway binds loopback, SSH
protects the transport, the token still gates it:

```sh
argus start --listen-addr 127.0.0.1:8443 --token "$TOK"        # gateway, loopback only
argus start --gateway ssh://me@gw.example --token "$TOK"        # node over SSH
argus --gateway ssh://me@gw.example --token "$TOK"              # dashboard over SSH
```

In an `ssh://` URL, `:port` is the SSH port; `?port=N` overrides the gateway's
loopback port (default 8443).

</details>

### Expose the gateway with a tunnel

Let argus manage a tunnel that routes a public URL back to the gateway — handy
for reaching it from your phone. Provider: **Cloudflare Tunnel** (`cloudflared`
on `PATH`). Pick a mode with `--tunnel cloudflare:quick` (ephemeral URL),
`cloudflare:remote` (token), or `cloudflare:local` (your hostname); plain
`cloudflare` infers it from the `--cloudflare-*` flags.

```sh
argus start --tunnel cloudflare:quick --token "$TOK"
# prints: tunnel public URL: https://<random>.trycloudflare.com
```

<details>
<summary>Remote and locally-managed tunnels</summary>

Remotely-managed (stable hostname, run with a Cloudflare token):

```sh
argus start --tunnel cloudflare:remote --cloudflare-token "$CF_TUNNEL_TOKEN" --token "$TOK"
```

Locally-managed (argus creates and owns the tunnel + DNS):

```sh
argus start --tunnel cloudflare:local --cloudflare-hostname argus.example.com --token "$TOK"
# prints: tunnel public URL: https://argus.example.com
```

argus creates the tunnel, routes a DNS record to it, and runs it. A local tunnel
needs a Cloudflare origin cert; if missing and you're at a terminal, argus runs
`cloudflared tunnel login` for you (otherwise run it yourself — writes
`~/.cloudflared/cert.pem`). The tunnel is named `argus` (override with
`--cloudflare-tunnel-name`) and is **persistent** — reused on the next start. The
hostname must be in a zone in the same Cloudflare account as the cert; for a
non-default cert path set `TUNNEL_ORIGIN_CERT`.

</details>

Every gateway requires a `--token` regardless of how it's exposed — the token is
what makes a node a gateway in the first place — so a tunnel can't accidentally
publish an open one. The tunnel edge terminates TLS; if it dies, argus retries
with backoff and keeps serving on your LAN.

## Configuration

The defaults work out of the box — no config needed to start.

When you do want to tweak things, every setting is available as a command-line
flag, an `ARGUS_*` environment variable, or a key in an optional YAML config file
at `$XDG_CONFIG_HOME/argus/config.yaml` (flag wins, then env var, then file).

Run `argus <command> --help` for the settings each command accepts.

## License

Licensed under the MIT License. Check the [LICENSE](./LICENSE) file for details.
