# Releasing

Releases are driven by [release-please](https://github.com/googleapis/release-please).
Pushing conventional commits to `main` keeps release PRs open; merging one cuts
the release and triggers the matching build.

- **Go binary** — `release.yml` → release PR titled `chore: release X.Y.Z`,
  tagged `X.Y.Z`; merging it runs `publish.yml` (builds/uploads the binaries).
- **Flutter app** — the same `release.yml` manages a second release-please
  package (`app`). Commits touching `app/` open a PR titled
  `chore(app): release X.Y.Z`, tagged `app-X.Y.Z`; merging it runs
  `publish-app.yml` (builds a signed AAB and uploads it to the Play Store).

## Cutting an app release

1. Land `feat(app): …` / `fix(app): …` commits on `main`.
2. release-please opens/updates the `chore(app): release X.Y.Z` PR (bumps
   `app/pubspec.yaml` and the app CHANGELOG). The version line is
   `X.Y.Z+N`: release-please bumps the semver (`versionName`) and increments the
   `+N` build number (`versionCode`) in the same PR, so both are captured in the
   tagged commit and the build needs no version flags. Baselined at `0.0.13+13`
   (the last published versionCode), so the first release is `0.0.14+14` and the
   patch tracks the build number until a breaking change bumps the minor.
3. Merge the PR. `publish-app.yml` builds and uploads to the **closed testing
   (`alpha`)** track. Promote to production manually in the Play Console.

## One-time setup (Play Store)

`publish-app.yml` needs these repository **secrets** (Settings → Secrets and
variables → Actions):

| Secret | What it is |
| --- | --- |
| `PLAY_SERVICE_ACCOUNT_JSON` | JSON key for a Google Cloud service account granted "Release to testing tracks" in the Play Console (Users & permissions). |
| `ANDROID_KEYSTORE_BASE64` | The **upload keystore**, base64-encoded: `base64 -w0 upload-keystore.jks`. Must be the same upload key already registered with Play App Signing, or Play rejects the AAB. |
| `ANDROID_KEYSTORE_PASSWORD` | Keystore (store) password. |
| `ANDROID_KEY_ALIAS` | Upload key alias. |
| `ANDROID_KEY_PASSWORD` | Upload key password. |

The workflow reconstructs `app/android/key.properties` + the keystore from these
at build time, reusing the same signing config as local builds
(`app/android/app/build.gradle.kts`).

### Safe first run

For the first run, temporarily set `status: draft` (or `track: internal`) in
`publish-app.yml` to validate the pipeline end-to-end without pushing to real
testers, then switch back to `alpha` / `completed`.
