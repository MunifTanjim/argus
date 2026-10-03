# Gateway Tunnel

Argus can manage a tunnel that routes a public URL to the
[gateway](/guide/multi-machine). This is the easiest way to reach the gateway from
the mobile app. A reverse proxy also works, and both the CLI and the app can reach the
gateway over [SSH](/guide/multi-machine#ssh-access).

## Cloudflare

Provider: **Cloudflare Tunnel** (requires
[`cloudflared`](https://github.com/cloudflare/cloudflared) on `PATH`). Pick a mode
with `--tunnel`. Plain `--tunnel cloudflare` infers the mode from the
`--cloudflare-*` flags. The tunnel edge terminates TLS. If the tunnel dies, Argus
retries with backoff and keeps serving on your LAN.

With any provider, if a tunnel comes up but reports no public URL within a minute,
Argus exits. This prevents it from handing out pairing QRs that encode the LAN
address.

### Quick

An ephemeral URL that changes on each run. Use it for a quick pairing test.

```sh
argus start --tunnel cloudflare:quick --token <TOKEN>
```

### Remote

A stable hostname for a tunnel you configured in the Cloudflare dashboard, run with
its token:

```sh
argus start --tunnel cloudflare:remote --cloudflare-token <CLOUDFLARE_TOKEN> --token <TOKEN>
```

- Argus reads the public hostname from the tunnel's **remotely-managed ingress**,
  which `cloudflared` reports once it connects. You don't pass it on the command
  line. If the tunnel routes several public hostnames, Argus uses the first one.
  It skips wildcard (`*.example.com`) and path-scoped rules, because neither gives a URL
  that pairing can reach.
- In the Cloudflare dashboard, point the tunnel's public hostname at the gateway's
  listen address (`http://localhost:8443` by default).
- Reading the ingress requires `cloudflared --loglevel info`, so this mode runs
  `cloudflared` with more verbose logging than the other modes. Argus classifies the
  extra lines as debug, so they appear only with `--log-level debug`.

### Local

A stable hostname where Argus creates and owns the tunnel and DNS record:

```sh
argus start --tunnel cloudflare:local --cloudflare-hostname argus.example.com --token <TOKEN>
```

- It needs a Cloudflare **origin certificate**. If the certificate is missing and
  you are at a terminal, Argus runs `cloudflared tunnel login`. Otherwise run it
  yourself (it writes `~/.cloudflared/cert.pem`).
- The tunnel is named `argus`. Override the name with `--cloudflare-tunnel-name`.
- The hostname must be in a zone in the **same Cloudflare account** as the
  certificate.

## ngrok

Provider: **[ngrok](https://ngrok.com)** (requires `ngrok` on `PATH` and an ngrok
account). Argus runs a public HTTP tunnel to the gateway. By default it uses your
account's free **static dev domain**, a stable URL, so pairing survives restarts:

```sh
argus start --token <TOKEN> --tunnel ngrok
```

- `--ngrok-domain <domain>` binds a **reserved/custom domain** instead. Reserve the
  domain in the ngrok dashboard first. The agent doesn't provision it.

  ```sh
  argus start --token <TOKEN> --tunnel ngrok --ngrok-domain argus.example.com
  ```

- ngrok needs an **authtoken**. Set `NGROK_AUTHTOKEN`. If it is missing and you are
  at a terminal, Argus prompts for one and stores it with `ngrok config add-authtoken`.
  Otherwise run that command yourself first.

## Zrok

Provider: **[zrok](https://zrok.io)** v2 (requires `zrok2` on `PATH` and a zrok
account, either the hosted service or a self-hosted instance). Argus runs a public
share at a stable URL backed by a reserved **name**:

```sh
argus start --token <TOKEN> --tunnel zrok --zrok-name argus
```

- `--zrok-name` is the reserved name (`name`, or `namespace:name`). The namespace
  defaults to `public`, for example `https://myapp.shares.zrok.io`. The name
  defaults to `argus` (`https://argus.shares.zrok.io`). Argus creates the name if it
  doesn't exist.
- The environment must be **enabled** (`zrok2 enable`). If it isn't and you are at a
  terminal, Argus prompts for your zrok account token and enables it. Otherwise run
  `zrok2 enable <token>` yourself first.
- For a **self-hosted** instance, point the CLI at it with `ZROK2_API_ENDPOINT` (or
  `zrok2 config set apiEndpoint …`). Argus's child `zrok2` process inherits the
  environment.

## External

Provider: **external**. You manage the tunnel yourself (a reverse proxy, ingress, or
`ssh -R`). It must already terminate TLS and forward to the local gateway. Argus
runs no process. It only records the public URL so pairing QRs point at the right
host:

```sh
argus start --token <TOKEN> --tunnel external --external-url wss://argus.example.com
```

- `--external-url` is required. It is the gateway's public URL,
  `scheme://host[/base-path]` with scheme `ws`, `wss`, `http`, or `https` (also
  `$ARGUS_EXTERNAL_URL`). It cannot contain a query, fragment, or user info. Argus
  copies it into pairing QRs.

::: tip
A tunnel cannot publish an open gateway: every gateway requires a `--token`,
however you expose it. See [Multi Machine](/guide/multi-machine) for the other ways
to reach a gateway ([SSH](/guide/multi-machine#ssh-access) or a reverse proxy).
:::
