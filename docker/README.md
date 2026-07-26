# Argus blind-gateway E2E — Docker Compose test harness

Spins up the full topology of the `feat/e2e-encryption-blind-gateway` branch so
you can exercise it yourself:

- **gateway** — the blind relay on `:8443`. It moves opaque, per-message
  Noise-IK-encrypted bodies between clients and nodes and publishes only a
  cleartext node roster. It cannot read session content or forge commands.
- **node-a / node-b / node-c** — three real nodes, each running a real Claude
  Code agent inside tmux, connected to the gateway over `ws://`.
- **client** — the interactive TUI. It reads the roster, opens one sealed channel
  per node *through* the gateway, and aggregates sessions client-side.

All four run the **same image** (`docker/Dockerfile`) — the role is decided
entirely by the command in `docker-compose.yml`.

Every client↔gateway connection is end-to-end encrypted **by construction**: the
gateway is a blind relay that serves no session data in cleartext, so a client always
opens sealed Noise channels to the nodes through it. There is no `--e2e` flag — the
transport is decided by destination (a gateway URL is always sealed; only a direct
local unix-socket connection to your own node is plaintext). Just bringing the stack
up exercises the encrypted path.

All commands below assume you run them from the repo root.

## Prerequisites

- Docker with Compose v2.
- An Anthropic account to log Claude Code in (interactive, one-time per node —
  argus never sees your credentials).

## 1. Open mode (blind gateway) — the default

```sh
cp docker/.env.example docker/.env
docker compose -f docker/docker-compose.yml up -d --build
docker compose -f docker/docker-compose.yml logs -f gateway    # ^C when you see nodes connect
```

Every node auto-generates its identity/signer keys on first boot and joins the
roster. Confirm all three are up:

```sh
docker compose -f docker/docker-compose.yml ps
```

### Log Claude in on each node (one-time)

Claude Code authenticates itself; credentials persist in each node's volume.

```sh
docker compose -f docker/docker-compose.yml exec -it node-a claude
# complete /login (it prints a URL — open it in YOUR browser, paste the code back),
# then quit claude. Repeat for node-b and node-c.
```

### Start a real, discoverable agent session on each node

Discovery scans tmux, so start `claude` inside a tmux pane (detached):

```sh
for n in node-a node-b node-c; do
  docker compose -f docker/docker-compose.yml exec $n \
    tmux new -d -s work -c /home/argus claude
done
```

### Open the TUI client and watch it aggregate

```sh
docker compose -f docker/docker-compose.yml run --rm client
```

You should see **all three nodes** in the roster, each with its Claude session —
proof of client-side aggregation over the blind relay. The TUI also joins the fleet
as its own ephemeral node (labelled `client`, from `--id=client`); it has no tmux
`claude` running, so it shows up empty — that is expected. Meanwhile the gateway logs
show only opaque relay + roster events, never session content:

```sh
docker compose -f docker/docker-compose.yml logs gateway
```

Quick connectivity/auth probe (answered by the gateway itself):

```sh
docker compose -f docker/docker-compose.yml exec node-a \
  argus ping --gateway ws://gateway:8443 --token devtoken
```

> **Note:** discovery has no periodic ticker — a newly started agent shows up
> after the TUI refreshes (reopen the client) or after a spawn. Expected.

## 2. Locked mode (trust ledger)

Locked mode makes device authorization unforgeable even against an actively
malicious gateway, via a signer-signed, hash-chained trust log.

**Init on a signer node** (node-a reads the roster via `ARGUS_GATEWAY_URL`; add
node-b and node-c as co-signers so recovery is possible):

```sh
docker compose -f docker/docker-compose.yml exec node-a \
  argus lock init --signer node-b --signer node-c --gen-disablements 1
```

