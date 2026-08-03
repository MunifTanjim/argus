#!/usr/bin/env bash
# Run argus against a throwaway, per-namespace home so several nodes, a gateway
# and a client can share one machine without touching each other or your real
# config. Mirrors the isolation internal/e2elive gives its processes.
#
#   scripts/demo.sh gw start --mode=gateway --token=t --listen-addr=127.0.0.1:9999
#   scripts/demo.sh a  start --gateway=ws://127.0.0.1:9999 --token=t --id=node-a
#   scripts/demo.sh a  lock status
#
# Everything a namespace writes lives under demo/test/<namespace>/, so `rm -rf`
# on that directory is a full reset. The binary is shared at demo/test/.bin/argus
# and rebuilt when the sources are newer.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$ROOT/demo/test"
BIN="$TEST_DIR/.bin/argus"

info() { printf "\033[0;34m%s\033[0m\n" "$1" >&2; }
die() {
  printf "\033[0;31mError: %s\033[0m\n" "$1" >&2
  exit 1
}

usage() {
  cat >&2 <<EOF
Usage: ${BASH_SOURCE[0]##*/} <namespace> [argus args...]

Runs the argus binary with an isolated HOME and XDG dirs under
demo/test/<namespace>/.

  namespace   directory name for this instance's data (e.g. gw, a, b, phone)

Environment:
  DEMO_REBUILD=1   rebuild the binary even if it looks current
  DEMO_NO_BUILD=1  never build; fail if the binary is missing

Examples:
  ${BASH_SOURCE[0]##*/} gw start --mode=gateway --token=t --listen-addr=127.0.0.1:9999
  ${BASH_SOURCE[0]##*/} a start --gateway=ws://127.0.0.1:9999 --token=t --id=node-a
  ${BASH_SOURCE[0]##*/} a lock status
EOF
  exit 1
}

[[ $# -ge 1 ]] || usage
case "$1" in
-h | --help) usage ;;
esac

NS="$1"
shift

# The namespace becomes a path segment, so keep it to something that cannot climb
# out of demo/test or surprise the shell.
[[ "$NS" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] ||
  die "namespace '$NS' must start alphanumeric and use only letters, digits, dot, underscore or dash"

NS_DIR="$TEST_DIR/$NS"

# argus defaults its socket to \$XDG_RUNTIME_DIR/argus/argus.sock, and macOS caps a
# unix socket path at 104 bytes. Say so now rather than letting the node fail to
# bind with a less obvious message.
SOCK="$NS_DIR/run/argus/argus.sock"
[[ ${#SOCK} -le 104 ]] ||
  die "socket path is ${#SOCK} bytes, over the 104 limit: $SOCK
     use a shorter namespace, or pass --socket=/tmp/<something>.sock"

needs_build() {
  [[ -n "${DEMO_REBUILD:-}" ]] && return 0
  [[ -x "$BIN" ]] || return 0
  [[ "$ROOT/go.mod" -nt "$BIN" || "$ROOT/go.sum" -nt "$BIN" ]] && return 0
  [[ -n "$(find "$ROOT/cmd" "$ROOT/internal" -name '*.go' -newer "$BIN" -print -quit)" ]]
}

if needs_build; then
  [[ -z "${DEMO_NO_BUILD:-}" ]] || die "binary missing or stale at $BIN and DEMO_NO_BUILD is set"
  info "building $BIN"
  mkdir -p "$(dirname "$BIN")"
  version="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo demo)"
  (cd "$ROOT" && go build -ldflags "-X main.version=$version" -o "$BIN" ./cmd/argus)
elif [[ -n "${DEMO_NO_BUILD:-}" && ! -x "$BIN" ]]; then
  die "binary missing at $BIN and DEMO_NO_BUILD is set"
fi

mkdir -p "$NS_DIR"/{config,state,cache,run}

# exec so signals and the exit code belong to argus itself — `start` is meant to be
# stopped with Ctrl-C.
exec env \
  HOME="$NS_DIR" \
  XDG_CONFIG_HOME="$NS_DIR/config" \
  XDG_STATE_HOME="$NS_DIR/state" \
  XDG_CACHE_HOME="$NS_DIR/cache" \
  XDG_RUNTIME_DIR="$NS_DIR/run" \
  "$BIN" "$@"
