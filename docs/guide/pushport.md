# PushPort

[**PushPort**](https://pushport.muniftanjim.dev) is a hosted push relay and an
alternative push provider for Argus.

[UnifiedPush](/guide/mobile-app#push-notification) is the default. PushPort is an
alternative — pick whichever you prefer. Your notifications stay private either
way: the Argus node encrypts each notification end-to-end for your device, so
neither the gateway nor the relay sees the content.

Delivery runs over **Firebase Cloud Messaging** (Android).

## Prerequisites

- An Argus app build that embeds a PushPort app id. Build it with
  `make build PUSHPORT_APP_ID=<your-app-id>`. Without an embedded app id, the
  PushPort option does not appear in the app.
- A reachable gateway and its master token.

## Setup

1. Register the instance token. Run this command on any machine that can reach
   your gateway:

   ```sh
   argus pushport register --gateway wss://your-gateway --token <master-token>
   ```

   The command opens the PushPort registration page. Complete the form, copy the
   `pit_…` token, and paste it back when prompted. The gateway stores the token
   and applies it live. No restart is needed.

2. Select the provider in the app. Open **Settings → Push → Provider** and
   select **PushPort/FCM**. This option appears only when the gateway has an
   instance token.

The app then subscribes and registers its endpoint with your gateway.
