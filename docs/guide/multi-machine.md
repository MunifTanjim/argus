# Multi Machine

Run Argus across several machines: one node acts as a **gateway**, each machine's
node dials into it, and your TUI or phone connects to that one endpoint.

```sh
# gateway (always-on box)
argus start --token <TOKEN>
# connected node (each dev box)
argus start --gateway wss://gateway.argus --token <TOKEN>
# TUI (anywhere)
argus --gateway wss://gateway.argus --token <TOKEN>
```

## Roles

A node's role comes from its flags:

| `--gateway` | `--token` | role                                                    |
| ----------- | --------- | ------------------------------------------------------- |
| unset       | set       | **gateway node** — listens, aggregates, serves clients. |
| set         | set       | **connected node** — dials the gateway; doesn't listen. |
| unset       | unset     | **local node** — unix socket only.                      |

`--token` is the shared secret: the gateway requires it; nodes and clients present
it. The gateway serves plain `ws://` — for a public or encrypted endpoint, front it
with a [tunnel](#tunnel), an [`ssh://`](#ssh-access) uplink, or a reverse proxy
(`wss://`).

## Exposing the gateway

The gateway listens on `:8443` by default (override with `--listen-addr`). To reach
it from another network or your phone, pick one of the options below. Every gateway
requires a `--token` regardless of how it's exposed, so an exposed gateway is never
an open one.

### Tunnel

Let Argus manage a Cloudflare tunnel that routes a public URL back to the gateway —
the easiest way to reach it from your phone:

```sh
argus start --tunnel cloudflare:quick --token <TOKEN>
# prints: tunnel public URL: https://<random>.trycloudflare.com
```

See [Gateway Tunnel](/guide/gateway-tunnel) for stable hostnames and the other Cloudflare modes.

### SSH access

Tunnel over SSH instead of exposing a port — the gateway binds loopback, SSH
protects the transport, and the token still gates access.

```sh
# gateway, loopback only
argus start --listen-addr 127.0.0.1:8443 --token <TOKEN>
# node over SSH
argus start --gateway ssh://gateway.argus --token <TOKEN>
# TUI over SSH
argus --gateway ssh://gateway.argus --token <TOKEN>
```

Give an `ssh://` URL anywhere a gateway URL is accepted — the connected node, the
TUI, and [`argus pair`](/guide/mobile-app#pairing) all support it. The format is:

```
ssh://[user@]host[:ssh-port][?port=PORT]
```

- **`:ssh-port`** — the SSH port to dial (default `22`).
- **`?port=PORT`** — the gateway's loopback port to reach on the remote host (default `8443`).

For example, `ssh://gateway.argus:2222?port=9000` connects over SSH on port `2222`
and reaches a gateway listening on `127.0.0.1:9000` on that host.

### Reverse proxy

Front the gateway yourself with TLS to serve `wss://`.
