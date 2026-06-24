# Mobile App

A companion **Android app** (in [`app/`](https://github.com/MunifTanjim/argus/tree/main/app))
mirrors the TUI — sessions, status, transcripts, live screen, prompts,
history — and adds **Push Notification**: it pings you the moment a session needs
you (a prompt, a question, a finished turn) **even when backgrounded or killed**.

## Pairing

The app talks to a **[gateway](/guide/multi-machine)** over the network, so you
need one running — even on a single machine, start the node in gateway mode
(`argus start --token <TOKEN>`) rather than a plain local node, which only listens
on a unix socket the phone can't reach. Then expose the gateway — a
[tunnel](/guide/gateway-tunnel) is easiest — and pair the device.

Pairing mints a **per-device token** so each phone connects with its own revocable
credential instead of the master token. Run `argus pair` against the gateway:

```sh
argus pair --gateway wss://gateway.argus --token <TOKEN>
```

Argus asks the gateway for a fresh token, prints a **QR code**, and waits for the
device to connect.

Scan the QR in the app. The token is persisted on the gateway — and thus revocable
— only after a device actually connects.

Revoke a device with `argus unpair`:

```sh
argus unpair --gateway wss://gateway.argus --token <TOKEN>
```

## Push Notification

The app pings you the moment a session needs you — **even when backgrounded or
killed**. Nothing to configure; push is always on.

Push runs over **UnifiedPush**, so it works on any Android:

- **Google Play devices** — works out of the box, no setup.
- **De-Googled devices** — install any UnifiedPush distributor (e.g.
  [ntfy](https://ntfy.sh)), then select it in **Settings → Push notifications**.
