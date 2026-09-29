# Protocol

The Go types in `internal/api/protocol.go` are the source of truth for field
names. Argus has two reference clients: the Go client (`internal/client`, used
by the TUI and the CLI) and the Flutter app (`app/`).

Timeouts and intervals marked **client default** are values that the Go client
uses. A new client can pick different values for its use case. All other
values are fixed by the node or the gateway.

## Overview

### Components

| Component   | What it does                                                                                           |
| ----------- | ------------------------------------------------------------------------------------------------------ |
| **node**    | Runs on each machine. Discovers agent sessions, drives tmux, and serves the API.                       |
| **gateway** | Listens on HTTP and relays traffic between clients and nodes. It can run with a node or alone.         |
| **client**  | The TUI, the mobile app, or the CLI. It talks to a node directly or to nodes through a gateway.        |

A client reaches a node in one of two ways:

```
local:    client ──unix socket──▶ node

gateway:  client ──WebSocket /client──▶ gateway ◀──WebSocket /node── node
```

On the gateway path, the node dials out to the gateway. The gateway never
dials a node. A gateway that runs with a node reaches that node through a
loopback `/node` connection.

The gateway is blind. It does not read session data, and it does not merge
results from several nodes. The client opens one relay channel per node and
merges the results itself.

### What a client implements

A client that uses the gateway path must implement these parts:

