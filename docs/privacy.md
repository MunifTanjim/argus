---
sidebar: false
aside: false
editLink: false
---

# Privacy Policy

**Effective date:** 2026-10-03

Argus is a self-hosted tool to watch and control your AI coding sessions. It
runs on your own machines. There is no Argus account, and there is no tracking.

The app talks only to a gateway you run. Push notifications and voice input need
[external services](#external-services). Push notifications are always
end-to-end encrypted.

This policy covers two things:

- The Argus mobile app for Android and iOS.
- The `argus` software that you run on your own machines.

## Who Operates Argus

**Munif Tanjim** ("the developer") writes and publishes Argus.

You run your own nodes, your own gateway, and the app. You control the data on
them, and the developer has no access to it.

The developer operates one service, [PushPort](#pushport), and controls the
data that it handles.

## Data Stored on Your Device

The app keeps its state in the secure storage of the operating system: the
settings, credentials, and keys that it needs to reach your gateway and to
receive notifications. If you configure voice input, the same storage keeps your
voice settings and your OpenRouter API key.

The app writes no transcript, session log, or message content to disk. Session
content stays in memory while the app runs. Voice audio for OpenRouter is the
one exception: the app keeps it on disk only until the upload ends.

## Camera

The app uses the camera for one purpose: to scan the pairing QR code. The scan
runs on the device. The app stores no image and
sends no image.

## Microphone and Speech Recognition

The app uses the microphone for one purpose: voice input, which turns your
speech into text. Voice input is off until you select a provider.

- **System** uses the speech recognizer of your operating system. Recognition
  runs on the device when it can. Otherwise the speech service of your
  operating system processes the audio.
- **OpenRouter** sends the audio from your device to
  [OpenRouter](https://openrouter.ai) with your own API key. OpenRouter sends
  the audio to the model provider that you select. The audio and the key never
  pass through your gateway, and they never reach the developer.

## Data Stored on Your Machines

`argus` writes its state to files on your own machines. Only your user account
can read them. Argus sends none of that state to the developer.

## Push Notifications

A notification carries a short summary of what needs your attention. Your node
encrypts it for your device before it leaves your machine. Only your device can
decrypt it, so no service on the delivery path can read it.

### Android

[UnifiedPush](https://unifiedpush.org) is the default. The app embeds an FCM
distributor and uses it unless you select a different
[distributor](https://unifiedpush.org/users/distributors/), for example ntfy or
Sunup. [PushPort](#pushport) over FCM is an alternative to UnifiedPush.

### iOS

[PushPort](#pushport) over the Apple Push Notification service (APNs) is the
only push path.

### PushPort

[PushPort](https://pushport.muniftanjim.dev) is a push relay that the developer
operates. It is used only after you register an instance token for your
gateway, and on Android only if you select it.

PushPort receives the label of your instance, your device push token, the
encrypted notifications, and the IP address of each request. It stores only the
label and the IP address of the registration, until you delete the instance with
your instance token. It asks for no identity, so no data in PushPort is tied to
a specific person.

The developer processes this data on the basis of a legitimate interest: to
deliver your notifications and to protect PushPort from abuse. PushPort can run
on servers outside your country.

## Analytics and Tracking

The app contains no analytics, no crash reporting, no advertising, no location
tracking, and no device fingerprinting. It sends no usage data to the developer
or to any third party.

The platforms report separately from the app. If you turn on sharing in the
settings of your operating system, the App Store and Google Play give the
developer aggregate crash reports and usage reports. Argus plays no part in
this.

## External Services

Argus contacts an external service only when you use the feature that needs it:

- **PushPort**, operated by the developer, relays encrypted push payloads. See
  [PushPort](#pushport).
- **Apple Push Notification service (APNs)**, operated by Apple, delivers
  encrypted push payloads to iOS devices.
- **Firebase Cloud Messaging (FCM)**, operated by Google, delivers encrypted
  push payloads to Android devices.
- **A [UnifiedPush distributor](https://unifiedpush.org/users/distributors/)**,
  for example ntfy or Sunup, delivers encrypted push payloads on Android.
- **A tunnel or proxy**, for example Cloudflare Tunnel, carries traffic to your
  gateway, if you expose the gateway beyond your local network.
- **OpenRouter** transcribes your voice for the OpenRouter voice provider. See
  [Microphone and Speech Recognition](#microphone-and-speech-recognition).
- **The speech service of your operating system** transcribes your voice for
  the System voice provider.

Some tunnels, including Cloudflare Tunnel, terminate TLS at their edge, so the
operator can read the gateway traffic that passes through. To close that gap,
turn on [end-to-end encryption](/guide/e2ee).

Other than voice input, Argus sends no session data to an AI provider. The
coding agent that you run, for example Claude Code, makes its own network
connections. This policy does not cover them.

The privacy policy of each third party governs the data that the third party
handles.

## Data Retention and Deletion

Argus stores your data on machines you control, so you decide what to keep and
for how long. There is no Argus account, so no account exists to delete.

You can revoke a device, delete its push registration, and erase the
credentials of a gateway at any time.

An uninstall removes the data of the app on Android. On iOS, data in the secure
storage can survive an uninstall, and a reinstalled app can read it again. To
erase it, delete your gateways and your OpenRouter API key from the app before
you uninstall.

## Your Rights

If the GDPR covers you, you have the rights of access, correction, erasure,
restriction, portability, and objection. To use them, write to the address in
[Contact](#contact). Because no data is tied to a specific person, the developer
usually cannot find data that belongs to you. You can also complain
to the data protection authority of your country.

The developer does not sell your personal information. The developer does not
share it for cross-context behavioral advertising. The developer makes no
automated decisions about you and builds no profiles.

## Legal Disclosure

If the developer receives a legal demand, the developer has no data to give
that identifies you or reveals the content of your notifications.

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
