# Claude Code Remote Control — Session Ingress Protocol

Reverse-engineered from Claude Code 2.1.288 (native build). This is the wire
protocol between a headless Claude Code worker and the session-ingress server
that drives it. It is the protocol behind:

- `claude --remote-control [name]` (the local "bridge" host)
- `claude --sdk-url <url>` (the hidden worker flag)
- Claude Code on the web / mobile remote sessions (`cse_…` sessions)

Everything below was extracted from the embedded JS chunks in the native
binary. Symbol names are the minified originals.

## Architecture

```
┌──────────────┐  OAuth + REST   ┌─────────────────────┐
│ claude.ai    │◄───────────────►│ Anthropic backend   │
│ web/mobile   │                 │ (session fanout)    │
└──────────────┘                 └──────────┬──────────┘
                                            │ worker_jwt
                                            ▼
        ┌────────────────────────────────────────────────┐
        │  Session ingress  <api_base>/v1/code/sessions/* │
        │  SSE ↓ inbound · POST ↑ uploads                │
        └────────────────────────────────────────────────┘
              ▲                                ▲
              │ spawns + relays                │ SDK stream-json
              │                                │
        ┌─────────────┐                  ┌──────────────────┐
        │ bridge host │                  │ headless worker  │
        │ (--remote-  │                  │ claude --print   │
        │  control)   │                  │ --sdk-url …      │
        └─────────────┘                  └──────────────────┘
```

The **bridge** (`claude --remote-control`) is optional. Anything that can
spawn the worker and speak the ingress protocol can replace it — including a
self-hosted server.

## The hidden `--sdk-url` flag

```
claude --print \
  --sdk-url https://<api-base>/v1/code/sessions/<session-id> \
  --session-id <session-id> \
  --input-format stream-json --output-format stream-json \
  --replay-user-messages \
  --resume=https://<api-base>/v1/code/sessions/<session-id>
```

Hidden option (`.hideHelp()`), described in the parser as *"Use remote
WebSocket endpoint for SDK I/O streaming (only with -p and stream-json
format)"*.

Constraints (validated at startup, `yqt` / `SXo` in the binary):

- Requires `--input-format stream-json` **and** `--output-format stream-json`.
- Scheme must be `https:` or `wss:` (`wss:` is normalized to `https:`).
- **Hostname allowlist**:
  - `api.anthropic.com`
  - `api-staging.anthropic.com`
  - `beacon.claude-ai.staging.ant.dev`
  - `claude.fedstart.com`
  - `claude-staging.fedstart.com`

  Rejection: `Error: --sdk-url rejected: host "…" is not an approved
  Anthropic endpoint. This flag is reserved for Remote Control worker
  processes connecting to Anthropic's backend.`

  The check is on `hostname` only — the **port is not checked**. A
  self-hosted server therefore has to be reachable under one of the
  allowlisted hostnames (split-horizon DNS, `/etc/hosts`, or a container
  `--add-host`) with a locally-trusted TLS certificate (`NODE_EXTRA_CA_CERTS`
  works). Non-session-ingress traffic on that host must be reverse-proxied to
  the real API if the same machine still needs it.

## URL shapes

| Item | Value |
| ---- | ----- |
| Session URL | `https://<api-base>/v1/code/sessions/<session-id>` |
| Session id | `^[a-zA-Z0-9_-]+$` (created sessions use a `cse_` prefix; `session_` is also recognized) |
| Worker SSE | `<session-url>/worker/events/stream` |
| Upload/events | `<session-url>/worker/events` |
| Parse rule | `^(.*)/v1/code/sessions/([^/?#]+)$` → `{apiBaseUrl, sessionId}` (`parseBridgeSdkUrl`) |

## Authentication

Every worker request carries:

```
Authorization: Bearer <token>
anthropic-version: 2023-06-01
anthropic-client-platform: <client platform string>
User-Agent: <claude user agent>
Content-Type: application/json        (on POST/PUT)
```

The token comes from:

- the `worker_jwt` field of the `/bridge` attach response, or
- a work item secret (`session_ingress_token`), or
- the `CLAUDE_CODE_SESSION_ACCESS_TOKEN` env var (this is how the bridge
  passes it to the child worker).

Tokens that start with `sk-ant-sid` are instead sent as
`Cookie: sessionKey=<token>` plus optional `X-Organization-Uuid`.

## Worker lifecycle endpoints

All paths below are relative to the session URL.

### `POST /worker/register`

Registers the worker and claims the session lease.

- Body: `{}` (empty JSON)
- Response: `{"worker_epoch": <int>}` — **required**. The epoch guards against
  superseded workers; a 409 with `x-ccr-conflict-reason: superseded_by_worker`
  means another worker took over.

### `GET /worker/events/stream` — inbound SSE (server → worker)

Query params:

