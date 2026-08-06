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
  DEMO_PUBLISH     host port to publish to the container's 8443 (gateway only)

Examples:
  ${BASH_SOURCE[0]##*/} gw start --mode=gateway --token=t --listen-addr=:8443
  ${BASH_SOURCE[0]##*/} a start --gateway=ws://argus-demo-gw:8443 --token=t --id=node-a
  ${BASH_SOURCE[0]##*/} a lock status
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
  [[ -z "${DEMO_NO_BUILD:-}" ]] || die "image $IMAGE missing and DEMO_NO_BUILD is set"
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

publish=()
[[ -z "${DEMO_PUBLISH:-}" ]] || publish=(-p "${DEMO_PUBLISH}:8443")

# --rm plus the container name means a namespace is one process at a time, and a
# Ctrl-C leaves nothing behind. exec so signals and the exit code belong to
# docker, which forwards them to argus.
exec docker run --rm -it \
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
  "${publish[@]}" \
  "$IMAGE" "$@"
