package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/util"
)

// quickURLRe matches the public URL cloudflared prints for a quick tunnel.
var quickURLRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// remoteConfigRe captures the Go-quoted config blob a remotely-managed cloudflared
// logs on connect and on every config change:
//
//	<ts> INF Updated to new configuration config="{\"ingress\":[…]}" version=10
//
// The body pattern skips escaped characters, so the match ends at the first
// *unescaped* quote — correct whether or not another quoted field follows, and for
// a hostname or path that itself contains a quote.
var remoteConfigRe = regexp.MustCompile(`config="((?:[^"\\]|\\.)*)"`)

// Cloudflare runs cloudflared in one of three modes by which fields are set:
//
//   - quick: neither Token nor Tunnel set. Ephemeral unauthenticated tunnel with a
//     printed *.trycloudflare.com URL.
//   - remotely-managed: Token set. Hostname/ingress configured on Cloudflare's side;
//     run with --token. cloudflared logs the ingress it pulled, so the public
//     hostname is scraped from its output (see remoteIngressHostname).
//   - locally-managed: Tunnel set. argus creates the tunnel (if absent), routes a DNS
//     record for Hostname to it, then runs it (credentials in ~/.cloudflared/<UUID>.json).
type Cloudflare struct {
	Bin      string // path to the cloudflared binary
	Token    string // remotely-managed tunnel token
	Tunnel   string // locally-managed tunnel name (selects local mode)
	Hostname string // public hostname argus routes to the locally-managed tunnel
	LogLevel string // cloudflared --loglevel (debug/info/warn/error/fatal); "" omits it
	CredsDir string // dir cloudflared reads <UUID>.json creds from; "" skips the local-creds check

	runner cmdRunner // nil => defaultRunner (real exec); overridable in tests
}

func (c Cloudflare) Name() string { return "cloudflare" }

func (c Cloudflare) Command(origin string) (CommandSpec, error) {
	// Global flags (incl. --loglevel) go after "tunnel" and before the subcommand.
	base := []string{"tunnel", "--no-autoupdate"}
	if lvl := c.effectiveLogLevel(); lvl != "" {
		base = append(base, "--loglevel", lvl)
	}
	switch {
	case c.Tunnel != "":
		// Locally-managed: catch-all ingress (--url) to the gateway; credentials
		// resolved by name from ~/.cloudflared.
		return CommandSpec{Path: c.Bin, Args: append(base, "run", "--url", origin, c.Tunnel)}, nil
	case c.Token != "":
		return CommandSpec{Path: c.Bin, Args: append(base, "run", "--token", c.Token)}, nil
	default:
		return CommandSpec{Path: c.Bin, Args: append(base, "--url", origin)}, nil
	}
}

// effectiveLogLevel floors quick and remotely-managed modes at info: they scrape their
// public URL from cloudflared output (see ExtractURL), which is emitted only at info or
// below, so a caller asking for warn/error would never get a URL. ClassifyLine keeps the
// resulting INF noise below argus's threshold. Locally-managed mode learns its hostname
// from Prepare, so it keeps whatever level was asked for.
func (c Cloudflare) effectiveLogLevel() string {
	if c.Tunnel == "" && c.LogLevel != "" && c.LogLevel != "debug" {
		return "info"
	}
	return c.LogLevel
}

// cfLevelByToken maps cloudflared's log-level tokens to slog levels. INF → Debug:
// cloudflared's INFO is chatty noise, and the scrape modes must run at --loglevel info
// (see effectiveLogLevel), so demote it below argus's info threshold. FTL → Error, never
// fatal: classification only sets a level.
var cfLevelByToken = map[string]slog.Level{
	"DBG": slog.LevelDebug,
	"INF": slog.LevelDebug,
	"WRN": slog.LevelWarn,
	"ERR": slog.LevelError,
	"FTL": slog.LevelError,
}

// ClassifyLine implements LineClassifier. cloudflared's format is
// "<timestamp> <LVL> <message>", so the level token is among the first fields;
// lines without one (continuations, panics) default to Info.
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
	switch {
	case c.Tunnel != "":
		return "", false // locally-managed: Hostname is known ahead of time, reported by Prepare
	case c.Token != "":
		return remoteIngressHostname(line)
	default:
		m := quickURLRe.FindString(line)
		return m, m != ""
	}
}

