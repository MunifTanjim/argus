# Locked Mode

Locked mode lets you cryptographically control which devices and nodes can connect
to your Argus network. Once enabled, every connection is gated by a trust log — a
signed append-only chain that records who is allowed. The gateway never sees or
validates the chain contents; it relays the bytes opaquely so only your nodes and
clients can read and verify them.

## Quickstart

```sh
# on the node that will hold the first signer key (list every signer, this one included).
# without --confirm this only prints what it would create:
argus lock init sigpub:<this-node> [sigpub:<other-node>...]
argus lock init --confirm sigpub:<this-node> [sigpub:<other-node>...]

# check the state of this node:
argus lock status

# authorize another device (node label, or a devpub: key):
argus lock sign <device>

# view the full trust-log history:
argus lock log
```

## Key formats

Everything locked mode asks you to copy between machines is 32 bytes, so each kind
carries a prefix that says what it is:

| Prefix | What it is | Where it comes from |
| --- | --- | --- |
| `sigpub:` | signer key | `argus lock status` on the signing node |
| `devpub:` | device (identity) key | `argus lock status` on that device |
| `gen:` | genesis hash — the trust root | `argus lock init`, or `argus lock pin` |
| `dis:` | disablement secret | `argus lock init`, shown once |
| `tip:` | current chain tip, for audit | `argus lock status` / `argus lock log` |

Commands reject a value of the wrong kind by name rather than accepting it because
the length matches, so pasting a genesis hash where a device key belongs fails
instead of silently authorizing something that does not exist:

```
$ argus lock sign gen:4f3a9c1e...
error: device "gen:4f3a9c1e..." : expected a device key (devpub:), got a genesis hash (gen:)
```

Device commands (`lock sign`, `lock revoke-device`) take either a node label/id or a
`devpub:` key. Every **signer** input — `lock init`, `lock add-signer`,
`lock remove-signer`, `lock revoke-signer` and its `--replacement` — takes **only** a
`sigpub:` key. A label can only become a key by way of the roster the **gateway** serves. At init
that lets a hostile gateway seat its own key in the genesis; afterwards it lets one
decide which key you actually added, removed or revoked — revoking "node-b" by name
could leave the compromised key in place and remove an honest one instead. A key read
off `argus lock status` on the node itself never passes through the gateway.

`lock init` takes the signer keys as positional arguments, and they are the
**complete** set the new log will trust — including the key of the node you run it
on, which must be listed like any other. Nothing is added implicitly, so the command
you ran is a full record of who can sign in the network it created. `lock init` also does nothing until you pass `--confirm`. On its own it prints the
signer set, the number of disablement secrets, and the devices it would authorize
from the gateway's roster — then exits having created nothing. Read that list before
confirming: those identity keys come from the gateway, and at this point nothing has
verified them.

Omit your own key and the command refuses, printing it for you to paste back:

```
$ argus lock init sigpub:8b2d7e05...
error: this node's own signer key must be listed explicitly:
  argus lock init sigpub:4f3a9c1e... sigpub:8b2d7e05...
```

## Key concepts

**Signer key** — each node generates an Ed25519 key (`~/.config/argus/signer.json`
or the path configured by `signer_path`). The trust log lists which signer pubkeys
are trusted; entries must be signed by a currently trusted key to be accepted.

**Identity key** — a Curve25519 Noise static key (`client-identity.json`) used to
open E2E channels. A device must be authorized (its identity pubkey listed in the
trust log) to pass the locked-mode gate.

**Disablement secret** — a random break-glass credential generated at `lock init`.
Presenting the secret disables locked mode network-wide without requiring any signer
key. Store it securely and offline. It does **not** release a quarantined device: a
device with no pin cannot tell whether the chain carrying the disable entry is the
real one, so it keeps refusing channels. Recover a quarantined node with
`argus lock pin` or `argus lock local-disable`, not with `lock disable`.

