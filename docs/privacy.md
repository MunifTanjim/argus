---
sidebar: false
aside: false
editLink: false
---

# Privacy Policy

**Effective date:** 2026-06-25

Argus is a self-hosted tool for watching and controlling your AI coding sessions. It runs on **your own machines** — there are no Argus-operated servers, no user accounts, and no tracking. Your session data, transcripts, and control traffic stay on hardware you control.

This policy explains what the Argus mobile app and the self-hosted Argus software handle.

## Who this covers

- **The Argus mobile app** (Android/iOS).
- **The Argus gateway and node software** that you run yourself on your own machines.

The developer of Argus does not operate any service that receives your data.

## Data the app stores on your device

When you pair the app with your gateway, the app stores the following in your device's secure storage (the protected area provided by Android and iOS):

- **Gateway URL** — the address of your own gateway.
- **Access token** — a private key used to sign in to your gateway.
- **Device ID** — a random identifier created once on the device.

This data never leaves your device except to communicate with the gateway you configured. It is not sent to the developer.

## Data sent to your own gateway

To receive push notifications, the app registers with your gateway. Your gateway stores, on the machine you run it on:

- A push **subscription** — where to send notifications, and the keys needed to encrypt them.
- A scrambled form of the **device ID** and timestamps (when it was added and last seen).

This information lives on your own machine. You can revoke it at any time by running `argus unpair`, which deletes the device record and invalidates its token.

## Push notifications

Push notifications use the open **UnifiedPush / Web Push** standard. Each notification is **encrypted by your gateway before it leaves** — only your device can read it.

Delivery happens through a **distributor** of your choosing:

- An external distributor app such as **ntfy** or **Sunup**, or
- An **embedded FCM** relay, used only if no external distributor is installed.

Distributors (including FCM) only relay the **encrypted, opaque payload** — they cannot read its contents. Notification metadata that the app processes locally includes things like the session or repository name and the type of interaction needing your attention.

## No analytics or tracking

The Argus app contains **no analytics, no crash reporting, no advertising, no location tracking, and no device fingerprinting**. No usage data is collected or transmitted to the developer or any third party.

## Third-party services

Argus integrates with third-party services **only when you choose to enable them**, and they only ever see encrypted traffic:

- **UnifiedPush distributor** (e.g. ntfy, Sunup) — relays encrypted push payloads.
- **Embedded FCM** — relays encrypted push payloads when no other distributor is present.
- **Cloudflare Tunnel** or another tunnel/proxy — if you expose your gateway beyond your local network, it carries only TLS-encrypted traffic.

Any data handled by these services is governed by their own privacy policies.

## Data retention and deletion

- **Uninstalling the app** removes all data it stored on your device.
- **`argus unpair`** revokes a device's token and deletes its record on your gateway.
- All storage is on machines you control, so you decide what is kept and for how long.

## Children's privacy

Argus is not directed at children and does not knowingly collect data from children.

## Changes to this policy

This policy may be updated from time to time. The effective date above reflects the most recent change.

## Contact

Questions about this policy can be raised at the project repository: <https://github.com/MunifTanjim/argus/issues>.