Save the printed **`lock.genesis: <B64>`** and the **disablement secret** (shown
once — it's your break-glass recovery key).

`lock init` pins both roles inside the node-a container (its node and its client).
Every other device is unpinned and will quarantine.

**Pin every other node to the genesis.** Each node that was not present at `lock init`
must be pinned interactively. The command pulls the offered genesis, shows its word
fingerprint, and asks for confirmation — compare those words against `argus lock status`
on node-a before typing `y`:

```sh
docker compose -f docker/docker-compose.yml exec node-b argus lock pin
docker compose -f docker/docker-compose.yml exec node-c argus lock pin
```

**Pin the TUI client too.** The `client` service is a separate device with its own
pin; unpinned, it quarantines and shows an empty dashboard. Its home volume persists,
so pin it once:

```sh
docker compose -f docker/docker-compose.yml run --rm client \
  lock pin --gateway ws://gateway:8443 --token "${ARGUS_TOKEN:-devtoken}"
docker compose -f docker/docker-compose.yml run --rm client \
  lock status --gateway ws://gateway:8443 --token "${ARGUS_TOKEN:-devtoken}"  # client pin: <words>
```

`docker compose run` replaces the service's `command:`, which is where the client's
`--gateway`/`--token` normally live — hence the explicit flags. The `client` service
also sets `ARGUS_GATEWAY_URL`/`ARGUS_TOKEN`, so they can be omitted on this harness;
they are spelled out here because that is what the commands actually need. The client
container runs no node, so `lock pin` also notes that the local node is unreachable and
pins the client role only — expected here.

**Declarative alternative:** you can instead set `ARGUS_LOCK_GENESIS=<B64>` in
`docker/.env` and uncomment the env var in `docker/docker-compose.yml` (both
`x-node-base` and the `client` service). The env var takes precedence over the
pinned file, which is useful for fleet-wide deployment where you want a single
authoritative genesis wired into your compose config. After editing, restart:

```sh
docker compose -f docker/docker-compose.yml up -d
```

**Authorize the TUI client.** The client's identity is separate from the node
identities, so `lock init` did not authorize it. Get its pubkey and sign it:

```sh
docker compose -f docker/docker-compose.yml run --rm client lock status
# prints this device's identity pubkey + the exact `argus lock sign <pubkey>` command
docker compose -f docker/docker-compose.yml exec node-a argus lock sign <pubkey>
```

**Verify:**

```sh
docker compose -f docker/docker-compose.yml exec node-a argus lock status   # enabled, signers: 3
docker compose -f docker/docker-compose.yml exec node-a argus lock log      # genesis + authorize-device entries
docker compose -f docker/docker-compose.yml run --rm client                 # connects, lists sessions
```

## 3. Rejection demo (unauthorized device)

This is the crown-jewel proof. Before signing the client (step 2), a client that
has the pinned genesis but an **unsigned identity** connects to the gateway, but
every node **drops the Noise handshake** (fail-closed —
`internal/node/responder.go`). The client opens **zero** channels and sees no
nodes/sessions.

To reproduce cleanly, reset the client identity so it's unauthorized, then watch
it get rejected and then accepted:

```sh
# fresh, unauthorized client identity
docker compose -f docker/docker-compose.yml run --rm client lock status
#   -> "locked mode enabled, this node authorized: false" + a sign hint
docker compose -f docker/docker-compose.yml run --rm client
#   -> roster is visible but NO node channels open (rejected)

# authorize it, wait ~30s for the chain to sync, then it works
docker compose -f docker/docker-compose.yml exec node-a argus lock sign <pubkey>
docker compose -f docker/docker-compose.yml run --rm client
#   -> now connects and lists sessions
```

Show **live revocation** kicking an authorized device off:

```sh
docker compose -f docker/docker-compose.yml exec node-a argus lock revoke-device <pubkey>
# the client's open channels are torn down within a sync tick
```

Break-glass (disable locked mode network-wide with a disablement secret):

```sh
docker compose -f docker/docker-compose.yml exec node-a argus lock disable <secret>
```

## Teardown

```sh
docker compose -f docker/docker-compose.yml down                            # keep volumes (identities persist)
docker compose -f docker/docker-compose.yml --profile client down -v        # wipe everything
```

`--profile client` matters on the wipe: `client` is a profile service, and a
plain `down -v` leaves `client-home` behind.

## Notes & troubleshooting

- **Claude Code on Alpine (musl):** the image installs `libc6-compat` as a shim.
  If `claude` still fails to launch, switch the runtime stage in
  `docker/Dockerfile` from `alpine:3` to `node:22-slim` (and swap `apk add ...`
  for `apt-get install -y ca-certificates tini tmux procps git`). The argus
  binary is static and runs on either.
- **Port 8443 already in use?** (e.g. you already run argus on the host) — set
  `ARGUS_GATEWAY_PORT` to a free host port in `docker/.env`; the container side
  stays 8443, so the internal `ws://gateway:8443` used by nodes/clients is
  unaffected.
- **TUI needs a TTY** — always launch the client with `docker compose run` (which
  allocates one), not `up`.
- **The gateway serves plain `ws://`** — TLS is meant to be terminated by a
  tunnel or reverse proxy in production; skipped here for local testing.
- **Sessions empty?** Make sure `claude` is actually running in a tmux pane on the
  node (`docker compose exec node-a tmux ls`) and reopen the client to trigger a
  rescan.
- **`permission denied` reading state** (e.g. `persisted genesis unusable`) — a
  volume left over from an image whose container user had a different uid. The
  home volumes hold uid-owned `0600` state, so they are not portable across a
  user change. Wipe them: `--profile client down -v`.
