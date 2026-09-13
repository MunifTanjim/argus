# Mobile App

A companion **mobile app** for iOS and Android (in [`app/`](https://github.com/MunifTanjim/argus/tree/main/app))
mirrors the TUI — sessions, status, transcripts, live screen, prompts,
history — and adds **Push Notification**: it pings you the moment a session needs
you (a prompt, a question, a finished turn) **even when backgrounded or killed**.

<DemoVideo src="/screenshots/demo-app.mp4" alt="Argus Android app — sessions, transcript, and history in your pocket" portrait />

## Install

### Android

The app is in **Closed Testing** on Google Play — anyone can join:

1. Join the **[argus-android-app-beta](https://groups.google.com/g/argus-android-app-beta)**
   Google Group with the Google account you use on the device.
2. Open the **[testing opt-in link](https://play.google.com/apps/testing/dev.muniftanjim.argus)**
   and tap **Become a tester**.
3. Follow the link to install **Argus** from the Play Store.

Updates then arrive automatically through the Play Store like any other app.

### iOS

The iOS app is distributed through **TestFlight**. To get access, ask the owner
to add you as a tester in App Store Connect. You will receive an email invitation.

1. Install **TestFlight** from the App Store if you do not have it.
2. Open the invitation email and tap **View in TestFlight**.
3. Tap **Install** in TestFlight.

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

### Connect over SSH

Instead of exposing the gateway publicly, the app can tunnel its connection over
SSH — the same loopback-bound gateway the [CLI reaches over
SSH](/guide/multi-machine#ssh-access). In the app's **manual entry** screen,
switch to **SSH** and provide the host, user (optional — defaults to `root`),
the gateway's loopback port (default `8443`), your token, and an OpenSSH private
key (imported or app-generated). The key is stored in the device keystore; the
host's key is pinned on first connect (trust-on-first-use) and a later change is
rejected.

## Push Notification

The app pings you the moment a session needs you — **even when backgrounded or
killed**. Nothing to configure; push is always on.

**iOS** — push runs over APNs through [PushPort](/guide/pushport), on by default
in the published app. Source builds must embed a PushPort app id.

**Android** — push runs over **UnifiedPush**, so it works on any Android:

- **Google Play devices** — works out of the box, no setup.
- **De-Googled devices** — install any UnifiedPush distributor (e.g.
  [ntfy](https://ntfy.sh)), then select it in **Settings → Push notifications**.
- **PushPort** — an alternative to UnifiedPush. See the [PushPort guide](/guide/pushport).
