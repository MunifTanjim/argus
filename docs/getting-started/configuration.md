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

## Logs

`pretty` logs have color when they go to a terminal. Set `NO_COLOR` to turn the
color off, or `CLICOLOR_FORCE=1` to keep it when you pipe the output.

## Agent hooks

`bin` (default `argus`) is the binary that the Antigravity hooks run. Set it to
a full path when `argus` is not on the `PATH` of Antigravity, for example when
it starts from a desktop launcher. Argus updates the hooks the next time it
starts. The Claude Code plugin does not use this setting.

## Agent tools

`mcp.port` (default `7734`) is the local port where the node serves the
[agent tools](/getting-started/installation#agent-tools). Set it to `0` to turn
the tools off. Argus then removes its entries from the agent configs. If the
port is in use, the node runs without the tools and logs an error.

## Agents

Persistent agents read these keys under `agents`. An agent's own definition
overrides the `agents.defaults` values.

| Key | Default | Meaning |
| --- | --- | --- |
| `agents.default-dir` | data directory `agents` | Where new agent directories go. |
| `agents.profile-dir` | data directory `profile` | Where the profile is kept. |
| `agents.skills-dir` | data directory `skills` | Where shared skills are kept. |
| `agents.primary` | none | The agent that receives requests when none is named. |
| `agents.webhook.listen-addr` | none (off) | Address, such as `127.0.0.1:7420`, where the node accepts routine webhooks. Reachability from other machines is up to your network. |
| `agents.execution` | `ask` | What agents may do on this node without a matching approval rule. `ask` asks you about risky actions from your own requests and denies actions from unattended work. `allow` lets unattended work and ordinary actions run, and still asks about risky actions from your requests. `never-unattended` denies every action from unattended work. |
| `agents.approvals.unattended-expiry` | `30m` | How long a request for approval from unattended work waits for your answer before it is denied. |
| `agents.approvals.hook.command` | none | A program asked about every agent action. See [Agents](/guide/agents#policy-hook). |
| `agents.approvals.hook.url` | none | An HTTPS address asked about every agent action, instead of a program. |
| `agents.approvals.hook.mode` | `shadow` | `shadow` only records the hook's answers. `fail-open` lets them decide, falling back to the rules when the hook fails. `fail-closed` lets them decide and denies when the hook fails. |
| `agents.approvals.hook.timeout` | `5s` | How long Argus waits for the hook. |
| `agents.defaults.agent` | `claude` | The coding agent that runs the sessions. |
| `agents.defaults.model` | none | The model of the sessions. |
| `agents.defaults.timezone` | `auto` | The timezone of schedules and quiet hours. |
| `agents.defaults.idle-close` | `30m` | How long an idle session stays open. |
| `agents.defaults.max-concurrent-sessions` | `2` | Sessions an agent runs at once. |
| `agents.defaults.git.mode` | `nested` | `nested` gives each agent its own repository. `outer` uses an enclosing repository when there is one. |
| `agents.defaults.git.commit-files` | `false` | Whether the `files` directory is committed. |
| `agents.defaults.memory.mode` | `auto` | `auto`, `review` or `off`. |
| `agents.defaults.memory.core-budget` | `24KB` | Size limit of the core memory. |
| `agents.defaults.memory.capture-after` | `2m` | How long after a session goes quiet its memory is captured. |
| `agents.defaults.memory.tidy-at` | `03:00` | Time of the daily memory tidy. |
| `agents.defaults.memory.log-entries` | `20` | Recent log entries loaded into a session. |
| `agents.defaults.memory.extractor` | none | The model that captures memory. |
| `agents.defaults.proactivity` | `default` | `off`, `low`, `default` or `high`. |
| `agents.defaults.quiet-hours` | none | Hours, such as `22:00-07:00`, when background work does not notify you; you get one summary when they end. |
| `agents.defaults.notify` | `needs-you` | `all`, `needs-you` or `off`. |
| `agents.defaults.notify-delay` | `3m` | While you are connected, how long a notification waits; it is dropped if you read the thread meanwhile. |
| `agents.defaults.brief.enabled` | `false` | Whether the agent writes a daily brief. |
| `agents.defaults.brief.at` | `08:00` | Time of the daily brief. |
| `agents.defaults.limits.min-interval` | `15m` | Shortest interval between runs of a routine. |
| `agents.defaults.limits.max-enabled-routines` | `20` | Routines an agent can have enabled. |
| `agents.defaults.limits.max-check-backs` | `10` | Pending check-backs an agent can have. |
| `agents.defaults.limits.run-timeout` | `1h` | Longest time a run can take. |
| `agents.defaults.limits.max-runs-per-hour` | `12` | Routine runs an agent can start in an hour. |
| `agents.defaults.limits.max-consecutive-failures` | `10` | Failed runs in a row after which a routine pauses. |
| `agents.defaults.limits.check-back-min` | `15m` | Shortest time a self-paced routine can wait before its next run. |
| `agents.defaults.limits.check-back-max` | `7d` | Longest time a self-paced routine can wait before its next run. |
| `agents.defaults.limits.catch-up-window` | `12h` | How old a missed run can be and still run when the node comes back. |
| `agents.defaults.limits.pause-after-away` | `7d` | Routines pause when no client has connected for this long. |

## Projects & Workspaces

For the project and workspace settings, see
[Projects & Workspaces](/guide/projects).

## TUI keymaps

To change the keys of the TUI, see [Keymaps](/guide/tui#keymaps).

## Lua

To configure argus with Lua, add an `init.lua` file next to the config file in
use. If no config file exists, argus reads `init.lua` from the [config
directory](#config-file). `--no-config` skips it. If the file does not exist,
nothing happens.

The TUI is the only client that loads `init.lua`. `argus` and `argus view` load
it.

The file runs once when the TUI starts. If it fails, the footer shows the error
and the TUI starts without the bindings that the file did not register. The
standard library is available, and `require` finds modules next to `init.lua`.
The
functions `io.popen`, `os.execute`, and `os.exit` raise an error. Use
`argus.exec` to run a program. `io.read`, `io.write`, `io.input`, and
`io.output` also raise an error. Each method of `io.stdin`, `io.stdout`, and
`io.stderr` raises an error. `io.lines` without a file name raises an error.
`io.close` without a file raises an error. `dofile` and `loadfile` without a
file name raise an error. `io.open` and `io.lines` with a file name work.
`print` writes to the [log](#logs). If the TUI started no local servers, it has
no log, and `print` writes nothing.

If a Lua call runs longer than 2 seconds, it stops with an error. The load of
`init.lua` is one call, and each key press is one call. The time inside
`argus.exec` does not count. A file read that waits for input, for example
from a FIFO, is not stopped. A coroutine works only in the call that creates
it.

```lua
argus.bind("ctrl+a>w", "client.pane.focus_next")

argus.bind("ctrl+a>t", function(ctx)
  local r = argus.exec({ "tmux", "split-window", "-h" })
  return r ~= nil and r.ok
end)
```

### argus.action

`argus.action{ name, title, run }`

Defines an action. `argus.action` works only while `init.lua` loads.

- `name` uses only lowercase letters, digits, and underscores. If a name is
  defined again, the later definition wins and the log gets a warning.
- `title` is the label of the keys bound to the action, in the footer and the
  help.
- `run` is a function. It receives `ctx` and `args`. `ctx.section` is the name
  of the focused [section](/guide/tui#sections). `args` is the table given to
  `argus.bind` or `argus.invoke`, or an empty table. The value that `run`
  returns is the result of the action.

```lua
argus.action({
  name = "split",
  title = "split pane",
  run = function(ctx, args)
    return argus.exec({ "tmux", "split-window", args.dir or "-h" })
  end,
})

argus.bind("ctrl+a>v", "split", { dir = "-v" })
```

### argus.bind

`argus.bind(key, target[, args])`

`key` uses the
[Rex key notation](https://www.superlogical.com/rex/docs/customize/keyboard-shortcuts),
not the Vim notation of YAML mappings. `argus.bind` works only while
`init.lua` loads. A Lua binding replaces a mapping or a built-in key with the
same key. The same sequence and `tui.key-timeout` rules apply as for YAML
mappings.

`target` is a function or an action name. The [command
table](/guide/tui#commands) lists the built-in action names. An action must be
defined before a binding names it.

- A function receives `ctx`. `ctx.section` is the name of the focused
  [section](/guide/tui#sections). If the function returns `true`, the key is
  handled. If it returns `nil` or `false`, the key keeps its mapping or built-in
  meaning, and the effects of its `argus.invoke` calls stay. If the function
  raises an error, the footer shows the error and the key is handled.
- A built-in action name runs that action. In a section that does not have the
  action, the key keeps its mapping or built-in meaning. An unknown action name
  raises an error at load.
- An action defined with `argus.action` gets `args`, and the key is always
  handled. If `run` raises an error, the footer shows the error.

`args` is a table, and only an action defined with `argus.action` takes it.

These differences from Rex apply:

- `cmd`, `super`, and `meta` are the same modifier. A terminal sends it only
  with the Kitty keyboard protocol.
- A physical key in brackets, for example `ctrl+[KeyA]`, raises an error.
- A character is the character that the key types, shift included. For
  example, `&` and `D` are keys, and `shift+7` does not match `&`.
- `+` as the last key, as in `alt++`, is the `+` key. `>` at the start of a
  key or after `+`, as in `ctrl+a>>` or `alt+>`, is the `>` key.
- A key with a mode prefix, for example `nav/h`, raises an error.

A key can start with a section name and `:`, for example `workspace:ctrl+h`.
The binding then works only in that section. `global:` and no prefix both mean
every section. A section binding wins over a global binding for the same key.
For the same key and section, the last `argus.bind` wins.

A binding works in every section where keys use the keymap. It does not work
on the live screen, in a text input, or while a popup or the help is open.

### argus.invoke

`argus.invoke(action[, args])`

Runs an action and returns two values: the result and an error message. If
the action runs, the error is `nil`. An action defined with `argus.action`
returns the value that its `run` returns, and gets `args`. The focus
actions return `{ ok = true }` if the focused pane changed and
`{ ok = false }` if not. The tab actions return `{ ok = true }` if the tab
changed and `{ ok = false }` if not, for example if the pane has only one tab.

In these cases, the result is `nil` and the error tells why:

- The action name is unknown.
- The focused section does not have the built-in action.
- `args` is given to a built-in action.
- `run` raises an error. The effects of `run` before the error stay.

`argus.invoke` works only inside a bound function or an action. Elsewhere it
raises an error. If `args` is not a table, it also raises an error.

### argus.exec

`argus.exec(argv[, opts])`

Runs a program without a shell. `argv` is a list of strings: the program, then
its arguments. The TUI waits for the program to end.

`argus.exec` runs each program in its own process group, and the TUI owns the
terminal. If a program reads the terminal or changes its modes, for example
`fzf`, the program stops until `SIGKILL` ends it, 2 seconds after the
timeout. If a program writes to `/dev/tty`, the output draws over the TUI.

| Option | Meaning |
|---|---|
| `cwd` | The working directory. The default is the one of the TUI. |
| `env` | A table of variables to set. A value of `false` removes the variable. |
| `clear_env` | If `true`, the program starts with an empty environment. `env` still applies. |
| `stdin` | A string that the program reads from its standard input. |
| `timeout` | Seconds until the program and every process it started get `SIGTERM`. `SIGKILL` follows 2 seconds later. The default is 2. |
| `stdout`, `stderr` | If `false`, the stream is discarded. |

An unknown option raises an error.

The result is a table with these fields:

- `code`: the exit status, or `-1` if a signal ended the program.
- `signal`: the signal name, for example `SIGTERM`, or `nil`.
- `stdout`, `stderr`: the captured output, or `""`. argus keeps the first
  1 MiB of each stream and discards the rest.
- `timed_out`: `true` if `timeout` stopped the program.
- `ok`: `true` if `code` is `0` and the program did not time out.

A non-zero exit is a result, not an error. If the program does not start,
`argus.exec` returns `nil` and an error message. An empty `argv`, an argument
or option of the wrong type, and a `timeout` that is not a positive number
raise an error.

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
