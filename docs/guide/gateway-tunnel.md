# Gateway Tunnel

Let Argus manage a tunnel that routes a public URL back to the
[gateway](/guide/multi-machine) — the easiest way to reach it from your phone (a
reverse proxy works too, and both the CLI and the app can reach it over
[SSH](/guide/multi-machine#ssh-access)).

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

## ngrok

Provider: **[ngrok](https://ngrok.com)** (requires `ngrok` on `PATH` and an ngrok
account). Argus runs a public HTTP tunnel to the gateway. By default it uses your
account's free **static dev domain** — a stable URL, so pairing survives restarts:

```sh
argus start --token <TOKEN> --tunnel ngrok
```

- `--ngrok-domain <domain>` binds a **reserved/custom domain** instead. Reserve it in
  the ngrok dashboard first (the agent doesn't provision it):

  ```sh
  argus start --token <TOKEN> --tunnel ngrok --ngrok-domain argus.example.com
  ```

- ngrok needs an **authtoken**. Set `NGROK_AUTHTOKEN`, or — if it's missing and you're
  at a terminal — Argus prompts for one and stores it via `ngrok config add-authtoken`;
  otherwise run that yourself first.

## Zrok

Provider: **[zrok](https://zrok.io)** v2 (requires `zrok2` on `PATH` and a zrok
account — the hosted service or a self-hosted instance). Argus runs a public share at
a stable URL backed by a reserved **name**:

```sh
argus start --token <TOKEN> --tunnel zrok --zrok-name argus
```

- `--zrok-name` is the reserved name (`name`, or `namespace:name`; the namespace
  defaults to `public`, i.e. `https://myapp.shares.zrok.io`). Defaults to `argus`
  (→ `https://argus.shares.zrok.io`) when unset. Argus creates the name if it doesn't
  exist.
- The environment must be **enabled** (`zrok2 enable`). If it isn't and you're at a
  terminal, Argus prompts for your zrok account token and enables it for you; otherwise
  run `zrok2 enable <token>` yourself first.
- For a **self-hosted** instance, point the CLI at it with `ZROK2_API_ENDPOINT` (or
  `zrok2 config set apiEndpoint …`); Argus's child `zrok2` inherits the environment.

## External

Provider: **external** — a tunnel you manage yourself (a reverse proxy, ingress, or
`ssh -R`) that already terminates TLS and forwards to the local gateway. Argus runs no
process; it only records the public URL so pairing QRs point at the right host:

```sh
argus start --token <TOKEN> --tunnel external --external-url wss://argus.example.com
```

- `--external-url` is required — the gateway's public URL, `scheme://host[/base-path]`
  with scheme `ws`, `wss`, `http`, or `https` (also `$ARGUS_EXTERNAL_URL`). No query,
  fragment, or user info; it's echoed into pairing QRs.

::: tip
A tunnel can't accidentally publish an open gateway — every gateway requires a
`--token` regardless of how it's exposed. See [Multi Machine](/guide/multi-machine)
for the other ways to reach a gateway ([SSH](/guide/multi-machine#ssh-access) or a
reverse proxy).
:::