1. Line framing over a WebSocket byte stream. See [Framing](#framing).
2. JSON-RPC 2.0 requests, responses, and notifications in both directions.
3. The roster: the `node.event` stream from the gateway.
4. One relay channel per online node. See [Relay channels](#relay-channels).
5. Noise IK and sealed records, if the client supports E2EE mode. See
   [End-to-end encryption](#end-to-end-encryption).
6. Composite ids, and routing of each call to the correct node. See
   [Aggregation](#aggregation).
7. The feature methods that the client needs. See [Features](#features).
8. Trust log verification, if the client supports locked mode. See
   [Locked mode](#locked-mode).

### Versions

The protocol has no version number and no compatibility check. The `version`
field in `server.info` and `node.identify` is the Argus build version. It is
for display only.

## Wire format

### Transports

**Unix socket.** Each node listens on a unix socket. The default path is
`argus.sock` in the runtime directory (configuration key `socket`). The CLI,
the TUI on the same machine, and the `argus hook` command use this socket. The
socket has no token authentication. It is the only transport that serves the
`lock.*` methods.

**WebSocket.** The gateway serves plain HTTP on `:8443` by default
(`--listen-addr`). It has two WebSocket routes and no other HTTP routes:

| Route     | Who connects | Token accepted                     |
| --------- | ------------ | ---------------------------------- |
| `/node`   | nodes        | the master token only              |
| `/client` | clients      | the master token or a client token |

The gateway does not terminate TLS. For `wss://`, put a tunnel, an SSH uplink,
or a reverse proxy in front of the gateway. See
[Multi Machine](/guide/multi-machine). The gateway does not verify the
`Origin` header, because the token controls access. The read limit is 16 MiB
per WebSocket message.

**Gateway URLs.** A gateway URL is a base URL. The client appends the route.

- `ws://host[:port][/base-path]` or `wss://...`. The Go client appends
  `/node` or `/client` after the base path. The reverse proxy must remove the
  base path, because the gateway serves the routes at the root.
- `ssh://[user@]host[:sshport][?port=N]`. The Go client runs `ssh -W` to
  `127.0.0.1:N` on the gateway host and speaks WebSocket through that tunnel.
  `port` is `8443` by default. This form takes no path.

The Flutter app accepts only `ws://` and `wss://` URLs without a path.

### Framing

Every frame is one JSON object on one line, followed by `\n`. The maximum line
length is 16 MiB. A receiver skips empty lines. If a line is longer than
16 MiB, the receiver closes the connection.

On a WebSocket, treat the text messages as one byte stream. A WebSocket
message does not always hold exactly one frame. The Go peer writes through a
4 KiB buffer. As a result, a large frame arrives as one message, and its `\n`
arrives in the next message. A client must buffer the input and split it on
`\n`.

If a line is not valid JSON, the receiver sends back an error response with
code `-32700`, message `"parse error"`, and no `id`. Then it continues with
the next line.

### Envelope

The envelope is JSON-RPC 2.0 with two extra fields, `route` and `body`:

```ts
{
  jsonrpc: "2.0"
  id?:     number        // requests and responses
  method?: string        // requests and notifications
  route?:  RouteHeader   // relay frames only
  body?:   string        // relay frames only (base64)
  params?: any           // direct frames only
  result?: any           // direct frames only
  error?:  { code: number, message: string }
}
```

A frame belongs to one of these classes:

| Class        | Rule                      |
| ------------ | ------------------------- |
| request      | has `method` and `id`     |
| notification | has `method` and no `id`  |
| response     | has `id` and no `method`  |
| relay frame  | has `route`, of any shape |

A relay frame never reaches the normal dispatch path. It carries its payload
in `body`, not in `params` or `result`. See [Relay channels](#relay-channels).

Both sides of a connection can send requests. For example, on the node uplink
the gateway sends `node.identify` to the node, and the node sends
`trustlog.push` to the gateway.

### Request ids

Each side picks its own request ids. Use integers. A server echoes the `id`
exactly as it received it, but the Go peer matches responses only as integers.

- On direct frames, the Go peer starts at `0` and counts up.
- On relay frames, the Go client and the app use one counter for all channels
  of a connection. The counter starts at `1`.

### Errors

| Code     | Name             | Use                                                        |
| -------- | ---------------- | ---------------------------------------------------------- |
| `-32700` | parse error      | The line is not valid JSON.                                |
| `-32600` | invalid request  | A refused operation. Some handlers also use it for bad params. |
| `-32601` | method not found | The receiver does not serve the method on this connection. |
| `-32603` | internal error   | Any other handler failure.                                 |
| `410`    | push target gone | `push.test` hit a dead push subscription.                  |

A handler error that is not a JSON-RPC error becomes `-32603` with the error
text as `message`. Most handlers return plain errors. As a result, bad params,
a missing field, and `unknown session: <id>` usually arrive as `-32603`. A
client must not depend on the code to tell these errors apart. The `message`
text is the only detail.

### Keepalive

`ping` is a request with no params and a `null` result. The peer answers it
directly, before any dispatch rule. As a result, a `ping` always works, even
on a connection that serves no other method.

Only two links send pings:

- The gateway pings each node every 15 seconds and waits 5 seconds for the
  reply. After two failed pings in a row, it closes the uplink. Any reply
  counts as a success, also an error reply.
- The client pings the gateway. The client default is the same as above. The
  app uses a 20-second interval and a 10-second timeout, and closes after one
  failure.

The gateway does not ping clients, and nodes do not ping the gateway. A client
that must detect a dead connection must send its own pings.

### Write deadline

Every frame write in the Go peer has a 10-second deadline. If a write does not
complete in time, the peer closes the whole connection, because a partial
frame corrupts the stream. On the gateway, this means that one slow relay
channel can close a client connection or a node uplink.

## Connecting a client

### Authentication

The client sends its token in the WebSocket handshake:

```http
Authorization: Bearer <token>
```

If the header is absent, or if it does not start with the exact text
`Bearer `, the gateway reads the `token` query parameter. Browsers need this
fallback because they cannot set headers on a WebSocket handshake.

If the token is wrong, the gateway answers `401` with the body `unauthorized`
and does not upgrade the connection.

There are two kinds of token:

- **Master token.** This is the `--token` value (`$ARGUS_TOKEN`). Nodes must
  use it on `/node`. A client that uses it on `/client` is an **admin**
  connection. If `--token` is empty, `/node` accepts any token.
- **Client token.** The gateway issues it during pairing. It is exactly 64
  lowercase hex characters. It works only on `/client` and never gives admin
  rights.

Admin connections can call `clients.*` and `pushport.setToken`. Other
connections get `-32600` with the message `unauthorized: admin token required`.

### Connection sequence

The Go client connects in this order:

1. Open the WebSocket to `<gateway>/client` with the token.
2. Receive the roster. The gateway sends one `node.event` of type `added` for
   each node in the roster, then one `node.event` for each roster change.
3. For each node with `online: true`, call `relay.open`. Skip a node without
   `identity_pubkey` if you use E2EE mode.
4. In E2EE mode, do the Noise handshake on the channel. See
   [Handshake](#handshake).
5. Call `node.identify` on the channel. In locked mode, record the `tip`.
6. Receive the session snapshot. The node sends one `session.event` of type
   `added` for each current session, then live events.
7. Send feature calls.

The session snapshot starts when the channel exists. In plaintext mode, that
is when the first frame arrives at the node. As a result, `session.event`
notifications can arrive before the response to your first request. The
snapshot has no end marker. To get a complete list at a known time, call
`sessions.list`.

On the unix socket, the node starts the same `session.event` stream as soon as
the client connects. There is no roster and no relay channel. Send requests as
direct frames.

### Roster events

`node.event` has the type `added`, `online`, `offline`, `removed`, or
`trust-changed`:

- A new node gives `added`.
- A node that reconnects within 30 seconds of a disconnect gives `online`,
  not `added`.
- A node that disconnects gives `offline`. After 30 seconds, the gateway
  removes it and sends `removed`.
- `trust-changed` tells the client to sync the trust log. Its `node` field is
  an empty descriptor.

The initial snapshot also sends `added` for offline nodes that are in the
30-second window, with `online: false`. `relay.open` fails for these nodes.

A second uplink with the same node id replaces the first. The gateway
subscribes the client before it sends the snapshot, so a client can see one
node event twice. If the event buffer of a client (64 events) is full, the
gateway drops the event for that client.

### Reconnect

The client default is to dial again after a disconnect. The wait starts at
500 ms and doubles up to 15 seconds. After a successful connection, the wait
goes back to 500 ms. The app waits from 1 second up to 30 seconds.

After a reconnect, repeat the full connection sequence. Old channel ids are
not valid on a new connection.

### Pairing a new device

A client token is 32 random bytes, written as 64 lowercase hex characters. The
pairing flow is:

1. An admin client calls `clients.pairStart`. The gateway returns a new token
   and its public URL. The token stays pending in memory for 60 seconds. `url`
   can be empty if the gateway has no public URL.
2. The admin client shows a QR code with this URI:

   ```
   argus://pair?url=<ws|wss base URL>&token=<token>
   ```

   The Go generator keeps a reverse-proxy base path in `url`. The app also
   accepts an optional `&e2e=true`, which selects E2EE mode. Nothing else on
   the wire tells a client which mode to use.
3. The admin client calls `clients.pairAwait` with the token. The call blocks
   until a device connects with the token, or until 60 seconds pass.
4. The new device connects to `/client` with the token. The gateway stores the
   token and wakes the waiting `clients.pairAwait`.

`clients.pairAwait` returns `connected: false` for an unknown token, for a
second await on the same token, and after the timeout. It does not return an
error in these cases.

The gateway stores each token as `client-tokens/<sha256(token)>.json` in the
state directory. `clients.list` returns the SHA-256 id in the field `token`,
not the raw token. `clients.remove` takes that id.

## Relay channels

A relay channel connects one client to one node through the gateway. Every
request that a client sends to a node travels on a relay channel. The gateway
forwards relay frames by channel id and does not decode `body`.

### Opening a channel

The client sends a normal request to the gateway:

```json
{"jsonrpc":"2.0","id":7,"method":"relay.open","params":{"node_id":"home"}}
```

The gateway answers with a channel id:

```json
{"jsonrpc":"2.0","id":7,"result":{"chan_id":"c12"}}
```

Channel ids come from one counter for the whole gateway. They start again at
`c1` when the gateway restarts. The gateway does not tell the node about the
new channel. The node learns about it from the first frame that arrives.

| Error                                           | Cause                            |
| ----------------------------------------------- | -------------------------------- |
| `-32600 invalid params: ...`                    | The params do not decode.        |
| `-32600 unknown node: <id>`                     | No live uplink for this node id. |
| `-32600 too many open channels for this client` | The client already holds 64 channels. |

### Route header

```ts
RouteHeader {
  chan_id:  string    // the routing key
  node_id?: string    // set on client-to-node requests
  sub_id?:  string    // reserved; current nodes do not set it
  term_id?: string    // reserved; current nodes do not set it
}
```

The gateway forwards a frame only if its `chan_id` exists and the sender owns
that side of the channel. It drops other relay frames without a reply.

### Frame shapes

A relay frame keeps `id` and `method` in cleartext. The payload goes in `body`
as a JSON string with standard base64 (with padding).

| Frame        | Cleartext fields        | `body` holds                                      |
| ------------ | ----------------------- | ------------------------------------------------- |
| request      | `id`, `method`, `route` | `seal(params)`                                    |
| response     | `id`, `route`           | `seal({"result": ...})` or `seal({"error": ...})` |
| notification | `method`, `route`       | `seal(params)`                                    |

A request:

```json
{"jsonrpc":"2.0","id":42,"method":"sessions.list","route":{"chan_id":"c12","node_id":"home"},"body":"<base64>"}
```

The response to it:

```json
{"jsonrpc":"2.0","id":42,"route":{"chan_id":"c12"},"body":"<base64>"}
```

The error detail of a response is inside `body`. The gateway cannot read it.
If a request has no params, `seal` gets an empty input.

`seal` depends on the mode:

- **Plaintext mode.** `seal` does nothing. `body` is the base64 of the inner
  JSON.
- **E2EE mode.** `seal` encrypts with the Noise session of the channel. See
  [Sealed records](#sealed-records).

### Plaintext mode

This mode is the default. There is no handshake. The client sends requests
directly after `relay.open`. If a frame arrives for an unknown `chan_id`, the
node creates the channel and starts the `session.event` stream on it. The Go
client sends `node.identify` as its first frame.

### Order and delivery

- The node serves each request in its own goroutine. Responses can arrive in
  a different order than the requests.
- Notifications on one channel arrive in the order that the node sent them.
- The node acts only on requests. It ignores notifications and responses from
  the client on a channel.
- If the node cannot open the `body` of a request, it drops the frame without
  a reply. The client sees only its own timeout.

The client default timeout for a call on a relay channel is 30 seconds. Calls
to the gateway itself have no client timeout.

### Mode mismatch

The client and the node must use the same mode. There is no error for a
mismatch:

- A node in E2EE mode drops frames for an unknown `chan_id` that are not
  `e2e.handshake`. A plaintext client sees only timeouts.
- A node in plaintext mode also accepts `e2e.handshake`.

### Limits and teardown

The gateway keeps a queue of 64 frames per channel and direction. If a queue
is full, the gateway closes the channel. It never drops one frame, because a
missing frame breaks the Noise nonce order.

The gateway closes a channel in these cases:

- The client calls `relay.close` with the `chan_id`.
- The client disconnects.
- The node uplink closes.
- A frame write to either side fails.
- A queue overflows.

The gateway does not send a teardown frame to either side. The node frees its
channel state when its uplink closes. The client drops a channel when it gets
a `node.event` of type `offline` or `removed`. It opens a new channel when the
node is `online` again.

`relay.close` returns `null`, also for a channel that the caller does not own.

## End-to-end encryption

In E2EE mode, each relay channel is a Noise session between the client and the
node. The gateway relays the handshake and the sealed frames, but it cannot
read them. Set `e2ee.enabled: true` on nodes and clients to use this mode. See
[End-to-End Encryption](/guide/e2ee).

### Keys

| Key             | Type       | Where                                                                   |
| --------------- | ---------- | ----------------------------------------------------------------------- |
| node identity   | Curve25519 | `node-identity.json` in the state directory                             |
| client identity | Curve25519 | Go: `client-identity.json` in locked mode, else one key per process. App: always stored. |
| node signer     | Ed25519    | `signer-key.json` in the state directory (locked mode)                  |

Key files are JSON with standard base64 values:
`{"private":"<base64>","public":"<base64>"}`. Curve25519 keys are 32 bytes.

The node reports its identity public key in `node.identify` as
`identity_pubkey` (base64). The gateway copies it to the roster (`nodes.list`,
`node.event`). The client reads the key from the roster.

The gateway can replace a roster key. If it does, the Noise handshake fails
against the real node. In locked mode, the client also requires that the trust
log authorizes the key.

### Noise parameters

| Item      | Value                                          |
| --------- | ---------------------------------------------- |
| Protocol  | `Noise_IK_25519_ChaChaPoly_BLAKE2s`            |
| Initiator | the client                                     |
| Payloads  | Both handshake messages have an empty payload. |

The prologue is:

```
argus-e2e/v1|<node_id>|<chan_id>
```

The prologue binds the session to the node id and the channel id that the
gateway assigned. For example, node `home` and channel `c12` give the
prologue `argus-e2e/v1|home|c12`. Both sides must build it from the same
values.

The Go side uses `github.com/flynn/noise`. The app has its own
implementation. Both follow the Noise specification:

- The ChaCha20-Poly1305 nonce is 4 zero bytes, then the 64-bit message counter
  in little-endian order.
- After the handshake, the initiator encrypts with the first cipher state and
  decrypts with the second. The responder uses the reverse.
- Message 1 is 96 bytes: ephemeral key (32), encrypted static key (48), and
  the tag of the empty payload (16).
- Message 2 is 48 bytes: ephemeral key (32) and the tag of the empty
  payload (16).

### Handshake

1. The client calls `relay.open` and gets a `chan_id`.
2. The client builds Noise message 1 and sends it as a relay notification.
   `body` is the base64 of the raw Noise message. It is not sealed.

   ```json
   {"jsonrpc":"2.0","method":"e2e.handshake","route":{"chan_id":"c12"},"body":"<base64(msg1)>"}
   ```

3. The node reads the static key of the client from message 1. The node drops
   the frame and sends nothing in these cases:
   - The node is quarantined.
   - Locked mode is on and the trust log does not authorize the key. See
     [Enforcement](#enforcement).
   - A channel with this `chan_id` already exists.
4. Otherwise, the node sends message 2 in the same frame shape.
5. The node starts the `session.event` stream on the channel.
6. The client completes the handshake.

A refused client gets no error frame. It sees only its own timeout. The client
default timeout for message 2 is 10 seconds. The app uses 15 seconds.

### Sealed records

`seal` turns one message into one or more records:

```
record = uint16_be(len(ciphertext)) || ciphertext
```

- Each record holds at most 65519 bytes of plaintext.
- The AEAD associated data of each record is 5 bytes: the 0-based record index
  as `uint32_be`, then `0x01` for the last record or `0x00` for the others.
- An empty message gives one empty final record.
- A receiver rejects an empty blob and a blob with a missing last record.

The Noise cipher state supplies the nonces. Each side must seal and open
messages in wire order. There is no rekey.

Each record that opens correctly advances the receive nonce. If the first
record of a blob fails, the nonce does not change and the receiver drops the
frame. If a later record fails, the earlier records already advanced the
nonce. The channel is then out of sync and cannot recover. Close it and open a
new channel.

### Methods on a channel

The node serves every method on a channel except `lock.*`. It returns
`-32601` for `lock.*`. It acts only on sealed requests, that is, frames with
both `id` and `method`.

## Aggregation

A client that talks to several nodes must merge their data and route each call
to one node. The gateway does not do this work.

### Composite ids

A node uses local ids. A client that talks to several nodes turns them into
composite ids:

```
<node_id>:<local_id>
```

The separator is the first `:`. A node id must not contain `:`. A local id can
contain `:`. For example, node `home` and session `default:%3` give
`home:default:%3`.

Local session ids have these forms:

| Form                         | Use                                                    |
| ---------------------------- | ------------------------------------------------------ |
| `<tmux_server>:<pane_id>`    | sessions in tmux. The servers are `default` and `argus` (sessions that Argus spawned). |
| `<agent>:<agent_session_id>` | headless agents, for example opencode                  |

The client makes these ids composite:

- `id` and `workspace_id` of each session, in `sessions.list`,
  `sessions.refresh`, and `session.event`
- `id` of each project and workspace in `project.list`
- `session_id` in the results of `sessions.spawn` and `sessions.resume`
- `workspace_id` in the result of `workspace.create`
- `session_id` in `tasks.changed`

The client adds `node_id` and `node_label` to sessions, projects, and history
items. History items are not composite. A node accepts a composite id with its
own prefix in `sessions.focus`.

### Routing

The client picks the node for each call:

| Rule | Methods |
| ---- | ------- |
| All nodes; merge results | `sessions.list`, `sessions.refresh`, `sessions.historyProjects`, `project.list` |
| All nodes; success if one node succeeds | `push.register`, `push.unregister`, `push.test`, `push.setPause` |
| Node from composite `session_id` | `sessions.transcriptView`, `sessions.toolDetail`, `sessions.capture`, `sessions.input`, `sessions.key`, `sessions.respond`, `sessions.kill`, `sessions.focus`, `sessions.tasks`, `sessions.changedFiles`, `sessions.fileDiff`, `sessions.commits`, `sessions.commitFiles`, `transcript.subscribe`, `terminal.open` |
| Node from composite `workspace_id` | `workspace.changedFiles`, `workspace.diff`, `workspace.listDir`, `workspace.readFile`, `workspace.commits`, `workspace.commitFiles`, `workspace.remove`, `workspace.setTarget`, `workspace.runSetup`, `workspace.setupLog` |
| Node from composite `project_id` | `workspace.create`, `project.rename`, `project.setHidden`, `project.setPinned`, `project.forget`, `project.branches`, `project.prs`, `project.issues` |
| Node from `node_id` param | `sessions.spawn`, `sessions.resume`, `agents.list`, `sessions.exportBundle`, `sessions.historySessions`, `sessions.historyTranscript`, `sessions.historyToolDetail` |
| Node that owns the `sub_id` or `term_id` | `transcript.unsubscribe`, `terminal.input`, `terminal.resize`, `terminal.close` |
| The gateway | `ping`, `server.info`, `nodes.list`, `relay.open`, `relay.close`, `trustlog.sync`, `push.vapidKey`, `clients.*`, `pushport.setToken` |

The client removes the node prefix before it sends an id to a node. The Go
client uses these error texts:

| Error | Cause |
| ----- | ----- |
| `-32600 session id is not gateway-qualified: <id>` | The id has no node prefix. The same text exists for `workspace id` and `project id`. |
| `-32600 <method> requires node_id` | `node_id` is empty and more than one node is connected. |
| `-32600 <method> requires a handle id` | `sub_id` or `term_id` is empty. |
| `-32600 <method>: unknown handle <id>` | The client did not open this `sub_id` or `term_id`. |

If `node_id` is empty and exactly one node is connected, the client uses that
node. If no node channel exists, the `push.*` fan-out goes to the gateway,
which returns `-32601`.

`sub_id` and `term_id` are local to one connection on the node. The client
must remember which node owns each handle.

### Offline nodes

When a node goes offline, the Go client sends `updated` events with
`offline: true` for the sessions of that node. After 30 seconds, or when the
node is removed, it sends `removed` events. The next real `session.event` from
the node sets `offline` back to `false`. While a node is offline, calls to it
fail in the client without a request.

## Features

### Sessions

`session.event` is a notification from the node:

```ts
{ type: "added" | "updated" | "removed", session: Session }
```

`updated` carries the full session, not a patch. If the event buffer of a
subscriber (64 events) is full, the node drops the event for that subscriber.
The node subscribes before it sends the snapshot, so one event can arrive
twice. To repair the list, call `sessions.list` or `sessions.refresh`.

A session with a tmux pane accepts pane methods: `sessions.capture`,
`sessions.input`, `sessions.key`, `sessions.kill`, and `sessions.focus`. For
a session without a pane, these methods fail with
`session has no terminal control`. For an unknown id, they fail with
`unknown session: <id>`.

`sessions.kill` kills the tmux pane. For a session without a pane, it removes
the session card.

**Answering an interaction.** When a session waits for input, its
`interaction` field describes the request. Answer it with `sessions.respond`:

- If a `PermissionRequest` hook is held, the node turns the structured fields
  (`behavior`, `reason`, `answers`, `question_action`, `set_mode`,
  `option_value`) into the hook decision.
- If no hook is held and the agent is opencode, the node passes the answer to
  the opencode service.
- In all other cases, the node drops the answer and returns success.

For `option_value`, the node maps `deny` to a deny, `allow` to an allow, and
any other value to an allow that also sets that permission mode. The node
ignores `kind`, `option_index`, and `text`.

### Transcripts

1. The client picks a `sub_id` and calls `transcript.subscribe` with the
   number of chunks it already holds (`have_chunks`).
2. The result is a `TranscriptDelta` that brings the client up to date.
3. The node reads the transcript every second. If it changed, the node sends
   a `transcript.delta` notification.
4. The client calls `transcript.unsubscribe` with the `sub_id` to stop.

To apply a delta, the client cuts its chunk list to `from_index`, then appends
`chunks`. Filter deltas by `sub_id`.

- The first result starts at `have_chunks - 1`, because the last cached chunk
  can have grown.
- The node does not verify the cached chunks of the client. The cache must
  come from the same session and the same `agent_id`.
- `from_index` can be less than the length of the client list, because the
  node can change the last chunks.
- A delta can look identical on the client, because the node compares fields
  that it does not send.
- A second subscribe with the same `sub_id` replaces the first. All
  subscriptions end when the connection closes.
- Set `agent_id` to follow a subagent transcript.

Errors: `sub_id required`, `unknown session: <id>`,
`session has no transcript: <id>`, and `unknown subagent: <id>`.

Transcript items do not carry `toolInput` and `result`. To get these fields,
the client calls `sessions.toolDetail`.

The node sends `tasks.changed` on the same subscription when the task count
in the transcript goes up. The client then calls `sessions.tasks`. The node
sends it only for subscriptions without `agent_id`, only for agents with task
support (today, Claude Code), and never at subscribe time.

### Terminals

1. The client picks a `term_id` and calls `terminal.open` with the size.
2. The node sends `terminal.output` notifications.
3. The client sends keystrokes with `terminal.input`.
4. The client sends `terminal.resize` when its size changes.
5. The client calls `terminal.close` to stop.

The node runs `tmux attach` to a mirror session that shows only the pane of
the session. The first output is the screen that tmux draws on attach. There
is no separate snapshot.

- `data` is standard base64 in both directions. One output chunk holds at
  most 32 KiB. A chunk can end inside a UTF-8 sequence.
- The terminal size also changes the size of the real tmux window of the
  agent.
- The default size is 80×24. The maximum is 1000 in each direction. The node
  ignores a resize with a size of 0 or less.
- For a session without a pane, the node first creates a viewer pane.
- A second `terminal.open` with the same `term_id` replaces the first.
- `terminal.close` does not send `terminal.exited`.

The node sends `terminal.exited` when the terminal ends:

| `reason`  | Meaning                                                                  |
| --------- | ------------------------------------------------------------------------ |
| `exited`  | The process ended, or the session left the registry. An empty `reason` means the same. |
| `evicted` | A newer `terminal.open` for the same session, from any connection. The last opener wins. |

If the client sets `client_pane` and that pane shares a tmux window with the
session, the node refuses the open.

Errors: `term_id required`, `unknown term_id: <id>`, `bad base64`, and
`terminal not available for this session`.

### Projects and workspaces

`project.changed` is a notification with empty params. It tells the client to
call `project.list` again.

- `project.forget` deletes the registry rows and keeps the files. It fails if
  the project has live sessions.
- `workspace.remove` fails for the main worktree and for a workspace with live
  sessions. Without `force`, it fails for uncommitted changes and for a
  teardown script failure. With `force`, it removes the workspace and returns
  a teardown failure as `warning`.
- `against: "target"` has an effect only on `workspace.changedFiles`.
- `workspace.commits` always lists commits since the merge base with the
  target branch. It returns an empty list if the workspace has no target.
- `path: ""` is the root only for `workspace.listDir`. `workspace.diff`
  fails with `path is required`.

### Push notifications

Nodes send push notifications to mobile devices. The gateway delivers them but
cannot read them. The device side of the flow is:

1. Call `push.vapidKey` on the gateway to get the VAPID public key. An empty
   `key` means that Web Push is not available.
2. Subscribe with a push distributor (UnifiedPush or PushPort).
3. Call `push.register` with a stable `device_id`, the endpoint, and the
   subscription keys. The client sends this call to every node. `p256dh` and
   `auth` are base64url without padding.
4. Decrypt each push message (RFC 8291, `aes128gcm`).

The node sends a push when a session changes to `awaiting_input`. It does not
send pushes for events in the initial snapshot. It skips a device whose
`paused_until` is in the future.

The cleartext payload is:

```json
{"id":"<32 hex>","title":"...","body":"...","data":{"session_id":"...","node_id":"..."}}
```

`id` is random for each delivery. The device uses it to drop duplicates.
`session_id` is the local id. Join it with `node_id` to get the composite id.
A `push.test` payload has `data: {"test": "1"}` and no `session_id`.

## Locked mode

Locked mode uses a signed, append-only trust log. The log controls which
client devices can open E2EE channels to a node, and which nodes a client
trusts. The design follows Tailscale tailnet lock. See
[End-to-End Encryption](/guide/e2ee) for the user guide.

A client that supports locked mode must decode and verify the log, pick one
chain, and refuse nodes that the log does not authorize.

### Entry encoding

All integers are big-endian. A field is a `uint32` length followed by the
bytes. A zero-length field decodes as empty.

| Kind value | Kind               | Payload                                         |
| ---------- | ------------------ | ----------------------------------------------- |
| `1`        | `genesis`          | `Signers`, `Disablements`                       |
| `2`        | `add-signer`       | `Key` = signer public key                       |
| `3`        | `remove-signer`    | `Key` = signer public key                       |
| `4`        | `authorize-device` | `Key` = device public key                       |
| `5`        | `revoke-device`    | `Key` = device public key                       |
| `6`        | `disable`          | `Key` = disablement secret                      |
| `7`        | `revoke-signer`    | `Signers` = revoked keys, `Replaces`, `CoSigns` |

The signed bytes are:

```
kind(1 byte)
field(Prev)
uint32(count) field(Signers[i])...
uint32(count) field(Disablements[i])...
field(Key)
field(Signer)
[kind 7 only] uint32(count) field(Replaces[i])...
```

The wire form of an entry is:

```
signed bytes
field(Sig)
[kind 7 only] uint32(count) (field(CoSign.Signer) field(CoSign.Sig))...
```

Co-signs are sorted by signer, then by signature. A decoder rejects trailing
bytes.

| Item         | Value                                                        |
| ------------ | ------------------------------------------------------------ |
| Hash         | BLAKE2s-256 over the wire form of the entry                  |
| Signature    | Ed25519 over the signed bytes. `Signer` is a 32-byte key.    |
| Link         | `Prev` is the hash of the previous entry. It is empty for genesis. |
| Genesis hash | the hash of entry 0. It identifies the log.                  |

A `revoke-signer` entry has an empty `Signer` and an empty `Sig`. Its co-signs
are its only authentication. Each co-sign is an Ed25519 signature over the
signed bytes. A client that fills `Signer` or `Sig` computes a different hash.

A chain (in files and in chain assembly) is `uint32(count)`, then
`field(entry wire form)` for each entry. The decoder limits are 1 MiB per
field, 4096 items per list, and 65536 entries per chain.

### Chain rules

- A genesis must have an empty `Prev`, at least one signer, no duplicate
  signers, and a `Signer` from its own `Signers`.
- Every other entry must extend the current tip, and the log must not be
  disabled.
- A decoder rejects an unknown kind.
- `add-signer`, `remove-signer`, `authorize-device`, and `revoke-device`
  need a non-empty `Key` and a signature from a trusted signer.
- `authorize-device` fails if the device is already authorized.
- `remove-signer` fails if the key is not a signer or if it is the last
  signer.
- A `disable` entry is valid if the Argon2id commitment of its secret is in the
  genesis `Disablements`. It needs a valid signature, but any key can sign it.
  A disable is permanent.
- When a signer is removed or revoked, the devices it authorized are also
  removed.

A `revoke-signer` entry has these extra rules:

- `Signers` (the revoked keys) and `Replaces` must not overlap.
- The number of distinct trusted co-signers must be more than the number of
  revoked keys.
- A co-sign from a revoked signer counts only if `Replaces` is not empty.
- At least one signer must remain, including the keys in `Replaces`.
- `Prev` is the fork point: the parent of the first entry of the revoked
  signer, or a chosen fork point.

The disablement commitment is Argon2id with the salt
`argus-trustlog-disablement-v1`, time 1, memory 64 MiB, 4 threads, and a
32-byte output. A secret is 32 random bytes.

### Fork choice

When two chains have the same genesis, pick one with these rules, in order:

1. A disabled chain beats a chain that is not disabled.
2. If one chain is a prefix of the other, the longer chain wins.
3. Otherwise, compare only the first different entry of each chain. The entry
   with more weight wins. The weight of a `revoke-signer` entry is its number
   of valid co-signs from signers that were trusted at the fork. The weight of
   any other entry is `1` if its signer was trusted at the fork.
4. On a tie, a removal (`remove-signer` or `revoke-signer`) wins.
5. On a second tie, the lower entry hash wins.

### Distribution

The gateway keeps the entries in memory. It removes duplicates by hash. It
does not verify signatures, but it cannot forge or reorder entries, because
every entry is signed and linked by hash.

`trustlog.sync` is available to nodes and clients:

- The caller sends `known`, the hashes of all entries it holds. If the list is
  longer than 4096 hashes, the caller cuts it and sets `truncated`.
- The gateway returns every entry that is not in `known`, parents first.
- `want` lists hashes in `known` that the gateway does not hold. A node answers
  with `trustlog.push`. A client ignores `want`.
- `disjoint` is true if `known` is not empty, not truncated, and shares no
  hash with the gateway.

To build chains from the result, merge the new entries with the entries that
you hold. Find each entry that no other entry names as `Prev`. Walk back from
each of these entries to the genesis, and drop incomplete branches. Then apply
[fork choice](#fork-choice) to each complete chain.

The client default is to sync on connect, every 5 minutes, and after each
`node.event` of type `trust-changed`. The app syncs every 30 seconds.

### Enforcement

A node verifies the client key at the end of each Noise handshake. It refuses
the channel in these cases:

- The node is quarantined and not locally disabled.
- Locked mode is on, the node is not locally disabled, the log is not
  disabled, and the log does not authorize the client key.

An empty log authorizes no device. When the log changes, the node closes
channels whose client is no longer authorized.

The client does the same check in the other direction. It opens a channel only
to nodes whose identity key the log authorizes, unless the log is disabled.
When the log changes, it closes channels to nodes that are no longer
authorized.

The `signer_pubkey` in the roster comes from the gateway. Use it for discovery
only. For a trust decision, read the key from `node.identify` on an
authenticated channel.

### Equivocation

A gateway can show different chains to different parties. The client detects
this with the node tips:

1. After each trust sync, call `node.identify` on each channel and record
   `tip`.
2. Look up each tip in the set of all entry hashes of the chosen chain.
3. If the same tip value is missing for two checks in a row, report
   equivocation. A changed tip starts the count again.

The client skips nodes that were connected and are now offline. The Go client
keeps the flag until it exits. It reports the flag but does not close
channels.

### Pinning and quarantine

A node and a client can pin the genesis hash. The pin comes from the
configuration key `lock.genesis` (form `gen:<hex>`) or from a pin file in the
state directory. The pin files are `trustlog-genesis` (node) and
`client-trustlog-genesis` (client). Each holds the 32 raw bytes of the hash.
If both sources exist and disagree, the node does not start. A node that runs
`lock.init` pins its new genesis.

If a node without a log sees a valid chain on the gateway, or if its disabled
chain is superseded by another root, it becomes quarantined. A quarantined
node refuses all channels until an operator pins a genesis with `lock.pin` or
runs `lock.localDisable`. The Go client applies the same rules to itself.

The app does not use a pin configuration and has no quarantine. It pins the
first valid chain that it receives, and it enforces locked mode after it has
a log.

Keys in text form use a prefix and 64 hex characters: `sigpub:`, `devpub:`,
`gen:`, `dis:`, and `tip:`.

## Node and gateway internals

A client does not use the protocol in this section.

### Node uplink

A node connects to `<gateway>/node` with the master token. Then this sequence
occurs:

1. The gateway sends `node.identify` to the node. The node must answer in 30
   seconds with a non-empty `id`, or the gateway closes the connection.
2. The gateway adds the node to its roster and tells all clients with a
   `node.event` notification.
3. The link stays open. On this link, the node answers only `node.identify` and
   `ping`. All client traffic arrives in relay frames.

The node calls these methods on the gateway through the uplink: `nodes.list`,
`trustlog.sync`, `trustlog.push`, and `push.deliver`. The gateway returns
`-32601 method not found: <method>` for other methods.

If the link drops, the node dials again. The wait starts at 500 ms and doubles
up to 15 seconds. After a successful connection, the wait goes back to 500 ms.

**Trust log.** A node pushes its full chain with `trustlog.push` after each
local change. It syncs on connect and every 5 minutes. When a push adds
entries, the gateway sends these notifications:

- `trustlog.changed` with all head hashes, to every node except the sender.
  The node pulls, at most once per second, if it does not hold all heads.
- `node.event` of type `trust-changed`, to every client.

### Push delivery

1. The node builds the payload and encrypts it for the device (RFC 8291,
   `aes128gcm`).
2. The node sends `push.deliver` to the gateway with the ciphertext. A
   gateway that runs with the node delivers in the same process.
3. The gateway posts the ciphertext to the endpoint.

The encrypted body is one `aes128gcm` record. The plaintext ends with the
delimiter `0x02` and has no padding.

```
salt(16) || rs = 4096 (uint32_be) || idlen = 65 || sender_public_key(65) || ciphertext
```

The gateway posts with these headers:

```http
Content-Type: application/octet-stream
Content-Encoding: aes128gcm
TTL: 1800
Urgency: high
Authorization: vapid t=<ES256 JWT>, k=<base64url public key>
```

If a PushPort token is set and the endpoint host is the PushPort host, the
header is `Authorization: Bearer <pit_ token>`. If the gateway has no VAPID
key, it sends no `Authorization` header.

The subscription is gone in these cases:

- The push service answers `404` or `410`.
- On the VAPID path, the service answers `403` with a body that contains
  `do not correspond`.

`push.deliver` then returns `{"gone": true}`, and the node deletes the device
record. `push.test` returns error `410` in this case.

### Hooks

The `argus hook` command sends agent hook events to the local node over the
unix socket. The method is `hook.event`:

```ts
HookEvent {
  agent:       string   // for example "claude"
  event:       string   // for example "PermissionRequest"
  tmux_pane:   string
  tmux_socket: string   // base name of the $TMUX socket
  payload:     any      // the raw hook JSON from stdin (at most 8 MiB)
  auto_mode:   boolean  // CLAUDE_CODE_ENABLE_AUTO_MODE is "1"
  env?:        { [name: string]: string }
}
```

For `PermissionRequest` from Claude Code and Codex, the call blocks if the
session is live. The node holds it until a client answers with
`sessions.respond`. The node then returns the decision as
`hookSpecificOutput` JSON in `output`, and the command prints it. The agent
prompt stays open at the same time, and the first answer wins. If the hook
connection closes, or after 1490 seconds, the node returns an empty output.

For all other events, the command waits at most 2 seconds for the node. It
prints the output of the local adapter, not the result of the node, and
always exits with status 0.

## Method reference

In the tables, **Served by** tells which component handles the method. The
type notation uses `?` for a field that can be absent. Byte arrays (`bytes`)
are standard base64 strings in JSON.

### Connection and nodes

| Method          | Kind               | Served by        | Params                  | Result                        |
| --------------- | ------------------ | ---------------- | ----------------------- | ----------------------------- |
| `ping`          | request            | all              | none                    | `null`                        |
| `node.identify` | request            | node             | none                    | `IdentifyResult`              |
| `server.info`   | request            | gateway, node    | none                    | `ServerInfo`                  |
| `nodes.list`    | request            | gateway          | none                    | `{ nodes: NodeDescriptor[] }` |
| `node.event`    | notification       | gateway → client | `NodeEvent`             | —                             |
| `relay.open`    | request            | gateway          | `{ node_id: string }`   | `{ chan_id: string }`         |
| `relay.close`   | request            | gateway          | `{ chan_id: string }`   | `null`                        |
| `e2e.handshake` | relay notification | client ↔ node    | Noise message in `body` | —                             |

```ts
IdentifyResult {
  id:               string   // stable node id; composite-id prefix
  label:            string
  version:          string
  capabilities:     { spawn_session: boolean }
  identity_pubkey?: string   // base64 Curve25519
  signer_pubkey?:   string   // base64 Ed25519 (locked mode)
  tip?:             bytes    // trust-log tip (locked mode)
}

ServerInfo {
  version:            string
  nodes:              { id, label, version, capabilities }[]
  pushPortConfigured: boolean
}

NodeDescriptor {
  id, label, version: string
  capabilities:     { spawn_session: boolean }
  identity_pubkey?: string
  signer_pubkey?:   string   // from the gateway; discovery only
  online:           boolean
}

NodeEvent {
  type: "added" | "online" | "offline" | "removed" | "trust-changed"
  node: NodeDescriptor       // empty for trust-changed
}
```

### Sessions

All methods in this table are served by the node. The node serves them on the
unix socket and on relay channels.

| Method                  | Kind         | Params                           | Result                       |
| ----------------------- | ------------ | -------------------------------- | ---------------------------- |
| `sessions.list`         | request      | none                             | `Session[]`                  |
| `sessions.refresh`      | request      | none                             | `Session[]` (after a rescan) |
| `session.event`         | notification | `{ type, session: Session }`     | —                            |
| `sessions.capture`      | request      | `SessionRef`                     | `{ screen: string }`         |
| `sessions.input`        | request      | `InputParams`                    | `null`                       |
| `sessions.key`          | request      | `{ session_id, keys: string[] }` | `null`                       |
| `sessions.respond`      | request      | `RespondParams`                  | `null`                       |
| `sessions.spawn`        | request      | `SpawnParams`                    | `{ session_id, pane_id }`    |
| `sessions.resume`       | request      | `ResumeParams`                   | `{ session_id }`             |
| `sessions.kill`         | request      | `SessionRef`                     | `null`                       |
| `sessions.focus`        | request      | `SessionRef`                     | `null`                       |
| `agents.list`           | request      | `{ node_id? }`                   | `{ agents: AgentInfo[] }`    |
| `sessions.tasks`        | request      | `SessionRef`                     | `{ tasks: Task[] }`          |
| `tasks.changed`         | notification | `{ sub_id, session_id }`         | —                            |
| `sessions.exportBundle` | request      | `ExportBundleParams`             | `{ filename, data: bytes }`  |

For a headless agent (opencode), `pane_id` in the `sessions.spawn` result is
an empty string.

```ts
SessionRef { session_id: string }

InputParams {
  session_id: string
  text:       string
  submit:     boolean   // send Enter after the text
  prepare:    boolean   // leave copy mode and vim normal mode first
}

RespondParams {
  session_id:       string
  behavior?:        "allow" | "deny"
  reason?:          string            // deny message
  answers?:         { [question: string]: string | string[] }
  question_action?: "" | "chat" | "cancel"
  set_mode?:        "acceptEdits" | "default" | "auto"
  option_value?:    string            // echo of DecisionOption.value
  kind?:            string            // ignored by the node
  option_index?:    number            // ignored by the node
  text?:            string            // ignored by the node
}

SpawnParams {
  node_id?: string   // routing only
  name?:    string   // tmux session name
  cwd?:     string
  agent?:   string   // default agent if empty
  command?: string   // overrides agent
  prompt?:  string
}

ResumeParams {
  node_id?:         string
  agent:            string
  agent_session_id: string
  cwd:              string
}

AgentInfo { id, name, color: string, spawnable: boolean }

Task {
  id, subject:  string
  description?: string
  active_form?: string
  status:       "pending" | "in_progress" | "completed"
  blocks?:      string[]
  blocked_by?:  string[]
}

ExportBundleParams {
  node_id?:        string
  agent:           string
  transcript_path: string
  metadata:        BundleMetadata   // written into the bundle manifest
}
```

### Transcripts and history

| Method                       | Kind         | Params                                                      | Result                |
| ---------------------------- | ------------ | ----------------------------------------------------------- | --------------------- |
| `sessions.transcriptView`    | request      | `SessionRef`                                                | `{ chunks: Chunk[] }` |
| `sessions.toolDetail`        | request      | `{ session_id, agent_id?, tool_id }`                        | `ToolDetail`          |
| `transcript.subscribe`       | request      | `TranscriptSubscribeParams`                                 | `TranscriptDelta`     |
| `transcript.unsubscribe`     | request      | `{ sub_id }`                                                | `null`                |
| `transcript.delta`           | notification | `TranscriptDelta`                                           | —                     |
| `sessions.historyProjects`   | request      | none                                                        | `HistoryProject[]`    |
| `sessions.historySessions`   | request      | `HistorySessionsParams`                                     | `HistorySessionPage`  |
| `sessions.historyTranscript` | request      | `{ node_id?, agent?, transcript_path, agent_id? }`          | `{ chunks: Chunk[] }` |
| `sessions.historyToolDetail` | request      | `{ node_id?, agent?, transcript_path, agent_id?, tool_id }` | `ToolDetail`          |

`agent_id` selects a subagent trace. History methods use the transcript path on
the node, because past sessions have no live session id.

```ts
TranscriptSubscribeParams {
  sub_id:      string
  session_id:  string
  agent_id?:   string
  have_chunks: number
}

TranscriptDelta { sub_id: string, from_index: number, chunks: Chunk[] }

ToolDetail { toolInput?: string, result?: string, resultIsError?: boolean }

HistorySessionsParams {
  node_id?:    string
  project_dir: string
  limit?:      number   // 0 or less returns all from offset
  offset?:     number
}
```

### Terminals

| Method            | Kind         | Params                                              | Result |
| ----------------- | ------------ | --------------------------------------------------- | ------ |
| `terminal.open`   | request      | `{ term_id, session_id, cols, rows, client_pane? }` | `null` |
| `terminal.input`  | request      | `{ term_id, data }`                                 | `null` |
| `terminal.resize` | request      | `{ term_id, cols, rows }`                           | `null` |
| `terminal.close`  | request      | `{ term_id }`                                       | `null` |
| `terminal.output` | notification | `{ term_id, data }`                                 | —      |
| `terminal.exited` | notification | `{ term_id, reason? }`                              | —      |

### Changes and commits

| Method                  | Params                                   | Result               |
| ----------------------- | ---------------------------------------- | -------------------- |
| `sessions.changedFiles` | `SessionRef`                             | `ChangedFilesResult` |
| `sessions.fileDiff`     | `{ session_id, path, orig_path?, rev? }` | `FileDiffResult`     |
| `sessions.commits`      | `SessionRef`                             | `CommitsResult`      |
| `sessions.commitFiles`  | `{ session_id, sha }`                    | `ChangedFilesResult` |

```ts
ChangedFilesResult {
  root?: string
  files: {
    path:       string
    orig_path?: string
    change:     "added" | "modified" | "deleted" | "renamed" | "untracked"
    staged:     boolean
    unstaged?:  boolean
  }[]
}

FileDiffResult {
  path:         string
  old_content?: string   // HEAD, or the side before rev
  new_content?: string   // working tree, or the side after rev
  not_shown?:   boolean  // binary or too large
}

CommitsResult {
  commits:   { sha, short, subject, author: string, unix_sec: number }[]
  unpushed?: boolean
}
```

### Projects and workspaces

| Method                   | Params                            | Result                                |
| ------------------------ | --------------------------------- | ------------------------------------- |
| `project.list`           | none                              | `{ projects: ProjectNode[] }`         |
| `project.changed`        | notification, empty params        | —                                     |
| `project.rename`         | `{ project_id, name }`            | `null`                                |
| `project.setHidden`      | `{ project_id, value }`           | `null`                                |
| `project.setPinned`      | `{ project_id, value }`           | `null`                                |
| `project.forget`         | `{ project_id }`                  | `null`                                |
| `project.branches`       | `{ project_id }`                  | `{ branches: BranchInfo[] }`          |
| `project.prs`            | `{ project_id }`                  | `{ prs: PRInfo[], truncated? }`       |
| `project.issues`         | `{ project_id }`                  | `{ issues: IssueInfo[], truncated? }` |
| `workspace.create`       | `WorkspaceCreateParams`           | `WorkspaceCreateResult`               |
| `workspace.remove`       | `{ workspace_id, force? }`        | `{ warning? }`                        |
| `workspace.setTarget`    | `{ workspace_id, target_branch }` | `null`                                |
| `workspace.runSetup`     | `WorkspaceRef`                    | `null`                                |
| `workspace.setupLog`     | `WorkspaceRef`                    | `{ output: string }`                  |
| `workspace.changedFiles` | `WorkspaceRef`                    | `ChangedFilesResult`                  |
| `workspace.diff`         | `WorkspaceFileParams`             | `{ path, diff?, not_shown? }`         |
| `workspace.listDir`      | `WorkspaceFileParams`             | `ListDirResult`                       |
| `workspace.readFile`     | `WorkspaceFileParams`             | `{ path, content?, not_shown? }`      |
| `workspace.commits`      | `WorkspaceRef`                    | `CommitsResult`                       |
| `workspace.commitFiles`  | `{ workspace_id, sha }`           | `ChangedFilesResult`                  |

See [Projects and workspaces](#projects-and-workspaces) for the rules of each
method.

```ts
ProjectNode {
  id, name:        string
  kind:            "git" | "plain"
  dir:             string
  root?:           string   // empty for a bare repository
  default_branch?: string
  is_gone?, hidden?, pinned?: boolean
  error?:          string   // git failed; workspaces are the last known
  created_at?, last_seen_at?: string   // RFC 3339
  scripts?:        { setup?: string, teardown?: string }
  workspaces:      WorkspaceNode[]
  node_id?, node_label?: string        // added by the client
}

WorkspaceNode {
  id, dir:        string
  is_main?, is_gone?: boolean
  branch?:        string   // empty for a detached HEAD
  head?:          string
  target_branch?: string
  created_at?, last_seen_at?: string
  setup?: {
    state:        "running" | "ok" | "failed"
    command:      string
    exit_code?:   number
    started_at?, ended_at?: string
    output_tail?: string
  }
}

WorkspaceRef {
  workspace_id: string
  against?:     "" | "target"   // workspace.changedFiles only
}

WorkspaceFileParams {
  workspace_id: string
  path:         string          // repository-relative; "" is the root for listDir
  against?:     "" | "target"   // diff only
  orig_path?:   string          // diff only
  rev?:         string          // diff only; a commit sha
}

WorkspaceCreateParams {
  project_id:     string
  source?:        "new" | "branch" | "pr" | "issue"   // default "new"
  branch?:        string
  number?:        number   // pr, issue
  target_branch?: string
}

WorkspaceCreateResult {
  workspace_id, dir: string
  warning?: string
  prompt?:  string   // issue text, for an agent spawn
  setup?:   string   // setup command that started
}

ListDirResult {
  root?, path?: string
  entries: { name, path: string, is_dir?, symlink?: boolean, target?: string }[]
}

BranchInfo { name: string, remote?, local?, checked_out?: boolean }
PRInfo     { number, title, author?, head_branch, base_branch, url? }
IssueInfo  { number, title, author?, url? }
```

### Push

| Method              | Served by            | Params                         | Result                 |
| ------------------- | -------------------- | ------------------------------ | ---------------------- |
| `push.vapidKey`     | gateway              | none                           | `{ key?: string }`     |
| `push.register`     | node                 | `PushRegisterParams`           | `null`                 |
| `push.unregister`   | node                 | `{ device_id }`                | `null`                 |
| `push.test`         | node                 | `{ device_id }`                | `null`, or error `410` |
| `push.setPause`     | node                 | `{ device_id, paused_until? }` | `null`                 |
| `push.deliver`      | gateway (from nodes) | `PushDeliverParams`            | `{ gone?: boolean }`   |
| `pushport.setToken` | gateway (admin)      | `{ token }`                    | `null`                 |

```ts
PushRegisterParams {
  device_id:     string   // stable key; a new register replaces the old one
  endpoint:      string
  p256dh?:       string   // base64url, no padding
  auth?:         string   // base64url, no padding
  paused_until?: string   // RFC 3339; empty means enabled
}

PushDeliverParams {
  endpoint:   string
  ciphertext: string   // standard base64
  ttl?:       string
  urgency?:   string
}
```

### Client tokens

All methods in this table are served by the gateway and need an admin
connection.

| Method              | Params      | Result                                  |
| ------------------- | ----------- | --------------------------------------- |
| `clients.pairStart` | none        | `{ token, url }`                        |
| `clients.pairAwait` | `{ token }` | `{ connected: boolean }`                |
| `clients.list`      | none        | `{ token, created_at }[]`, newest first |
| `clients.remove`    | `{ token }` | `null`                                  |

In `clients.list` and `clients.remove`, `token` is the SHA-256 id of the
token.

### Trust log distribution

| Method             | Kind         | Direction                | Params                            | Result                                             |
| ------------------ | ------------ | ------------------------ | --------------------------------- | -------------------------------------------------- |
| `trustlog.sync`    | request      | node or client → gateway | `{ known?: bytes[], truncated? }` | `{ entries?: bytes[], want?: bytes[], disjoint? }` |
| `trustlog.push`    | request      | node → gateway           | `{ entries?: bytes[] }`           | `null`                                             |
| `trustlog.changed` | notification | gateway → node           | `{ heads?: bytes[] }`             | —                                                  |

Each item in `entries` is one entry in wire form. Entries are in order, parents
first.

### Locked mode control

These methods are served on the unix socket only. On any other path, the node
returns `-32601`.

| Method                    | Params                                      | Result                                        |
| ------------------------- | ------------------------------------------- | --------------------------------------------- |
| `lock.init`               | `{ signers?, devices?, gen_disablements? }` | `{ tip, signer_count, disablement_secrets? }` |
| `lock.sign`               | `{ device }`                                | `{ tip, changed }`                            |
| `lock.revoke`             | `{ device }`                                | `{ tip, changed }`                            |
| `lock.addSigner`          | `{ signer }`                                | `{ tip, changed }`                            |
| `lock.removeSigner`       | `{ signer }`                                | `{ tip, changed }`                            |
| `lock.disable`            | `{ secret }`                                | `{ tip, disabled }`                           |
| `lock.pin`                | `{ genesis }`                               | `null`                                        |
| `lock.unpin`              | none                                        | `null`                                        |
| `lock.localDisable`       | none                                        | `null`                                        |
| `lock.status`             | none                                        | `LockStatusResult`                            |
| `lock.log`                | none                                        | `{ entries: LockLogEntry[], tip?, signers? }` |
| `lock.revokeSignerStart`  | `{ revoked, replaces?, fork_from? }`        | `{ blob }`                                    |
| `lock.revokeSignerCosign` | `{ blob }`                                  | `{ blob }`                                    |
| `lock.revokeSignerFinish` | `{ blob }`                                  | `{ tip }`                                     |

- `gen_disablements` is a number: the count of disablement secrets to create.
  All other keys, hashes, secrets, and blobs are `bytes`.
- `lock.init` needs the signer key of this node in `signers`. Signer and device
  keys must be 32 bytes. It returns the disablement secrets one time only.
- `changed` is `false` if the device or signer was already in the requested
  state.
- `lock.disable` does not need a trusted signer, but the node must have a
  signer key.

A revoke-signer blob is the wire form of a `revoke-signer` entry. It holds the
fork point, the revoked keys, the replacements, and the co-signs so far.
`lock.revokeSignerStart` adds the co-sign of this node. Each other signer adds
a co-sign with `lock.revokeSignerCosign`. `lock.revokeSignerFinish` needs
enough co-signs and appends the entry unchanged.

```ts
LockStatusResult {
  enabled:          boolean
  tip?:             bytes
  signers?:         bytes[]
  device_count:     number
  devices?:         bytes[]
  length?:          number
  signer_trusted:   boolean
  authorized:       boolean
  signer_pubkey?:   bytes
  identity_pubkey?: bytes
  disabled?, local_disabled?, pinned?, quarantined?: boolean
  pin_genesis?:     bytes
  seen_genesis?:    bytes    // genesis that caused the quarantine
  pin_source?:      "config" | "file"
}

LockLogEntry {
  index:         number
  kind:          "genesis" | "add-signer" | "remove-signer" | "authorize-device"
                 | "revoke-device" | "revoke-signer" | "disable"
  hash?:         bytes
  target?:       bytes
  signers?:      bytes[]   // genesis
  revoked?:      bytes[]   // revoke-signer
  replaces?:     bytes[]   // revoke-signer
  cosign_count?: number    // revoke-signer
}
```

### Hooks

| Method       | Served by | Params      | Result                |
| ------------ | --------- | ----------- | --------------------- |
| `hook.event` | node      | `HookEvent` | `{ output?: string }` |

The `argus hook` command uses the unix socket. The node also serves
`hook.event` on relay channels. See [Hooks](#hooks).

## Data types

Session and API types use `snake_case` field names. Transcript types use
`camelCase` field names. `ServerInfo.pushPortConfigured` and the fields of
`ToolDetail` are also `camelCase`.

### Session

```ts
Session {
  id:                 string
  agent:              string          // for example "claude"
  agent_session_id?:  string
  name?:              string
  tmux: {
    server:       "default" | "argus"
    pane_id:      string              // for example "%3"
    session_name: string
    window_index: number
    current_path: string
  }
  cwd?:               string
  transcript_path?:   string
  status:             "discovered" | "starting" | "working" | "awaiting_input" | "idle" | "dead"
  status_label?:      string          // "awaiting" for awaiting_input, else the status
  source:             "discovered" | "spawned" | "hooked"
  frontend?:          "tmux" | "vscode" | "external"
  input_mode?:        "pane" | "api"
  can_open_terminal?: boolean
  repo?:              string
  branch?:            string          // short sha for a detached HEAD
  workspace_id?:      string
  summary?:           Summary
  interaction?:       Interaction     // present only while the session waits for input
  node_id?, node_label?: string       // added by the client
  offline?:           boolean         // added by the client
}

Summary {
  model_name?, model_color?: string
  has_context?:       boolean
  context_pct?:       number          // 0 to 100
  tokens?:            number
  task?:              string
  last_activity?:     string          // RFC 3339
  task_session_keys?: string          // space-separated session UUIDs
}

Interaction {
  kind:        "permission" | "question" | "plan" | "idle"
  message?:    string
  tool_name?:  string
  tool_input?: string
  questions?:  {
    header?, question?: string
    multi_select?:        boolean
    options?:             string[]
    option_descriptions?: string[]    // same order as options
    option_previews?:     string[]    // same order as options
  }[]
  plan?:       string
  options?:    { label, value: string, reject?: boolean, placeholder?: string }[]
}
```

### History

```ts
HistoryProject {
  project_dir, cwd: string
  repo?:           string
  label:           string
  session_count:   number
  last_activity:   string
  node_id?, node_label?: string
}

HistorySessionPage {
  items: {
    session_id:      string
    agent?:          string
    resumable?:      boolean
    title?, first_message?: string
    transcript_path: string
    model_name?, model_color?: string
    last_activity:   string
    tokens?, turn_count?, duration_ms?: number
    node_id?, node_label?: string
  }[]
  has_more: boolean
}
```

### Transcript

```ts
Chunk {
  id:                  string
  kind:                "user" | "ai" | "system" | "compact" | "shell"
  timestamp?, text?:   string
  modelName?, modelColor?: string
  items?:              Item[]
  thinking?, toolCount?: number
  usage?:              { input?, output?, cacheRead?, cacheCreation?: number }
  stopReason?:         string
  durationMs?:         number
  interrupted?:        boolean
  hasContext?:         boolean
  contextPct?, contextFirstPct?: number
  contextDeltaTokens?: number
  summary?, label?, detail?: string
  isError?:            boolean
  previewItemId?:      string         // the item to show as the chunk preview
}

Item {
  id:             string
  kind:           "thinking" | "text" | "tool" | "subagent" | "skill"
  text?:          string
  signature?:     boolean
  toolName?, toolId?: string
  inputPreview?:  string
  resultIsError?: boolean
  subagents?: {
    id:     string
    name?, type?, desc?, status?, color?: string
    isTeammate?, idle?, hasTrace?: boolean
    trace?: Chunk[]                   // history only
  }[]
}
```

`Item` never carries `toolInput` or `result` on the wire. Use
`sessions.toolDetail` or `sessions.historyToolDetail` to get them.

### Bundle metadata

```ts
BundleMetadata {
  title?, model_name?, model_color?, cwd?, repo?: string
  git_user_name?, git_user_email?, first_message?: string
  tokens?, turn_count?, duration_ms?: number
  last_activity?: string
}
```
