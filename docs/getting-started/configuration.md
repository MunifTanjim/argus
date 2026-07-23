# Configuration

The defaults work out of the box — no config needed to start.

When you do want to tweak something, every setting is available three ways, in
priority order: a **command-line flag** (e.g. `--token`), an **`ARGUS_*`
environment variable**, or a key in the **YAML config file** — otherwise the
built-in default applies. A set flag wins over an env var, which wins over the
file.

Run `argus <command> --help` for the settings each command accepts — that's the
authoritative list.

## Config file

Argus reads `$XDG_CONFIG_HOME/argus/config.yaml` by default (typically
`~/.config/argus/config.yaml`). Point at a different file with `--config` or
`$ARGUS_CONFIG`. A missing default file is fine; a missing explicit `--config`
path is an error.

`argus config dir` prints the config directory path — handy in scripts or when you
can't remember where it lives:

```sh
argus config dir
```

A minimal example:

```yaml
# ~/.config/argus/config.yaml
token: shared-secret    # gateway token — see Multi Machine

push:
  desktop:
    enabled: true       # native desktop notifications on this node (macOS, opt-in)

log:
  level: info           # trace | debug | info | warn | error | fatal
  format: pretty        # pretty | json
```

## Desktop notifications

`push.desktop.enabled` (default `false`) opts this node into native **macOS**
desktop notifications: when a session starts waiting on you (permission prompt,
question, plan, or a finished turn), this machine pops a banner, and clicking it
focuses that session's tmux pane. Other platforms are a no-op.

It is config-file / env only — there is no command-line flag:

```yaml
push:
  desktop:
    enabled: true
```

or `ARGUS_PUSH_DESKTOP_ENABLED=true`.

### Renderers

Argus renders through whichever of three backends it finds, in this order — and
the experience differs a lot between them, so installing the preferred one is
worth it:

1. **[`alerter`](https://github.com/vjeantet/alerter) — preferred, best
   experience.** A self-contained binary; nothing to configure. You get a
   clickable banner branded with the Argus icon, and repeat alerts for the same
   session replace the previous one instead of stacking. Install it on `PATH`:

   ```sh
   brew install vjeantet/tap/alerter
   ```

2. **[Hammerspoon](https://www.hammerspoon.org/) — clickable, extra setup.**
   Used only if `alerter` is absent. Requires both the `hs` CLI on `PATH` **and
   the IPC module enabled** — add `require("hs.ipc")` to your
   `~/.hammerspoon/init.lua` and reload the config. Without IPC loaded, `hs -c`
   fails (exit 69, "can't access Hammerspoon message port") and argus falls back
   to the plain banner below.

3. **`osascript` — always available, not clickable.** The built-in fallback when
   neither of the above is usable. You still get a notification, but clicking it
   does nothing (no jump to the session).

So: **install `alerter` for the full click-to-focus experience.** Everything
degrades gracefully — a missing tool, a failed render, or a non-macOS host never
breaks anything, it just drops to the next best (or silently no-ops).

Enable it on each machine you sit in front of; leave it off on headless boxes.

## Mobile notifications

`push.mobile.delay` (default `0s`) sets a grace period before a mobile push
fires. With the default, mobile pushes are instant — the same moment in-app and
desktop notifications go out.

Set it to a non-zero duration to hold mobile pushes back:

```yaml
push:
  mobile:
    delay: 30s
```

When the delay elapses, the push fires only if the session is still awaiting
input or idle — so answering at your desk within the window keeps the phone
quiet. Desktop and in-app notifications are always instant.
