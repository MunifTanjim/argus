# End-to-End Encryption

End-to-end encryption (E2EE) gives each client-to-node link its own encrypted
channel. The gateway only relays the sealed traffic and can never read it, so it
is safe to run one on an untrusted or internet-facing host. E2EE is off by default.

## Enable

Set `e2ee.enabled: true` on the gateway, every node, and every client. All three
must agree, or the link stays unencrypted.

```yaml
# ~/.config/argus/config.yaml
e2ee:
  enabled: true
```

The same setting is available as `ARGUS_E2EE_ENABLED=true`. A node with E2EE on
refuses to start if it cannot load its encryption key. It never falls back to
plaintext.

## Two modes

Both modes encrypt every link. They differ in whether Argus also checks _which_
devices may connect.

| Mode        | What it does                                                                      | Enable                                           |
| ----------- | --------------------------------------------------------------------------------- | ------------------------------------------------ |
| TOFU / open | Encrypts every link. Trusts any device the first time it connects. No allow-list. | `e2ee.enabled: true`                             |
| Locked      | Encrypts every link, and only lets approved devices and nodes connect.            | above, plus `argus lock init` and pinned devices |

TOFU (trust on first use) is the default once E2EE is on. Locked mode adds the
allow-list, covered below.

## Locked mode

In locked mode, only the devices and nodes you approve can connect. Approvals live
in a **trust log**: a signed, append-only record that the gateway relays but cannot
read or change.

### Set up

```sh
# create the trust log. list every signer key, including this node's own.
# runs a preview until you pass --confirm:
argus lock init --confirm sigpub:<this-node> [sigpub:<other-node>...]

# authorize a device (node label, or a devpub: key):
argus lock sign <device>

# inspect state and history:
argus lock status
argus lock log
```

`lock init` needs your own signer key in the list and refuses without it. Run it
without `--confirm` first to preview the signer set and the devices it would
authorize.

### Key Formats

Every value you copy between machines is 32 bytes with a prefix that names its kind:

| Prefix    | What it is                    | Where it comes from                     |
| --------- | ----------------------------- | --------------------------------------- |
| `sigpub:` | signer key                    | `argus lock status` on the signing node |
| `devpub:` | device (identity) key         | `argus lock status` on that device      |
| `gen:`    | genesis hash (the trust root) | `argus lock init`, or `argus lock pin`  |
| `dis:`    | disablement secret            | `argus lock init`, shown once           |
| `tip:`    | current chain tip, for audit  | `argus lock status` / `argus lock log`  |

Copy keys from `argus lock status` on the machine itself, never from a label the
gateway resolves. A hostile gateway can swap the key behind a label and authorize
or revoke the wrong one.

### Pin Devices

A **pin** ties a device to one trust log. Pin every device you rely on.

- Run `argus lock pin` on the device. It shows a 24-word fingerprint of the genesis
  the gateway is offering.
- Compare those words against the `pin:` line of `argus lock status` on a node already
  pinned to the right log. A full match proves the gateway offered the real genesis,
  not a fabricated one. Confirm only then.
- Already know the genesis? `argus lock pin gen:<hex>` skips the prompt.
- `lock.genesis:` in the config pins at boot, outranks the pin file, and is the only
  pin that holds without the gateway's cooperation.
- `lock init` auto-pins the machine it runs on (its node and its TUI). Pin every
  other device yourself.

### Recover

- **Quarantine.** A device that sees a locked network but has no matching pin refuses
  all channels. Recover it with `argus lock pin`. A node recovers live; a TUI client
  needs an `argus` restart.
- **Break-glass.** `argus lock disable <secret>` turns off locked mode network-wide.
  The secret is shown once at `lock init`; store it offline. It does not release a
  quarantined device, so use `argus lock pin` or `argus lock local-disable` for that.
- **Relock.** A disabled log is permanent. Run `argus lock init` again for a fresh
  trust log, re-pin every device with `argus lock pin`, and re-authorize each client
  with `argus lock sign devpub:<identity>`.
- **Unpin.** `argus lock unpin` drops the pin and the local chain. A synced node
  quarantines at once and its live channels close.
- **Pin conflict.** If config `lock.genesis` and the pin file name different roots,
  `argus` refuses to start. Run `argus lock unpin`, or fix `lock.genesis`.

### Detect a Bad Gateway

A compromised gateway cannot read or forge your traffic, but it can hide or split
the trust log.

- **No quarantine is not an all-clear.** A gateway can withhold the chain so a device
  never learns the network is locked. Only a pin you placed yourself proves anything.
  Pin every device.
- **Split view.** A gateway may show different nodes different branches of the chain.
  Argus reads each node's tip over the authenticated channel and flags a mismatch as
  `⚠ equivocation detected` in `lock status`. You can also compare the `tip:`
  fingerprint by reading `lock status` directly on each node; matching words mean
  they share one chain.

### Revoke a Signer

If a signer key is compromised, revoke it with a co-signing ceremony that out-votes
the compromised signer. This needs at least 3 signers; with fewer, use break-glass
plus relock instead.

```sh
# start, on a signer node:
argus lock revoke-signer sigpub:<compromised> --replacement sigpub:<successor>

# co-sign, on another signer node:
argus lock revoke-signer --cosign <blob>

# finalize once quorum is reached:
argus lock revoke-signer --finish <blob>
```

- The chain forks just before the compromised signer's first action. Every entry
  after that point is erased and must be recreated, including honest ones.
- Only signers trusted at the fork point can take part. To include a newer signer,
  pick a later fork point with `--fork-from`; this preserves more of the revoked
  signer's entries.