A disabled log is terminal — it can never be re-enabled. To lock the network again,
run `argus lock init` once more: it creates a **new** genesis over the disabled one.
That is a new trust root, so every device pinned to the old one quarantines as soon
as it sees the new root — the same fail-closed behaviour as a device that has never
been pinned — and recovers with a single `argus lock pin`. A pin to a disabled chain
is stale rather than conflicting: it protects nothing, because the chain it names
authorizes nobody and can never be re-enabled, so `lock pin` replaces it without an
`unpin`. The machine you run `lock init` on repins both of its roles automatically.
A device pinned by `lock.genesis` in its config is never repinned for you — edit the
config there.

Client (TUI) identities are **not** carried into the new genesis: `lock init` only
authorizes the node identities on the gateway roster. Re-authorize each client with
`argus lock sign devpub:<identity>` on a signer node, exactly as you did the first
time. Carrying them forward would re-authorize anything a compromised signer had
authorized in the old chain.

## Pinning the genesis

The **genesis pin** is a 32-byte hash that tells a device which trust log it belongs to. Without it, a device on a locked network has no way to know which chain is authoritative and will refuse all E2E channels — a deliberate fail-closed posture.

`lock init` pins both roles on the machine it runs on: the node (which created the genesis) and that machine's TUI client, which is a separate role with its own pin file. Every other device — additional nodes and TUI clients — must be pinned separately.

### Pinning a new device

Run on the device you want to pin:

```sh
argus lock pin
```

The command connects to the gateway, reads the offered trust-log branches, and shows you the genesis **word fingerprint** — a short sequence of human-readable words derived from the genesis hash:

```
genesis offered by this network:
  gen:4f3a9c1e2d7b06a5f81c3e94d2b0a7c6e5194f8d3a2c7b60e91d48a2e5c3b7d1
  [chisel cobra drumbeat eyeglass hamlet island keyboard mural]

Compare these words against `argus lock status` on a node you trust.
Pin this device to it? [y/N]:
```

Before typing `y`, compare those words against the fingerprint shown by `argus lock status` (the `pin:` line) on a node you already trust — over the phone, in a chat, or any out-of-band channel. Matching words confirm you are pinning to the same trust root that node is already enforcing. Note that `pin:` and `trust fingerprint:` are different hashes: the genesis and the current signer set. Compare `pin:` against `pin:`.

If you already know the genesis (for example, from `lock init` output), you can pin without a prompt:

```sh
argus lock pin gen:<hex>
```

An unpinned device on a locked network enters **quarantine**: it can see the roster and the offered genesis, but refuses all E2E channels until pinned. A hostile gateway can put an unpinned device into this state by offering a fabricated genesis chain — which is precisely why comparing the fingerprint out-of-band before accepting is what makes the adoption trustworthy.

`argus lock status` opens with one of three states:

| Headline | Meaning |
|---|---|
| `locked mode: not enabled` | this device has no trust log |
| `locked mode: enforcing` | normal locked operation |
| `locked mode: disabled network-wide` | break-glass was used; nothing is enforced, permanently |

It reports quarantine for both roles on the machine it runs on:

- `pin: none — QUARANTINED (chain seen: …)` is the **node** on this machine.
- `client pin: none — QUARANTINED (chain seen: …)` is this machine's **TUI client**.
- `pin: … — SUPERSEDED: the network now uses …` is a device whose own root was
  disabled while the network moved to a new one. It refuses all channels until
  `argus lock pin`.

The two are pinned independently, so one can be quarantined while the other is not.

Pinning recovers a quarantined node live over its local socket — no restart. A quarantined **client** is different: the running TUI process read its pin at startup, so after `argus lock pin` you must restart `argus` for the dashboard to come back. The client has no `local-disable` equivalent either; `lock pin` plus a restart is its only escape.

### What quarantine does *not* prove

Quarantine triggers when a device sees a chain it cannot verify. Detection therefore depends on the gateway actually relaying a chain to that device. A malicious gateway can simply serve no chain to an unpinned device, and that device stays open — it never learns the network is locked. **A device that is not quarantined is not evidence that the network is unlocked.** The only positive assurance is a pin you placed yourself after comparing the fingerprint out-of-band. Pin every device; do not treat "no quarantine warning" as an all-clear.

### Unpinning a device

```sh
argus lock unpin
```

