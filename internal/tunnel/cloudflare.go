package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/MunifTanjim/argus/internal/shell"
)

// quickURLRe matches the public URL cloudflared prints for a quick tunnel.
var quickURLRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// Cloudflare runs cloudflared in one of three modes, selected by which fields
// are set (resolveTunnel gates them mutually exclusive):
//
//   - quick: neither Token nor Tunnel set. Ephemeral, unauthenticated tunnel
//     with a printed *.trycloudflare.com URL.
//   - remotely-managed: Token set. A tunnel whose hostname/ingress is configured
//     on Cloudflare's side; run with --token.
//   - locally-managed: Tunnel set. argus creates the tunnel (if absent), routes
//     a DNS record for Hostname to it, then runs it. Credentials live in
//     ~/.cloudflared/<UUID>.json; argus owns the lifecycle (create + route).
type Cloudflare struct {
	Bin      string // path to the cloudflared binary
	Token    string // remotely-managed tunnel token
	Tunnel   string // locally-managed tunnel name (selects local mode)
	Hostname string // public hostname argus routes to the locally-managed tunnel
	LogLevel string // cloudflared --loglevel (debug/info/warn/error/fatal); "" omits it

	runner cmdRunner // nil => defaultRunner (real exec); overridable in tests
}

func (c Cloudflare) Name() string { return "cloudflare" }

func (c Cloudflare) Command(origin string) (CommandSpec, error) {
	// Global flags (incl. --loglevel) go after "tunnel" and before the subcommand.
	base := []string{"tunnel", "--no-autoupdate"}
	if c.LogLevel != "" {
		base = append(base, "--loglevel", c.LogLevel)
	}
	switch {
	case c.Tunnel != "":
		// Locally-managed: a catch-all ingress (--url) to the loopback gateway; the
		// credentials file is resolved by name from ~/.cloudflared.
		return CommandSpec{Path: c.Bin, Args: append(base, "run", "--url", origin, c.Tunnel)}, nil
	case c.Token != "":
		return CommandSpec{Path: c.Bin, Args: append(base, "run", "--token", c.Token)}, nil
	default:
		return CommandSpec{Path: c.Bin, Args: append(base, "--url", origin)}, nil
	}
}

// cfLevelByToken maps cloudflared's text-log level tokens to slog levels.
// cloudflared's own INFO is chatty heartbeat/connection noise, so it maps to
// Debug (below argus's info threshold). This lets a quick tunnel run cloudflared
// at --loglevel info — needed because its public-URL banner is printed at info —
// without that banner's sibling lines surfacing as argus info. The URL itself is
// pulled out by ExtractURL and printed by the supervisor's report callback
// regardless of log level. FTL maps to Error rather than a fatal level:
// classification only sets the log level, and we never want a tunnel line to
// escalate beyond Error.
var cfLevelByToken = map[string]slog.Level{
	"DBG": slog.LevelDebug,
	"INF": slog.LevelDebug,
	"WRN": slog.LevelWarn,
	"ERR": slog.LevelError,
	"FTL": slog.LevelError,
}

// ClassifyLine implements LineClassifier. cloudflared's text format is
// "<timestamp> <LVL> <message>", so the level token is among the first fields; lines
// without a recognizable token (continuations, panics) default to Info.
func (c Cloudflare) ClassifyLine(line string) slog.Level {
	for i, f := range strings.Fields(line) {
		if i >= 3 {
			break
		}
		if lvl, ok := cfLevelByToken[f]; ok {
			return lvl
		}
	}
	return slog.LevelInfo
}

func (c Cloudflare) ExtractURL(line string) (string, bool) {
	if c.Token != "" || c.Tunnel != "" {
		return "", false // configured/known hostname is not printed by cloudflared
	}
	if m := quickURLRe.FindString(line); m != "" {
		return m, true
	}
	return "", false
}

// Prepare implements LifecycleProvider. For a locally-managed tunnel it ensures
// the tunnel exists (creating it if absent), routes a DNS record for Hostname to
// it, and returns the resulting public URL. Quick and remotely-managed modes
// need no setup and return ("", nil).
func (c Cloudflare) Prepare(ctx context.Context) (string, error) {
	if c.Tunnel == "" {
		return "", nil
	}

	exists, err := c.tunnelExists(ctx)
	if err != nil {
		return "", err
	}
	if !exists {
		if _, stderr, err := c.exec(ctx, c.tunnelArgs("create", c.Tunnel)...); err != nil {
			return "", fmt.Errorf("create tunnel %q: %w: %s", c.Tunnel, err, bytes.TrimSpace(stderr))
		}
	}

	// --overwrite-dns makes the route idempotent across restarts (and points an
	// existing record at this tunnel).
	if _, stderr, err := c.exec(ctx, c.tunnelArgs("route", "dns", "--overwrite-dns", c.Tunnel, c.Hostname)...); err != nil {
		return "", fmt.Errorf("route dns %s -> %s: %w: %s", c.Hostname, c.Tunnel, err, bytes.TrimSpace(stderr))
	}

	return "https://" + c.Hostname, nil
}

// tunnelExists reports whether a tunnel named c.Tunnel already exists, by parsing
// `cloudflared tunnel list --output json` and matching on name.
func (c Cloudflare) tunnelExists(ctx context.Context) (bool, error) {
	stdout, stderr, err := c.exec(ctx, c.tunnelArgs("list", "--output", "json")...)
	if err != nil {
		return false, fmt.Errorf("list tunnels: %w: %s", err, bytes.TrimSpace(stderr))
	}
	var tunnels []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(stdout, &tunnels); err != nil {
		return false, fmt.Errorf("parse tunnel list: %w", err)
	}
	for _, t := range tunnels {
		if t.Name == c.Tunnel {
			return true, nil
		}
	}
	return false, nil
}

// tunnelArgs builds args for a `cloudflared tunnel <sub...>` invocation. The
// origin certificate (needed by create/route/list) is resolved by cloudflared
// itself from --origin-cert / TUNNEL_ORIGIN_CERT / its default path; the child
// inherits argus's environment, so no flag is injected here.
func (c Cloudflare) tunnelArgs(sub ...string) []string {
	return append([]string{"tunnel"}, sub...)
}

// cmdRunner runs a one-shot command to completion and returns its stdout and
// stderr separately. Keeping them apart matters: cloudflared writes machine output
// (e.g. `tunnel list --output json`) to stdout but logs to stderr, so merging them
// corrupts parsed output. The real implementation execs c.Bin; tests inject a fake.
type cmdRunner func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)

func (c Cloudflare) exec(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	run := c.runner
	if run == nil {
		run = defaultRunner
	}
	return run(ctx, c.Bin, args...)
}

func defaultRunner(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := shell.NewCommandContext(ctx, name, args...)
	err = cmd.Run()
	return []byte(cmd.StdOut().String()), []byte(cmd.StdErr().String()), err
}
