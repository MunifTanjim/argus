# PushPort

[**PushPort**](https://pushport.muniftanjim.dev) is a hosted push relay and an
alternative push provider for Argus.

On Android, [UnifiedPush](/guide/mobile-app#push-notification) is the default and
PushPort is an alternative. You can use either one. On iOS, PushPort is the only
push path. Your notifications stay private either way: the Argus node encrypts
each notification end-to-end for your device, so neither the gateway nor the
relay sees the content.

Delivery runs over **Firebase Cloud Messaging** on Android and **Apple Push Notification service** on iOS.

## Setup

1. Register the instance token. Run this command on any machine that can reach
   your gateway:

   ```sh
   argus pushport register --gateway wss://your-gateway --token <master-token>
   ```

   The command opens the PushPort registration page. Complete the form, copy the
   `pit_…` token, and paste it back when prompted. The gateway stores the token
   and applies it live. No restart is needed.

2. **Android only** — select the provider in the app. Open
   **Settings → Push → Provider** and select **PushPort / FCM**. The option
   appears only after step 1.

   On iOS the app selects **PushPort / APNs** by itself, on the next connection
   to the gateway. No action is needed.

The app then subscribes and registers its endpoint with your gateway.