This clears the pin and removes the local chain file (a chain from the old genesis can never be used again). A node that had actually synced the chain quarantines immediately — it does not stay open until the next sync tick — and its live channels are dropped, so the documented `unpin` + `pin` rotation never opens a window where any key the gateway introduces is accepted.

`unpin` and `lock local-disable` are independent: neither reads or writes the other's state. Unpinning never lifts a local-disable, and a local-disable never drops the pin.

On a device pinned by `lock.genesis` in its config, `lock pin` refuses to write a different genesis: the config outranks the pin file, so remove `lock.genesis` from the config first.

`lock unpin` is allowed there, and clears only the persisted pin — the device stays pinned to `lock.genesis` and keeps enforcing. The command says so explicitly instead of claiming the device has no trust root.

### Recovering from a genesis pin conflict

If `lock.genesis` in the config and the persisted pin file name **different** genesis hashes, `argus` refuses to start:

```
genesis pin conflict: lock.genesis is gen:<X> but <path> holds gen:<Y>; run `argus lock unpin` to drop the persisted pin
```

Run that command on the affected device:

```sh
argus lock unpin
```

It clears both roles' persisted pins — the client's directly, and the node's over its socket, or from disk when the node cannot start. The device is left pinned to `lock.genesis` alone, so it never comes back open. If it was `lock.genesis` that was wrong, remove it from the config instead and re-run `argus lock pin`.

## Anti-equivocation: signed HEAD beacons

A malicious or compromised gateway could attempt to show different nodes or clients
different branches of the trust log — a "split-view" or equivocation attack. Argus
detects this at multiple layers:

1. **Signed HEAD beacons.** Every node holds a dedicated Ed25519 beacon key
   (separate from its signer and Noise keys). On each tip change and on reconnect
   the node emits a beacon: `{beaconPub, tip, length, counter, sig}` signed by that
   key. The counter is monotonic so replayed beacons are ignored.

2. **Blind gateway relay.** The gateway forwards beacons on the roster/node.event
   stream verbatim — it never verifies them (it can't: the keys are Ed25519 and
   opaque to the blind gateway). A compromised gateway can drop beacons but cannot
   forge them.

3. **Client cross-check.** The E2E client collects each node's beacon and on every
   trust-log sync tick verifies that all nodes sit on one linear history. A tip on a
   branch that can't be reconciled after a pull is flagged as equivocation.

4. **Client-as-courier.** The client also couriers each node's signed beacon to the
   other nodes over E2E channels. A receiving node verifies the beacon's signature
   against the roster-announced `beacon_pubkey`, counter-guards against replay, and
   consistency-checks the peer's tip against its own chain. A malicious client can
   withhold beacons but cannot forge them (Ed25519 + roster-pinned pubkey).

Detection response is warn-and-surface: an `equivocation` flag is set on the node
and returned in `lock status`. The flag is never cleared for the lifetime of the
node process. Fork-choice already prevents adopting a bad branch; this layer exposes
a gateway that is hiding branches.

## The word-fingerprint backstop

`argus lock status` prints a **trust fingerprint** — a short sequence of English
words derived from the current signer set:

```
trust fingerprint: [sawdust scenic seabird select shadow skydive solo sugar]
```

If you suspect equivocation, compare this fingerprint across all your nodes
out-of-band (phone call, chat, or another trusted channel). Matching fingerprints on
all nodes means they all see the same trust log. A mismatch, or an `⚠ equivocation
detected` warning in the status output, means the gateway may be showing split views
and the gateway operator should be investigated.

## Signer revocation

If a signer key is compromised you can revoke it via a co-signing ceremony that
requires the remaining trusted signers to out-vote the compromised one:

```sh
# start (on the initiating signer node):
argus lock revoke-signer sigpub:<compromised> --replacement sigpub:<successor>

# co-sign (on another signer node):
argus lock revoke-signer --cosign <blob>

# finalize (once quorum is reached):
argus lock revoke-signer --finish <blob>
```

The ceremony forks the chain from the point just before the revoked signer's
earliest action, erasing entries it signed. You need at least 3 signers to out-vote
one; with fewer, use `argus lock disable <secret>` + reinit as the recovery path.
