package tunnel

import "context"

// External is a tunnel managed outside argus — a reverse proxy, ingress, or ssh -R that
// already terminates TLS and forwards to the local gateway. argus runs no process; it
// only records the operator-supplied public URL so pairing QRs point at the right host.
type External struct {
	URL string // the gateway's public URL, e.g. wss://argus.example.com
}

func (External) Name() string { return "external" }

// Command reports that there is no child process to run.
func (External) Command(string) (CommandSpec, error) { return CommandSpec{}, ErrNoProcess }

// ExtractURL never matches: the URL is known ahead of time via Prepare.
func (External) ExtractURL(string) (string, bool) { return "", false }

// Prepare implements LifecycleProvider, returning the configured public URL.
func (e External) Prepare(context.Context) (string, error) { return e.URL, nil }