- `from_sequence_num=<n>` — resume after reconnect
- `keepalive_tolerance_seconds=<n>` — client-advertised tolerance (3× the
  client's max keepalive interval)

Headers: `Accept: text/event-stream`, plus auth; `Last-Event-ID: <n>` on
resume.

Response headers of interest:

- `Content-Type: text/event-stream` (checked)
- `x-ccr-sse-keepalive-interval-seconds: <n>` — echoed server keepalive
  interval
- `session_stream_writers: …` — display info about active writers (optional)

Frame shapes:

```
event: client_event
id: <sequence-num>
data: {"payload": {…SDK message…}, "event_id": "<string>", "event_type": "<string>", "sequence_num": <int>, "source": "client", "device_attestation_status": …}

event: ephemeral_event
data: {"event_type": "heartbeat_probe" | "owner_present"}
```

- `payload` is a full SDK stream-json message: `user`, `control_request`
  (`can_use_tool`, `request_user_dialog`, …), `system`, `result`, …
- `sequence_num` doubles as the SSE `id:` (replay anchor).
- A `control_request` with `source: "worker"` is dropped by the client.
- SSE comment keepalives (`: ping\n\n`) keep the connection alive; the client
  treats **no frame for keepalive-interval + 30 s** as dead and reconnects
  with backoff (500 ms base, 30 s cap, ~25% jitter). 401/403/404 on connect
  are permanent; the stream never re-POSTs on its own.

### `POST /worker/events` — outbound uploads (worker → server)

Two body shapes share this path:

1. **Batch upload** (the main lane — everything the SDK emits):

```json
{
  "worker_epoch": 1,
  "events": [
    {"payload": {"type": "assistant", "message": {…}, "uuid": "…"}},
    {"payload": {"type": "stream_event", "event": {…}}},
    {"payload": {"type": "result", "subtype": "success", …}},
    {"payload": {"type": "control_request", "request_id": "…", "request": {"subtype": "can_use_tool", "tool_name": "Bash", "input": {…}, "tool_use_id": "…"}}},
    {"payload": {"type": "user", "message": {"role": "user", "content": "…"}, "uuid": "…"}, "historical": true}
  ],
  "upload_hold": {…}                       // optional telemetry
}
```

2. **Raw SDK message** (legacy/direct write lane): a single message object
   such as `{"type":"user","message":{…}}` or `{"type":"keep_alive"}`.

Response: `200` (or `201`) with any JSON body. The client retries 5xx/429 with
backoff; 4xx (non-429) are dropped as unrecoverable.

The **permission flow**: the worker uploads a `control_request`
(`subtype: "can_use_tool"`); the controller answers by pushing a
`control_response` down the SSE stream:

```json
{"type": "control_response", "response": {"subtype": "success", "request_id": "<same id>", "response": {"behavior": "allow", "updatedInput": {…}}}}
```

Deny: `{"behavior": "deny", "message": "…"}`. A withdrawn ask is answered
with a `control_cancel_request` carrying the same `request_id`.

### `PUT /worker` — worker state

Body (registration/initial + status changes):

```json
{
  "worker_epoch": 1,
  "worker_status": "idle" | "running" | "requires_action" | "parked" | …,
  "connection_status": "disconnected",   // on goodbye
  "external_metadata": {…},
  "internal_metadata": {…},
  "requires_action_details": {…}
}
```

Response `200` with any JSON. If the server wants the worker to use the
**WebSocket upload stream** it answers with an `upload_stream` offer — omit it
and all uploads stay on HTTP. A `heartbeat_interval_seconds` field in
responses can retune the worker's heartbeat cadence.

### `GET /worker?include_hearth=true` — read worker state

Response: `{"worker": {"external_metadata": …, "internal_metadata": …}}`.
Used on start/resume to restore worker metadata; an auth-empty client skips
the call entirely.

### `POST /worker/heartbeat`

Body: `{"session_id": "…", "worker_epoch": 1, "supports_heartbeat_probe":
true, "current_interval_seconds": 60, "idle_seconds": 12}` → `200`.
Optional response field: `heartbeat_interval_seconds`.

### `POST /worker/events/delivery`

Body: `{"worker_epoch": 1, "updates": [{"event_id": "…", "status":
"received" | "processed"}]}` → `200`. Purely informational acks of inbound
SSE events.

### `POST /worker/internal-events` — transcript persistence

Body: `{"worker_epoch": 1, "events": [{"payload": {"type": "transcript",
…full SDK message…}, "is_compaction": false, "session_agent_id": "…",
"preserved_event_ids": ["…"]}]}` → `200` any JSON. This is the durable
transcript lane used to resume sessions.

### `GET /worker/internal-events` — transcript hydration

Query: `limit=1000`, optional `after_event_id`, `cursor`, `subagents=true`,
`session_agent_id=<id>`.

Response: `{"data": [{"payload": {…}}, …], "next_cursor": "…"}` — paginate
until no `next_cursor`. An empty `data` list starts a fresh conversation.
A 400 with `error.type = "after_event_id_not_found"` makes the client refetch
without the anchor.

## Bridge/host endpoints (only needed when driving the official bridge)

| Endpoint | Body | Response |
| -------- | ---- | -------- |
| `POST <api-base>/v1/code/sessions` | `{title, bridge: {}, tags?, session_grouping_id?, config: {cwd, model?, sources?, outcomes?}}` | `{session: {id: "cse_…"}}` |
| `POST <session-url>/bridge` | `{}` or `{expected_epoch}` | `{worker_jwt, expires_in, api_base_url, worker_epoch, mcp_config?}` |
| `GET/PUT <session-url>` | session record | session record |
| `POST <session-url>/archive` / `/unarchive` | `{}` | `200` |

`POST /bridge` 409 responses use `x-ccr-conflict-reason:
superseded_by_worker | epoch_stale` and an `error.type: "epoch_conflict"`
body with `expected_epoch`/`actual_epoch`.

## Work-queue mode (fleet environments)

A machine-registered bridge can poll work items instead of creating sessions
itself. Each work item carries a **work secret** — base64url JSON:

```json
{
  "version": 1,
  "session_ingress_token": "<bearer token>",
  "api_base_url": "https://…",
  "claude_code_args": ["--model", "…"],
  "mcp_config": {"content": "<json>", "path": "…", "mode": …},
  "auto_mode_environment": ["…"],
  "worktree": true
}
```

The bridge acknowledges/heartbeats each work item and spawns the worker with
the session URL derived from `api_base_url`.

## Bridge → worker spawn recipe (extracted verbatim)

```
claude --print \
  --sdk-url <session-url> \
  --session-id <session-id> \
  --input-format stream-json \
  --output-format stream-json \
  --replay-user-messages \
  --resume=<session-url> \
  [--verbose] [--debug-file …] [--permission-mode …] \
  [--settings …] [--mcp-config …] [--append-system-prompt-file …]
```

Environment set by the bridge (non-exhaustive):

```
CLAUDE_CODE_SESSION_ACCESS_TOKEN=<worker_jwt>
CLAUDE_CODE_ENVIRONMENT_KIND=bridge
CLAUDE_CODE_WORKER_EPOCH=<epoch>
CLAUDE_CODE_BRIDGE_OWNER_ACCOUNT_UUID / CLAUDE_CODE_BRIDGE_OWNER_ORG_UUID
CLAUDE_CODE_BRIDGE_SOURCE_DIR=<dir>
CLAUDE_CODE_BRIDGE_MCP_CARRIER=1
CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1          (epoch > 1)
SESSION_INGRESS_URL=<ingress url>              (when an mcp config is attached)
CLAUDE_CODE_OAUTH_TOKEN=                       (cleared)
```

The child's stdout still emits the full stream-json output (the bridge parses
it for activity tracking), so a spawner can monitor the worker without
touching the upload lane.

