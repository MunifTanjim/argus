# Gateway Tunnel

Let Argus manage a tunnel that routes a public URL back to the
[gateway](/guide/multi-machine) — the easiest way to reach it from your phone (a
reverse proxy works too; SSH is CLI-only).

## Cloudflare

Provider: **Cloudflare Tunnel** (requires
[`cloudflared`](https://github.com/cloudflare/cloudflared) on `PATH`). Pick a mode
with `--tunnel`; plain `--tunnel cloudflare` infers it from the `--cloudflare-*`
flags. The tunnel edge terminates TLS; if it dies, Argus retries with backoff and
keeps serving on your LAN.

### Quick

An ephemeral URL that changes on each run — fine for a quick pairing test.

```sh
argus start --tunnel cloudflare:quick --token <TOKEN>
```

### Remote

A stable hostname for a tunnel you configured in the Cloudflare dashboard, run via
its token:

```sh
argus start --tunnel cloudflare:remote --cloudflare-token <CLOUDFLARE_TOKEN> --token <TOKEN>
```

### Local

A stable hostname where Argus creates and owns the tunnel + DNS:

```sh
argus start --tunnel cloudflare:local --cloudflare-hostname argus.example.com --token <TOKEN>
```

- It needs a Cloudflare **origin certificate**. If it's missing and you're at a
  terminal, Argus runs `cloudflared tunnel login` for you; otherwise run it
  yourself (it writes `~/.cloudflared/cert.pem`).
- The tunnel is named `argus`, you can override with `--cloudflare-tunnel-name` flag.
- The hostname must be in a zone in the **same Cloudflare account** as the cert.

::: tip
A tunnel can't accidentally publish an open gateway — every gateway requires a
`--token` regardless of how it's exposed. See [Multi Machine](/guide/multi-machine)
for the other ways to reach a gateway ([SSH](/guide/multi-machine#ssh-access) or a
reverse proxy).
:::
