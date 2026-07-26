# Locked Mode

Locked mode lets you cryptographically control which devices and nodes can connect
to your Argus network. Once enabled, every connection is gated by a trust log — a
signed append-only chain that records who is allowed. The gateway never sees or
validates the chain contents; it relays the bytes opaquely so only your nodes and
clients can read and verify them.

## Quickstart

```sh
# on the node that will hold the first signer key:
argus lock init

# check the state of this node:
argus lock status

# authorize another device (node label, or raw base64 identity pubkey):
argus lock sign <device>

# view the full trust-log history:
argus lock log
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
  <base64>
  chisel cobra drumbeat eyeglass hamlet island keyboard mural

Compare these words against `argus lock status` on a node you trust.
Pin this device to it? [y/N]:
```

Before typing `y`, compare those words against the fingerprint shown by `argus lock status` (the `pin:` line) on a node you already trust — over the phone, in a chat, or any out-of-band channel. Matching words confirm you are pinning to the same trust root that node is already enforcing. Note that `pin:` and `trust fingerprint:` are different hashes: the genesis and the current signer set. Compare `pin:` against `pin:`.

If you already know the genesis (for example, from `lock init` output), you can pin without a prompt:

```sh
argus lock pin <genesis-b64>
```

An unpinned device on a locked network enters **quarantine**: it can see the roster and the offered genesis, but refuses all E2E channels until pinned. A hostile gateway can put an unpinned device into this state by offering a fabricated genesis chain — which is precisely why comparing the fingerprint out-of-band before accepting is what makes the adoption trustworthy.

`argus lock status` reports quarantine for both roles on the machine it runs on:

- `pin: none — QUARANTINED (chain seen: …)` is the **node** on this machine.
- `client pin: none — QUARANTINED (chain seen: …)` is this machine's **TUI client**.

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
genesis pin conflict: lock.genesis is <X> but <path> holds <Y>; run `argus lock unpin` to drop the persisted pin
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
trust fingerprint: sawdust scenic seabird select shadow skydive solo sugar
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
argus lock revoke-signer <compromised-signer> --replacement <new-node>

# co-sign (on another signer node):
argus lock revoke-signer --cosign <blob>

# finalize (once quorum is reached):
argus lock revoke-signer --finish <blob>
```

The ceremony forks the chain from the point just before the revoked signer's
earliest action, erasing entries it signed. You need at least 3 signers to out-vote
one; with fewer, use `argus lock disable <secret>` + reinit as the recovery path.
