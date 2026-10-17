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
