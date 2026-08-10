#!/usr/bin/env bash
# Run argus in a throwaway per-namespace container so several nodes, a gateway
# and a client can share one machine without seeing each other's agent sessions.
# HOME isolation alone is not enough: discovery reads the machine's process table
# and tmux sockets, so every namespace would report the same Claude sessions.
#
#   scripts/demo.sh gw start --mode=gateway --token=t --listen-addr=:8443
#   scripts/demo.sh a  start --gateway=ws://argus-demo-gw:8443 --token=t --id=node-a
#   scripts/demo.sh a  lock status
#   scripts/demo.sh --down
#
# Everything a namespace writes lives under demo/test/<namespace>/ on the host,
# so `rm -rf` on that directory is a full reset.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$ROOT/demo/test"
IMAGE="argus-demo:dev"
NETWORK="argus-demo"
CONTAINER_HOME="/home/argus"

info() { printf "\033[0;34m%s\033[0m\n" "$1" >&2; }
die() {
  printf "\033[0;31mError: %s\033[0m\n" "$1" >&2
  exit 1
}

usage() {
  cat >&2 <<EOF
Usage: ${BASH_SOURCE[0]##*/} <namespace> [argus args...]
       ${BASH_SOURCE[0]##*/} --down

Runs the argus binary in a container named argus-demo-<namespace>, on the
$NETWORK network, with demo/test/<namespace>/ mounted at $CONTAINER_HOME.

  namespace   name for this instance (e.g. gw, a, b, phone)
  --down      remove every demo container and the network

Namespaces reach each other by container name, for example
ws://argus-demo-gw:8443.

Environment:
  DEMO_REBUILD=1   rebuild the image even if it exists
  DEMO_NO_BUILD=1  never build; fail if the image is missing
  DEMO_PUBLISH     host port to publish to the container's 8443 (gateway only).
                   A bare port binds 127.0.0.1, because the demo token is
                   trivial. Give a host to bind elsewhere on purpose, e.g.
                   DEMO_PUBLISH=0.0.0.0:9999

A \`start\` records its --gateway and --token in demo/test/<namespace>/config/argus/
config.yaml, so later commands in that namespace reach the gateway without
repeating them. A gateway namespace records only the token: see below.

Examples:
  ${BASH_SOURCE[0]##*/} gw start --mode=gateway --token=t --listen-addr=:8443
  ${BASH_SOURCE[0]##*/} a start --gateway=ws://argus-demo-gw:8443 --token=t --id=node-a
  ${BASH_SOURCE[0]##*/} a lock status
  ${BASH_SOURCE[0]##*/} a lock init sigpub:<hex>
  ${BASH_SOURCE[0]##*/} gw lock init --gateway=ws://127.0.0.1:8443 sigpub:<hex>
EOF
  exit 1
}

[[ $# -ge 1 ]] || usage
case "$1" in
-h | --help) usage ;;
--down)
  info "removing demo containers and network"
  ids="$(docker ps -aq --filter "label=argus.demo" || true)"
  [[ -z "$ids" ]] || docker rm -f $ids >/dev/null
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
  exit 0
  ;;
esac

NS="$1"
shift

# The namespace is both a path segment and a container name, so keep it to
# something that cannot climb out of demo/test or upset docker.
[[ "$NS" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] ||
  die "namespace '$NS' must start alphanumeric and use only letters, digits, dot, underscore or dash"

NS_DIR="$TEST_DIR/$NS"
CONTAINER="argus-demo-$NS"

image_exists() { docker image inspect "$IMAGE" >/dev/null 2>&1; }

if [[ -n "${DEMO_REBUILD:-}" ]] || ! image_exists; then
  if [[ -n "${DEMO_NO_BUILD:-}" ]]; then
    [[ -z "${DEMO_REBUILD:-}" ]] || die "DEMO_REBUILD and DEMO_NO_BUILD are both set"
    die "image $IMAGE missing and DEMO_NO_BUILD is set"
  fi
  info "building $IMAGE"
  version="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo demo)"
  docker build \
    --target argus-demo \
    --build-arg "VERSION=$version" \
    -t "$IMAGE" \
    -f "$ROOT/docker/Dockerfile" \
    "$ROOT"
fi

docker network inspect "$NETWORK" >/dev/null 2>&1 ||
  docker network create --label argus.demo=1 "$NETWORK" >/dev/null

mkdir -p "$NS_DIR"/{config,state,cache,run}

# The flags of a `start` describe the namespace's topology, so record the parts
# every later command needs. Without this, `demo.sh a lock init` reaches the
# gateway with no token and the handshake is refused with 401.
start_gateway=""
start_token=""
start_mode=""
start_listen=""
parse_start_flags() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
    --gateway=*) start_gateway="${1#*=}" ;;
    --token=*) start_token="${1#*=}" ;;
    --mode=*) start_mode="${1#*=}" ;;
    --listen-addr=*) start_listen="${1#*=}" ;;
    --gateway)
      start_gateway="${2:-}"
      shift
      ;;
    --token)
      start_token="${2:-}"
      shift
      ;;
    --mode)
      start_mode="${2:-}"
      shift
      ;;
    --listen-addr)
      start_listen="${2:-}"
      shift
      ;;
    esac
    shift
  done
}

# YAML single-quoted scalar: the only escape inside one is a doubled quote.
yaml_quote() { printf "'%s'" "${1//\'/\'\'}"; }

if [[ "${1:-}" == "start" ]]; then
  parse_start_flags "$@"

  # Same inference argus itself makes: an explicit --mode wins, otherwise a token
  # with no upstream means this namespace serves the gateway.
  is_gateway=""
  if [[ "$start_mode" == "gateway" ]] ||
    [[ -z "$start_mode" && -z "$start_gateway" && -n "$start_token" ]]; then
    is_gateway=1
  fi

  if [[ -n "$start_token" || -n "$start_gateway" ]]; then
    conf_dir="$NS_DIR/config/argus"
    mkdir -p "$conf_dir"
    {
      echo "# Written by scripts/demo.sh from the flags of the last \`start\` in this"
      echo "# namespace. Delete it to go back to passing --gateway and --token by hand."
      [[ -z "$start_token" ]] || echo "token: $(yaml_quote "$start_token")"
      if [[ -n "$start_gateway" ]]; then
        echo "gateway:"
        echo "  url: $(yaml_quote "$start_gateway")"
      fi
    } >"$conf_dir/config.yaml"
  fi

  # gateway.url is left out of a gateway namespace's config on purpose: argus reads
  # that key as "the upstream I connect to", so `argus start --mode=gateway` refuses
  # a config that sets it. Client commands here need it passed by hand.
  if [[ -n "$is_gateway" ]]; then
    listen_port="${start_listen##*:}"
    [[ -n "$listen_port" ]] || listen_port=8443
    info "namespace '$NS' serves the gateway; its client commands need --gateway=ws://127.0.0.1:$listen_port"
  fi
fi

publish=()
if [[ -n "${DEMO_PUBLISH:-}" ]]; then
  # A bare port binds loopback. The demo gateway accepts a trivial token, so it
  # must not reach the LAN unless the caller names a host deliberately.
  spec="$DEMO_PUBLISH"
  [[ "$spec" == *:* ]] || spec="127.0.0.1:$spec"
  publish=(-p "${spec}:8443")
fi

# -i and -t answer different questions, so they are decided separately.
#
# -i is unconditional: argus prompts on stdin (`lock pin` asks for confirmation),
# and without -i docker attaches no stdin at all, so `echo y | demo.sh a lock pin`
# would read EOF instead of the answer.
#
# -t needs a terminal at both ends. Docker refuses a non-terminal stdin on a
# TTY-enabled container, and -t puts a carriage return on every output line, which
# would break `demo.sh a lock status | grep ...`.
attach_flags=(-i)
if [[ -t 0 && -t 1 ]]; then
  attach_flags+=(-t)
fi

# --rm plus the container name means a namespace is one process at a time, and a
# Ctrl-C leaves nothing behind. exec so signals and the exit code belong to
# docker, which forwards them to argus.
exec docker run --rm \
  "${attach_flags[@]+"${attach_flags[@]}"}" \
  --name "$CONTAINER" \
  --hostname "$CONTAINER" \
  --network "$NETWORK" \
  --label argus.demo=1 \
  --user "$(id -u):$(id -g)" \
  -v "$NS_DIR:$CONTAINER_HOME" \
  -e "HOME=$CONTAINER_HOME" \
  -e "XDG_CONFIG_HOME=$CONTAINER_HOME/config" \
  -e "XDG_STATE_HOME=$CONTAINER_HOME/state" \
  -e "XDG_CACHE_HOME=$CONTAINER_HOME/cache" \
  -e "XDG_RUNTIME_DIR=$CONTAINER_HOME/run" \
  "${publish[@]+"${publish[@]}"}" \
  "$IMAGE" "$@"
