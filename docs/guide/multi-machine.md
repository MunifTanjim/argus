# Multi Machine

To run Argus across several machines, run a **gateway** on one machine. The node
of each machine connects to it, and your TUI or the mobile app connects to the
gateway as a single endpoint.

```sh
# gateway (always-on box)
argus start --token <TOKEN>
# connected node (each dev box)
argus start --gateway wss://gateway.argus --token <TOKEN>
# TUI (anywhere)
argus --gateway wss://gateway.argus --token <TOKEN>
```

## Roles

`argus start` picks its role from its flags:

| `--gateway` | `--token` | role                                                          |
| ----------- | --------- | ------------------------------------------------------------- |
| unset       | set       | **gateway**: serves clients, and runs a node for this machine. |
| set         | set       | **connected node**: dials the gateway, does not listen.        |
| unset       | unset     | **local node**: unix socket only.                              |

To run a gateway without a node, for example on a server with no agents, add
`--mode gateway`.

`--token` is the shared secret. The gateway requires it, and nodes and clients
present it. The gateway serves plain `ws://`. For a public or encrypted endpoint,
put it behind a [tunnel](#tunnel), an [`ssh://`](#ssh-access) uplink, or a reverse
proxy (`wss://`).

## Exposing the gateway

The gateway listens on `:8443` by default (override with `--listen-addr`). To reach
it from another network or from the mobile app, use one of the options below. Every
gateway requires a `--token`, however you expose it, so an exposed gateway is never
open.

### Tunnel

Argus can manage a tunnel that routes a public URL to the gateway. This is the
easiest way to reach the gateway from the mobile app:

```sh
argus start --tunnel cloudflare:quick --token <TOKEN>
```

Argus logs the public URL when the tunnel is up. See
[Gateway Tunnel](/guide/gateway-tunnel) for stable hostnames and the other
providers: ngrok, zrok, and a tunnel that you manage.

### SSH access

Use SSH instead of exposing a port. The gateway binds to loopback, SSH protects the
transport, and the token still controls access.

```sh
# gateway, loopback only
argus start --listen-addr 127.0.0.1:8443 --token <TOKEN>
# node over SSH
argus start --gateway ssh://gateway.argus --token <TOKEN>
# TUI over SSH
argus --gateway ssh://gateway.argus --token <TOKEN>
```

You can use an `ssh://` URL anywhere a gateway URL is accepted. The connected node,
the TUI, and [`argus pair`](/guide/mobile-app#pairing) all support it. The format
is:

```
ssh://[user@]host[:ssh-port][?port=PORT]
```

- **`:ssh-port`**: the SSH port to dial (default `22`).
- **`?port=PORT`**: the gateway's loopback port on the remote host (default `8443`).

For example, `ssh://gateway.argus:2222?port=9000` connects over SSH on port `2222`
and reaches a gateway listening on `127.0.0.1:9000` on that host.

### Reverse proxy

Front the gateway yourself with TLS to serve `wss://`.

## Direct connections

By default, every client talks to every node through the gateway. A direct
connection skips the gateway for the traffic between a client and a node. The
gateway still provides the roster and the first connection, and Argus falls back
to it when a direct link drops.

Argus has two direct paths. A client prefers SSH if you configured it for the
node, then TCP over Tailscale, then the gateway.

### TCP over Tailscale

TCP direct runs only over [Tailscale](https://tailscale.com). The node checks
every incoming connection with the local Tailscale daemon and refuses a
connection that does not come from a device on your tailnet. If the daemon is
not running, the node refuses all direct connections. This applies in every
mode, with or without [E2EE](/guide/e2ee).

Without E2EE, a client that connects to a node directly presents its token to
that node, so the node sees the token. Nodes already hold the gateway token,
which grants more access than a client token.

On each node, set the address to listen on:

```yaml
# ~/.config/argus/config.yaml
p2p:
  listen: ["100.64.0.1:7800"]   # the node's Tailscale address and a free port
```

The node announces `ws://<address>/p2p` to the gateway. It announces only
addresses that are its own Tailscale addresses or its MagicDNS name. It drops
the others and logs a warning.

If the node cannot listen on an address, for example because Tailscale has not
assigned it yet at boot, the node logs a warning, keeps running, and tries
again in the background. The node works out the addresses to announce each
time it connects to the gateway, so an address that Tailscale did not have yet
at startup is announced after the next reconnect.

If Tailscale runs in userspace-networking mode, the node cannot listen on its
Tailscale address. Tailscale forwards incoming tailnet connections to the same
port on `127.0.0.1` by itself. Listen on loopback, and set `p2p.advertise` to
the node's tailnet address or MagicDNS name with that same port:

```yaml
p2p:
  listen: ["127.0.0.1:7800"]
  advertise: ["ws://node.tailnet.ts.net:7800/p2p"]
```

A machine that runs Tailscale in userspace-networking mode accepts direct
connections, but it cannot dial them. Direct dials ignore proxy settings, and
such a machine must reach the tailnet through the proxy of Tailscale. Its own
outgoing links use the gateway, or SSH if configured.

Clients need no setting. They read the announced URLs from the gateway and use
them when the node is on the tailnet of the client. To dial different URLs, set
`p2p.endpoints` on the client. A client that does not run Tailscale stays on
the gateway.

A client opens each node on the gateway first, then upgrades in the background,
and tries again every 60 seconds while the node is not on its best path. With
[E2EE](/guide/e2ee), the first connection of a client to a node always goes
through the gateway.

One-shot commands, such as `argus ping`, use SSH if it is configured for the
node, else the gateway. They do not dial TCP.

### SSH

SSH works without Tailscale. Map each node to an SSH destination on the client:

```yaml
p2p:
  ssh:
    work: ssh://dev@work.example.com:2222   # a node id or label
  ssh-command: argus bridge                 # the default
```

The client runs `ssh` and the command on the node's machine. The login must
work without a prompt, and the host key must already be known. Run `ssh` to the
host once by hand to accept it. The mobile app does not trust a host key that
it has not seen until you accept it.

A non-interactive SSH session can have a shorter `PATH`. If it cannot find
`argus`, set `p2p.ssh-command` to an absolute path, for example
`/usr/local/bin/argus bridge`.

An SSH login gives owner access to the node, the same as the local socket,
including the `lock.*` commands of [locked mode](/guide/e2ee#locked-mode). To
revoke the access of a client, remove its SSH keys as well as its token.

### Turn it off

Set `p2p.enabled: false` on a TUI or CLI client to use only the gateway. This
turns off TCP and SSH. The mobile app has no such setting. It uses SSH whenever
SSH destinations are configured for a node.

## Agents across nodes

Agents can orchestrate sessions on any connected node. An agent lists the
sessions of every online node, messages them, and kills them. A session ID
names the node, the agent, and the agent's own session id, like
`home:claude:5f1c0e2a-…`, so a call goes to the node that owns the session.

- `spawn_session` takes a `node` (an ID or a label). The working directory
  and the agent refer to that node. Without `node`, the session starts on the
  agent's own node. From the command line, use `argus session spawn --node <id|label>`.
- If a node is offline, `list_sessions` reports it as unreachable, and
  `kill_session` reports its sessions as not killed. `list_sessions` stops
  reporting a node after the gateway removes it from the roster. Replies and notices for an offline node wait in a
  queue. Argus sends them in order when the node returns.
- In [locked mode](/guide/e2ee#locked-mode), a node opens channels to another
  node only if the trust log authorizes its key. Authorize each node with
  `argus lock init` or `argus lock sign <node>`.
