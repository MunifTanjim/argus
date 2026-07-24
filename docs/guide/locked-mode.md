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
key. Store it securely and offline.

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
trust fingerprint: ocean hammer lamp river
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
