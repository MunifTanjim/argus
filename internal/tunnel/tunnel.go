// Package tunnel manages a tunnel-provider CLI (e.g. cloudflared) as a child
// process, exposing the local gateway to the internet without port-forwarding or a
// public IP. Each backend implements Provider; Supervisor owns the shared
// process lifecycle (start, URL scan, restart, kill).
package tunnel

import (
	"context"
	"errors"
	"log/slog"
)

// ErrNoProcess is returned from Command by a provider that runs no child process (an
// externally-managed tunnel). Supervisor reports any Prepare URL, then idles until ctx
// is cancelled instead of exec/restart.
var ErrNoProcess = errors.New("tunnel: provider runs no process")

// CommandSpec is the external command a provider runs to open its tunnel.
type CommandSpec struct {
	Path string
	Args []string
}

// Provider describes one tunnel backend. Adding a backend (ngrok, Tailscale
// Funnel, …) means adding one file with a Provider implementation.
type Provider interface {
	// Name is the backend's identifier, e.g. "cloudflare".
	Name() string
	// Command returns the CLI invocation that exposes origin (the local gateway URL).
	Command(origin string) (CommandSpec, error)
	// ExtractURL returns the public URL if line announces one, else ("", false).
	ExtractURL(line string) (string, bool)
}

// LineClassifier is an optional Provider extension mapping an output line to its
// slog level, so a backend's own severity (e.g. cloudflared WRN/ERR) drives the
// argus log level instead of everything landing at Info. Default is Info.
type LineClassifier interface {
	ClassifyLine(line string) slog.Level
}

// LifecycleProvider is an optional Provider extension for backends needing one-time
// setup before the run, and/or knowing their public URL in advance (vs. scraping it
// via ExtractURL). Supervisor calls Prepare once before the run loop.
type LifecycleProvider interface {
	// Prepare runs once before the run loop, returning the public URL when known
	// ahead of time ("" when it must be scraped from output).
	Prepare(ctx context.Context) (publicURL string, err error)
}
