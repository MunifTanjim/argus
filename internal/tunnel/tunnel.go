// Package tunnel manages a tunnel-provider CLI (e.g. cloudflared) as a child
// process, exposing the local gateway to the internet without port-forwarding or a
// public IP. Each backend implements Provider; Supervisor owns the shared
// process lifecycle (start, URL scan, restart, kill).
package tunnel

import (
	"context"
	"log/slog"
)

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

// LineClassifier is an optional Provider extension: it maps a line of the backend's
// output to the slog level it should be logged at. Supervisor uses it so a backend's
// own severity (e.g. cloudflared's WRN/ERR) drives the argus log level, instead of
// every line landing at Info. Providers that don't implement it default to Info.
type LineClassifier interface {
	ClassifyLine(line string) slog.Level
}

// LifecycleProvider is an optional Provider extension for backends that need a
// one-time setup step before the long-lived run, and/or know their public URL
// in advance (rather than scraping it from process output via ExtractURL).
// Supervisor calls Prepare once, before starting the run loop.
type LifecycleProvider interface {
	// Prepare runs once before the run loop. It returns the public URL when the
	// backend knows it ahead of time ("" when it must be scraped from output).
	Prepare(ctx context.Context) (publicURL string, err error)
}