// remoteIngressHostname pulls the public URL from the ingress a remotely-managed
// cloudflared logs at INF ("Updated to new configuration"). Returns the first rule with
// a usable hostname; the trailing catch-all (http_status:404) has none, and a tunnel
// routing several hostnames yields the first — argus can't pick between them.
func remoteIngressHostname(line string) (string, bool) {
	m := remoteConfigRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	// cloudflared escapes the blob Go-style, so unquote before parsing it as JSON.
	raw, err := strconv.Unquote(`"` + m[1] + `"`)
	if err != nil {
		return "", false
	}
	var cfg struct {
		Ingress []struct {
			Hostname string `json:"hostname"`
			Path     string `json:"path"`
		} `json:"ingress"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return "", false
	}
	for _, in := range cfg.Ingress {
		// A wildcard hostname doesn't resolve, and a path-scoped rule doesn't route the
		// bare hostname. Either would publish a URL that pairing can't reach — and unlike
		// every other failure here it would report success, so nothing downstream notices.
		if in.Hostname == "" || strings.Contains(in.Hostname, "*") || in.Path != "" {
			continue
		}
		return "https://" + in.Hostname, true
	}
	return "", false
}

// Prepare implements LifecycleProvider. For a locally-managed tunnel it ensures the
// tunnel exists, routes a DNS record for Hostname to it, and returns the public URL.
// Quick and remotely-managed modes need no setup and return ("", nil).
func (c Cloudflare) Prepare(ctx context.Context) (string, error) {
	if c.Tunnel == "" {
		return "", nil
	}

	exists, id, err := c.tunnelExists(ctx)
	if err != nil {
		return "", err
	}
	if !exists {
		if _, stderr, err := c.exec(ctx, c.tunnelArgs("create", c.Tunnel)...); err != nil {
			return "", fmt.Errorf("create tunnel %q: %w: %s", c.Tunnel, err, bytes.TrimSpace(stderr))
		}
	} else if err := c.checkCredentials(id); err != nil {
		return "", err
	}

	// --overwrite-dns makes the route idempotent across restarts.
	if _, stderr, err := c.exec(ctx, c.tunnelArgs("route", "dns", "--overwrite-dns", c.Tunnel, c.Hostname)...); err != nil {
		return "", fmt.Errorf("route dns %s -> %s: %w: %s", c.Hostname, c.Tunnel, err, bytes.TrimSpace(stderr))
	}

	return "https://" + c.Hostname, nil
}

// tunnelExists reports whether a tunnel named c.Tunnel exists, returning its UUID when it does.
func (c Cloudflare) tunnelExists(ctx context.Context) (bool, string, error) {
	stdout, stderr, err := c.exec(ctx, c.tunnelArgs("list", "--output", "json")...)
	if err != nil {
		return false, "", fmt.Errorf("list tunnels: %w: %s", err, bytes.TrimSpace(stderr))
	}
	var tunnels []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(stdout, &tunnels); err != nil {
		return false, "", fmt.Errorf("parse tunnel list: %w", err)
	}
	for _, t := range tunnels {
		if t.Name == c.Tunnel {
			return true, t.ID, nil
		}
	}
	return false, "", nil
}

// checkCredentials fails fast when the tunnel exists on the account but its
// credentials file is absent here — otherwise cloudflared restart-loops on
// "credentials file not found".
func (c Cloudflare) checkCredentials(id string) error {
	if c.CredsDir == "" || id == "" {
		return nil
	}
	if os.Getenv("TUNNEL_CRED_CONTENTS") != "" {
		return nil // inline creds; no file to check
	}
	path := os.Getenv("TUNNEL_CRED_FILE")
	if path == "" {
		path = cloudflaredCredentialsFile(c.CredsDir)
	}
	if path == "" {
		path = filepath.Join(c.CredsDir, id+".json")
	} else if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if exists, err := util.FileExists(path); err != nil {
		return fmt.Errorf("stat cloudflared credentials %s: %w", path, err)
	} else if exists {
		return nil
	}
	return fmt.Errorf(
		"cloudflare tunnel %q (UUID %s) is registered on the account but its credentials file %s is missing on this machine. "+
			"Copy the credentials JSON from the machine that originally ran `cloudflared tunnel create`, "+
			"or run `cloudflared tunnel delete %s` and restart argus to recreate it",
		c.Tunnel, id, path, c.Tunnel,
	)
}

// cloudflaredCredentialsFile returns the credentials-file path from cloudflared's
// config in dir, or "" if unset. Line scan, not a full YAML parse.
func cloudflaredCredentialsFile(dir string) string {
	for _, name := range []string{"config.yml", "config.yaml"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "credentials-file:"); ok {
				return strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
	}
	return ""
}

// tunnelArgs builds args for a `cloudflared tunnel <sub...>` call. cloudflared
// resolves the origin cert itself (from --origin-cert / TUNNEL_ORIGIN_CERT / default
// path); the child inherits argus's env, so no flag is injected here.
func (c Cloudflare) tunnelArgs(sub ...string) []string {
	return append([]string{"tunnel"}, sub...)
}

// cmdRunner runs a one-shot command, returning stdout and stderr separately:
// cloudflared writes machine output (e.g. list --output json) to stdout but logs to
// stderr, so merging them corrupts parsing. Real impl execs c.Bin; tests inject a fake.
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
