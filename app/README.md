# argus_app

A new Flutter project.

## Getting Started

This project is a starting point for a Flutter application.

A few resources to get you started if this is your first Flutter project:

- [Learn Flutter](https://docs.flutter.dev/get-started/learn-flutter)
- [Write your first Flutter app](https://docs.flutter.dev/get-started/codelab)
- [Flutter learning resources](https://docs.flutter.dev/reference/learning-resources)

For help getting started with Flutter development, view the
[online documentation](https://docs.flutter.dev/), which offers tutorials,
samples, guidance on mobile development, and a full API reference.

## PushPort build configuration

PushPort support is off unless the build embeds a PushPort app id. Configure it
with a `.env` file consumed via `--dart-define-from-file`. Copy the template and
fill it in (all values are public — no secrets):

```sh
cp .env.example .env
```

| Key | Purpose |
|---|---|
| `PUSHPORT_APP_ID` | Your registered PushPort app id. Empty by default; set it to enable PushPort. |
| `PUSHPORT_BASE_URL` | Overrides the default `https://pushport.muniftanjim.dev`. |

`.env` is gitignored; `.env.example` is committed.

The **gateway's** instance token is not a build-time value — it is set by running
`argus pushport register --gateway wss://your-gateway --token <master-token>` on
the gateway machine.

Both `make app-build` and `make app-run` load `app/.env` automatically when it
is present:

```sh
make app-build   # release APK, PushPort config from .env
make app-run     # dev run, PushPort config from .env
```

To point at a different file, pass it via `ARGS`, e.g.
`make app-build ARGS="--dart-define-from-file=prod.env"`.

### FCM (PushPort/FCM provider)

FCM needs a real Firebase config, which is not committed:

1. `cp android/app/google-services.example.json android/app/google-services.json`
2. Replace the placeholder values with the real Firebase credentials for
   `dev.muniftanjim.argus`.
3. Rebuild.

Without `google-services.json`, the `com.google.gms.google-services` Gradle
plugin is skipped and `Firebase.initializeApp()` fails gracefully — FCM is
inactive, every other feature works.

The gateway must send the encrypted body under the FCM data key `body`
(`lib/push/fcm_source.dart`, `_kBodyKey`). Confirm the PushPort server uses the
same key before deploying a new server version.