## Timing defaults (client side)

| Item | Default |
| ---- | ------- |
| SSE connect timeout | 30 s |
| Keepalive liveness | keepalive interval + 30 s |
| Reconnect backoff | 500 ms → 30 s, symmetric 25% jitter |
| Heartbeat seed | ~60 s, server-tunable |
| Upload retry | 500 ms → 30 s on 5xx/429 |
| Worker init PUT retries | 10 attempts, 500 ms·2ⁿ + jitter |
| Server config flags (`tengu_bridge_poll_interval_config`) | `non_exclusive_heartbeat_interval_ms: 0`, `multisession_poll_interval_ms_not_at_capacity: 2000`, `multisession_poll_interval_ms_at_capacity: 600000`, `session_keepalive_interval_v2_ms: 120000`, `sse_connect_timeout_ms: 30000` |

## Self-hosting notes (making a compatible server)

1. Serve `<allowlisted-host>` over HTTPS with a locally trusted cert
   (split-horizon DNS / `/etc/hosts` / container `--add-host`). Ports other
   than 443 are allowed — the allowlist checks the hostname only.
2. Implement the worker endpoints above. Minimum for a functioning worker:
   `POST /worker/register`, `GET /worker/events/stream` (SSE with
   keepalives), `POST /worker/events`, `PUT /worker`, `POST
   /worker/heartbeat`, `POST /worker/events/delivery`, `POST
   /worker/internal-events`, `GET /worker/internal-events`, `GET /worker`.
3. Do not offer `upload_stream` — the worker then uploads over HTTP only.
4. Reverse-proxy any other path on that host (e.g. `/v1/messages`) to the real
   API if the machine still needs normal API traffic, or point inference
   elsewhere (`ANTHROPIC_BASE_URL`, Bedrock, Vertex…). Inference never flows
   through the session ingress.
5. Spawn the worker exactly like the bridge does (recipe above). With an
   `ANTHROPIC_API_KEY` in the environment, no claude.ai subscription or OAuth
   login is involved anywhere.
