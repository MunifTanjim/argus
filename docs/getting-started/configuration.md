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

A minimal example:

```yaml
# ~/.config/argus/config.yaml
token: shared-secret    # gateway token — see Multi Machine

log:
  level: info           # trace | debug | info | warn | error | fatal
  format: pretty        # pretty | json
```
