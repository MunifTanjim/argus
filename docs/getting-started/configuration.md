# Configuration

The defaults work out of the box. You don't need a config to start.

You can set each option in three ways: a **command-line flag** (e.g.
`--token`), an **`ARGUS_*` environment variable**, or a key in the **YAML config
file**. A flag overrides an environment variable, which overrides the file. If
you set none of them, the built-in default applies.

Run `argus <command> --help` to see the settings each command accepts. That is the
authoritative list.

## Config file

Argus reads `$XDG_CONFIG_HOME/argus/config.yaml` by default (typically
`~/.config/argus/config.yaml`). Use a different file with `--config` or
`$ARGUS_CONFIG`. A missing default file is fine; a missing explicit `--config`
path is an error.

`argus config dir` prints the config directory path:

```sh
argus config dir
```

A minimal example:

```yaml
# ~/.config/argus/config.yaml
token: shared-secret    # gateway token, see Multi Machine

push:
  desktop:
    enabled: true       # native desktop notifications on this node (macOS, opt-in)

log:
  level: info           # trace | debug | info | warn | error | fatal
  format: pretty        # pretty | json
```

## Projects & Workspaces

For the project and workspace settings, see
[Projects & Workspaces](/guide/projects).

## TUI keymaps

To change the keys of the TUI, see [Keymaps](/guide/tui#keymaps).

## End-to-End Encryption

`e2ee.enabled` (default `false`) turns on the blind-relay encrypted transport.
`lock.genesis` pins the install to a trust log for locked mode. See
[End-to-End Encryption](/guide/e2ee) for setup and the two modes.

## Desktop notifications

`push.desktop.enabled` (default `false`) enables native **macOS** desktop
notifications on this node. When a session waits for you (permission prompt,
question, plan, or a finished turn), this machine shows a banner. Clicking the
banner focuses that session's tmux pane. On other platforms this setting does
nothing.

Set it in the config file or with an environment variable. There is no
command-line flag:

```yaml
push:
  desktop:
    enabled: true
```

or `ARGUS_PUSH_DESKTOP_ENABLED=true`.

### Renderers

Argus uses the first of three backends that it finds, in this order. The
backends behave differently, so install the preferred one:

1. **[`alerter`](https://github.com/vjeantet/alerter): preferred.** A
   self-contained binary that needs no configuration. It shows a clickable
   banner with the Argus icon. A new alert for a session replaces the previous
   alert for that session. Install it on `PATH`:

   ```sh
   brew install vjeantet/tap/alerter
   ```

2. **[Hammerspoon](https://www.hammerspoon.org/): clickable, needs extra setup.**
   Argus uses it only if `alerter` is absent. It requires the `hs` CLI on `PATH`
   **and the IPC module enabled**. Add `require("hs.ipc")` to your
   `~/.hammerspoon/init.lua` and reload the config. Without IPC, `hs -c` fails
   (exit 69, "can't access Hammerspoon message port") and Argus falls back to
   the plain banner below.

3. **`osascript`: always available, not clickable.** The built-in fallback when
   neither of the others is usable. You still get a notification, but clicking
   it does nothing.

Install `alerter` to get click-to-focus. If a tool is missing, a render fails,
or the host is not macOS, Argus logs a warning and uses the next backend or shows
nothing. A notification failure never stops Argus.

Enable it on each machine you sit in front of; leave it off on headless boxes.

## Mobile notifications

`push.mobile.delay` (default `0s`) sets how long to wait before sending a mobile
push. With the default, mobile pushes are sent at the same time as in-app and
desktop notifications.

Set a non-zero duration to delay mobile pushes:

```yaml
push:
  mobile:
    delay: 30s
```

After the delay, Argus sends the push only if the session is still awaiting
input or idle. If you answer at your desk within the delay, your phone gets
nothing. Desktop and in-app notifications are never delayed.
