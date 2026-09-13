---
sidebar: false
aside: false
editLink: false
---

# Privacy Policy

**Effective date:** 2026-09-12

Argus is a self-hosted tool to watch and control your AI coding sessions. It
runs on your own machines. There is no Argus account, and there is no tracking.

The app talks only to a gateway you run. Push notifications need
[external services](#external-services), but they are always end-to-end
encrypted.

This policy covers two things:

- The Argus mobile app for Android and iOS.
- The `argus` software that you run on your own machines.

## Who Operates Argus

**Munif Tanjim** ("the developer") writes and publishes Argus.

You run your own nodes, your own gateway, and the app. You control the data on
them, and the developer reaches none of it.

The developer operates one service: [PushPort](#pushport). The developer
controls the data that PushPort handles.

## Data Stored on Your Device

The app keeps its state in the secure storage of the operating system (the
Keystore on Android, the Keychain on iOS): the settings, credentials, and keys
that it needs to reach your gateway and to receive notifications.

The app writes no transcript, session log, or message content to disk. Session
content stays in memory while the app runs.

## Camera

The app uses the camera for one purpose: to scan the pairing QR code that
`argus pair` prints. The scan runs on the device. The app stores no image and
sends no image.

## Data Stored on Your Machines

`argus` writes its state to files on your own machines, under the configuration,
cache, state, and runtime directories of your user account. Only your user
account can read them. Argus sends none of that state to the developer.

## Push Notifications

A notification carries a short summary of what needs your attention. Your node
encrypts it for your device before it leaves your machine, with the Web Push
standard (RFC 8291). Only your device holds the decryption key, so every relay
on the delivery path carries an opaque payload.

### Android

[UnifiedPush](https://unifiedpush.org) is the default. The app embeds an FCM
distributor and uses it unless you select a different
[distributor](https://unifiedpush.org/users/distributors/), for example ntfy or
Sunup. [PushPort](#pushport) over FCM is an alternative to UnifiedPush.

### iOS

[PushPort](#pushport) over the Apple Push Notification service (APNs) is the
only push path. A notification extension decrypts the payload on your device
before the system displays it.

### PushPort

[PushPort](https://pushport.muniftanjim.dev) is a hosted push relay that the
developer operates. Nothing reaches PushPort, from the app or from your
machines, until you register an instance token with `argus pushport register`
on your gateway.

After you register the token, the app subscribes on its next connection to that
gateway. On iOS the subscription needs no further action, because PushPort is
the only push path. On Android you must also select PushPort in the app
settings.

PushPort receives:

- The device push token that APNs or FCM issued for your device.
- The encrypted payloads, which PushPort hands to APNs or FCM with that token.
  That push service delivers them to your device.

PushPort stores none of this. It cannot read the content of a notification. The
endpoint that the app receives carries your push token in sealed form, so
PushPort keeps no record of your subscription. The endpoint expires unless the
app renews it.

## Analytics and Tracking

The app contains no analytics, no crash reporting, no advertising, no location
tracking, and no device fingerprinting. It sends no usage data to the developer
or to any third party.

The platforms report separately from the app. If you turn on sharing in the
settings of your operating system, the App Store and Google Play give the
developer aggregate crash reports and usage reports. Argus plays no part in
this, and you control the sharing in the settings of your device.

## External Services

Argus contacts an external service only when you use the feature that needs it:

- **PushPort**, operated by the developer — relays encrypted push
  payloads. See [PushPort](#pushport).
- **Apple Push Notification service (APNs)**, operated by Apple — delivers
  encrypted push payloads to iOS devices.
- **Firebase Cloud Messaging (FCM)**, operated by Google — delivers encrypted
  push payloads to Android devices.
- **A [UnifiedPush distributor](https://unifiedpush.org/users/distributors/)**,
  for example ntfy or Sunup — delivers encrypted push payloads on Android.
- **A tunnel or proxy**, for example Cloudflare Tunnel — carries traffic to your
  gateway, if you expose the gateway beyond your local network.

Push payloads stay encrypted end-to-end on every one of these paths. Some
tunnels, including Cloudflare Tunnel, terminate TLS at their edge, so the
operator can read the gateway traffic that passes through. To close that gap,
turn on [end-to-end encryption](/guide/e2ee).

Argus sends no session data to an AI provider. The coding agent that you run,
for example Claude Code, makes its own network connections. This policy does not
cover them.

The privacy policy of each third party governs the data that the third party
handles.

## Data Retention and Deletion

Argus stores your data on machines you control, so you decide what to keep and
for how long. There is no Argus account, so no account exists to delete. The
developer holds nothing to delete on your behalf.

- To stop a device from reaching your gateway, run `argus unpair`.
- To delete the push registration of a device, disconnect the gateway in the
  app.
- To erase the credentials of a gateway, remove its profile in the app.

An uninstall removes the data of the app on Android. On iOS, Keychain items can
survive an uninstall, and a reinstalled app can read them again. To erase them,
remove each profile before you uninstall.

## Your Rights

[PushPort](#pushport) is the only place where the developer processes your
data, and it holds almost nothing. It seals your push token into the endpoint
and keeps no copy. It counts requests by IP address to limit the rate, and it
ties that count to no token, no gateway, and no person. The server logs carry
no IP address.

The developer processes this data for two reasons. Delivery needs your push
token, because the notification cannot reach you without it. The rate limit
needs your IP address, because the service needs protection from abuse.

You start the processing with `argus pushport register`. If you remove the
instance token, the processing stops.

If the GDPR covers you, you have the rights of access, correction, erasure,
restriction, portability, and objection. The developer stores nothing that
identifies you, so these requests reach nothing. You can write to the address
in [Contact](#contact). You can also complain to the data protection authority
of your country.

The developer does not sell your personal information. The developer does not
share it for cross-context behavioral advertising. The developer makes no
automated decisions about you and builds no profiles.

## Legal Disclosure

A legal demand can reach the developer, because the developer operates
PushPort. [PushPort](#pushport) holds no data at rest about you, so a demand
for stored data finds nothing. The developer cannot decrypt a notification, so
a demand for content finds nothing readable.

## Children's Privacy

Argus is not directed at children. It does not knowingly collect data from
children.

## Changes to This Policy

This policy can change. The effective date above records the most recent change.
The [history of this page](https://github.com/MunifTanjim/argus/commits/main/docs/privacy.md)
records every change and its date.

## Contact

**Munif Tanjim**
Privacy questions: <ObfuscatedEmail user="privacy+argus" domain="muniftanjim.dev" />

For a general question about Argus, open an issue at
<https://github.com/MunifTanjim/argus/issues>.
