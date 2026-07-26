# syntax=docker/dockerfile:1
#
# Multi-stage build for the argus blind-gateway E2E test harness.
# Build context is the REPO ROOT (see docker/docker-compose.yml) because
# assets.go //go:embed's app/android/.../ic_launcher.png — the build fails if
# that file isn't in context.
#
# Targets:
#   gateway  — tiny image, just the argus binary (the blind relay).
#   node     — adds tmux + real Claude Code so a node discovers live agents.

# ---- builder -----------------------------------------------------------------
FROM golang:1.26-alpine AS builder
# git only for module fetching over VCS if needed; the version is passed in as a
# build arg so .git is not required in the context.
RUN apk add --no-cache git
ARG VERSION=docker-dev
WORKDIR /src
# Warm the module cache first for faster rebuilds.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# CGO off: modernc.org/sqlite is pure Go, so the binary is static and runs on
# both Alpine (musl) and Debian.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /out/argus ./cmd/argus

# ---- gateway (also the base for node) ----------------------------------------
FROM alpine:3 AS gateway
# ca-certificates: outbound TLS (e.g. web-push); tini: clean PID 1 signal handling.
RUN apk add --no-cache ca-certificates tini \
    && adduser -D -h /home/argus argus
COPY --from=builder /out/argus /usr/local/bin/argus
USER argus
WORKDIR /home/argus
ENV HOME=/home/argus
EXPOSE 8443
ENTRYPOINT ["/sbin/tini", "--", "argus"]

# ---- node (gateway + real agent tooling) -------------------------------------
FROM gateway AS node
USER root
# tmux: agent sessions live here. procps: discovery shells out to BSD-style
# `ps -ax -o pid= -o tty= ...` (busybox ps rejects these flags). git: repo-name
# detection. nodejs/npm: install the Claude Code CLI. libc6-compat: musl shim in
# case the claude CLI ships a glibc-linked helper (see docker/README.md fallback).
RUN apk add --no-cache tmux procps git nodejs npm libc6-compat \
    && npm install -g @anthropic-ai/claude-code \
    && adduser -D -h /home/agent agent
USER agent
WORKDIR /home/agent
ENV HOME=/home/agent
