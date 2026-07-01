package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tunnel"
	"github.com/MunifTanjim/argus/internal/util"
)

// tunnelOptions are the resolved inputs for building a tunnel provider. Kept
// separate from cobra flags so the gates below are unit-testable.
type tunnelOptions struct {
	provider string // --tunnel: "cloudflare" or "cloudflare:<mode>" ("" disables)
	bin      string // provider binary path; "" => look up on PATH (set only in tests)

	cfToken      string // --cloudflare-token (remote)
	cfTunnelName string // --cloudflare-tunnel-name (local; default "argus")
	cfHostname   string // --cloudflare-hostname (local; public hostname argus routes)

	externalURL string // --external-url: gateway's public URL when the tunnel is external

	zrokName string // --zrok-name: reserved name selection ("namespace:name" or "name"; default "argus")

	runGateway bool
	listenAddr string

	logLevel slog.Level // argus log level; mapped to the provider's own --loglevel
}

// providerBinary maps a provider name to the CLI it runs (for PATH lookup).
var providerBinary = map[string]string{"cloudflare": "cloudflared", "zrok": "zrok2"}

// tunnelFlagCompletions are the shell-completion candidates for --tunnel, each a
// "value\tdescription" pair. Keeps the value list out of the (terse) flag help.
func tunnelFlagCompletions() []string {
	return []string{
		"cloudflare\tmanaged Cloudflare tunnel (mode inferred from --cloudflare-* flags)",
		"cloudflare:quick\tephemeral *.trycloudflare.com tunnel, no account",
		"cloudflare:remote\tremotely-managed tunnel (--cloudflare-token)",
		"cloudflare:local\tlocally-managed tunnel (--cloudflare-hostname)",
		"external\texternally managed tunnel; public URL via --external-url",
		"zrok\tnamed public zrok share (--zrok-name; prompts to enable if needed)",
	}
}

// resolveTunnel validates the options and builds the provider plus local origin URL.
// Returns (nil, "", nil) when no tunnel is requested. All failures are pre-flight.
func resolveTunnel(o tunnelOptions) (tunnel.Provider, string, error) {
	if o.provider == "" {
		return nil, "", nil
	}
	if !o.runGateway {
		return nil, "", fmt.Errorf("--tunnel requires gateway mode (run without --gateway and with --token)")
	}
	// --tunnel is "<provider>" or "<provider>:<mode>"; the mode is provider-specific.
	providerName, mode, _ := strings.Cut(o.provider, ":")
	if providerName == "external" {
		return resolveExternalTunnel(mode, o)
	}
	binName, ok := providerBinary[providerName]
	if !ok {
		return nil, "", fmt.Errorf("unknown tunnel provider %q (supported: cloudflare, zrok, external)", providerName)
	}
	switch providerName {
	case "zrok":
		return resolveZrokTunnel(mode, binName, o)
	case "cloudflare":
		cfMode, err := cloudflareMode(mode, o)
		if err != nil {
			return nil, "", err
		}
		bin, err := resolveBin(o.bin, binName)
		if err != nil {
			return nil, "", err
		}
		origin, err := originFromListen(o.listenAddr)
		if err != nil {
			return nil, "", err
		}
		name := o.cfTunnelName
		if cfMode == "local" && name == "" {
			name = "argus" // default name for the tunnel argus creates and owns
		}
		cfLog := cloudflaredLogLevel(o.logLevel)
		// A quick tunnel's public URL is only emitted by cloudflared at info or
		// below, but argus's default (info -> warn) would suppress it — so floor
		// quick mode at info. The INFO noise stays below the fold via ClassifyLine.
		if cfMode == "quick" && cfLog != "debug" {
			cfLog = "info"
		}
		return tunnel.Cloudflare{
			Bin:      bin,
			Token:    o.cfToken,
			Tunnel:   name,
			Hostname: o.cfHostname,
			LogLevel: cfLog,
		}, origin, nil
	default:
		// unreachable: providerBinary already gated unknown names
		return nil, "", fmt.Errorf("unknown tunnel provider %q", providerName)
	}
}

// resolveZrokTunnel builds the Zrok provider (zrok2). zrok's only mode is the named public
// share, so the suffix must be empty or the "reserved" alias. --zrok-name carries the
// reserved name (auto-created in Prepare); the environment is enabled in pre-flight.
func resolveZrokTunnel(mode, binName string, o tunnelOptions) (tunnel.Provider, string, error) {
	if mode != "" && mode != "reserved" {
		return nil, "", fmt.Errorf("--tunnel zrok takes no mode (or zrok:reserved), got %q", mode)
	}
	if o.cfToken != "" || o.cfHostname != "" || o.cfTunnelName != "" || o.externalURL != "" {
		return nil, "", fmt.Errorf("--cloudflare-*/--external-url flags are not valid with --tunnel zrok")
	}
	name := o.zrokName
	if name == "" {
		name = "argus" // default reserved name argus creates and owns
	}
	bin, err := resolveBin(o.bin, binName)
	if err != nil {
		return nil, "", err
	}
	origin, err := originFromListen(o.listenAddr)
	if err != nil {
		return nil, "", err
	}
	return &tunnel.Zrok{Bin: bin, Selection: name}, origin, nil
}

// zrokEnabled reports whether this host's zrok environment is enabled — whether
// `zrok2 overview` can reach the account, the reliable cross-OS signal. A package var for
// test injection.
var zrokEnabled = func(ctx context.Context, bin string) bool {
	return shell.NewCommandContext(ctx, bin, "overview", "--json").Run() == nil
}

// ensureZrokEnabled makes sure the zrok environment is enabled before the share runs.
// When it isn't, it prompts for an account token at a terminal and enables with it, else
// fails fast. Mirrors ensureCloudflareLogin (interactive setup belongs in pre-flight, not
// the supervisor goroutine).
func ensureZrokEnabled(ctx context.Context, bin string, interactive bool) error {
	if zrokEnabled(ctx, bin) {
		return nil
	}
	if !interactive {
		return fmt.Errorf("zrok environment not enabled: run 'zrok2 enable <token>' first")
	}
	token, err := promptZrokToken()
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("no zrok account token provided")
	}
	shell.StdErrF("argus start: enabling zrok environment…\n")
	cmd := shell.NewCommandContext(ctx, bin, "enable", token, "--headless")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zrok2 enable: %w: %s", err, cmd.StdErr().TrimSpace())
	}
	return nil
}

// promptZrokToken reads a zrok account token from the terminal without echoing it.
func promptZrokToken() (string, error) {
	shell.StdErrF("argus start: zrok environment not enabled. Enter your zrok account token (empty to abort): ")
	tok, err := term.ReadPassword(int(os.Stdin.Fd()))
	shell.StdErrLn()
	if err != nil {
		return "", fmt.Errorf("read zrok token: %w", err)
	}
	return strings.TrimSpace(string(tok)), nil
}

// resolveExternalTunnel builds the External provider for a tunnel argus does not run
// itself (a reverse proxy, ingress, or ssh -R). It takes no mode; --external-url carries
// the gateway's public URL and is the only required input.
func resolveExternalTunnel(mode string, o tunnelOptions) (tunnel.Provider, string, error) {
	if mode != "" {
		return nil, "", fmt.Errorf("--tunnel external takes no mode suffix (got %q)", mode)
	}
	if o.cfToken != "" || o.cfHostname != "" || o.cfTunnelName != "" {
		return nil, "", fmt.Errorf("--cloudflare-* flags are not valid with --tunnel external")
	}
	if o.externalURL == "" {
		return nil, "", fmt.Errorf("an external tunnel requires --external-url ws(s)://host (the gateway's public URL)")
	}
	u, err := url.Parse(o.externalURL)
	if err != nil {
		return nil, "", fmt.Errorf("parse --external-url %q: %w", o.externalURL, err)
	}
	switch u.Scheme {
	case "ws", "wss", "http", "https":
	default:
		return nil, "", fmt.Errorf("--external-url %q must use scheme ws, wss, http, or https", o.externalURL)
	}
	if u.Host == "" {
		return nil, "", fmt.Errorf("--external-url %q must include a host", o.externalURL)
	}
	// A reverse proxy may expose the gateway under a base path (/client and /node append
	// to it). Query/fragment/userinfo have no place here and would leak into pairing QRs.
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, "", fmt.Errorf("--external-url %q must be scheme://host[/base-path] with no query, fragment, or user info (it is echoed into pairing QRs)", o.externalURL)
	}
	// No local origin: argus runs no process for an external tunnel.
	return tunnel.External{URL: o.externalURL}, "", nil
}

// cloudflaredLogLevel maps argus's log level to cloudflared's --loglevel, offset at
// info so cloudflared's chatty info output is filtered at the source.
//
//	argus              -> cloudflared
//	trace, debug       -> debug
//	info, warn         -> warn
//	error, fatal       -> error
func cloudflaredLogLevel(l slog.Level) string {
	switch {
	case l <= slog.LevelDebug:
		return "debug"
	case l <= slog.LevelWarn:
		return "warn"
	default:
		return "error"
	}
}

// resolveBin returns bin if set (a test seam), else looks binName up on PATH.
func resolveBin(bin, binName string) (string, error) {
	if bin != "" {
		return bin, nil
	}
	if !shell.ExecutableExists(binName) {
		return "", fmt.Errorf("%s not found on PATH: install it or add it to PATH", binName)
	}
	return shell.ExecutablePath(binName), nil
}

// cloudflareMode resolves and validates the tunnel mode. explicit is the suffix from
// --tunnel cloudflare:<mode> ("" => infer from which --cloudflare-* params are set).
// Returns "quick", "remote", or "local", or a validation error.
func cloudflareMode(explicit string, o tunnelOptions) (string, error) {
	hasRemote := o.cfToken != ""
	hasLocal := o.cfHostname != "" || o.cfTunnelName != ""

	mode := explicit
	if mode == "" { // no suffix: infer the mode from the params
		switch {
		case hasRemote && hasLocal:
			return "", fmt.Errorf("--cloudflare-token (remote tunnel) cannot be combined with --cloudflare-hostname/--cloudflare-tunnel-name (local tunnel)")
		case hasRemote:
			mode = "remote"
		case hasLocal:
			mode = "local"
		default:
			mode = "quick"
		}
	}

	switch mode {
	case "quick":
		if hasRemote || hasLocal {
			return "", fmt.Errorf("a quick Cloudflare tunnel takes no other --cloudflare-* flags; use cloudflare:remote or cloudflare:local")
		}
	case "remote":
		if !hasRemote {
			return "", fmt.Errorf("a remote Cloudflare tunnel requires --cloudflare-token")
		}
		if hasLocal {
			return "", fmt.Errorf("--cloudflare-hostname/--cloudflare-tunnel-name are only valid with a local tunnel")
		}
	case "local":
		if o.cfHostname == "" {
			return "", fmt.Errorf("a local Cloudflare tunnel requires --cloudflare-hostname: argus routes a DNS record for the hostname to the tunnel")
		}
		if hasRemote {
			return "", fmt.Errorf("--cloudflare-token is only valid with a remote tunnel")
		}
		// The origin certificate (cloudflared tunnel login) is an environmental
		// prerequisite handled at startup by ensureCloudflareLogin, not here.
	default:
		return "", fmt.Errorf("unknown cloudflare tunnel type %q (use cloudflare:quick, cloudflare:remote, or cloudflare:local)", mode)
	}
	return mode, nil
}

// cloudflareCertPath resolves the origin cert path as cloudflared does:
// $TUNNEL_ORIGIN_CERT (isDefault=false), else ~/.cloudflared/cert.pem (isDefault=true).
// Child cloudflared processes inherit the env, so they resolve the same path.
func cloudflareCertPath() (path string, isDefault bool, err error) {
	if c := os.Getenv("TUNNEL_ORIGIN_CERT"); c != "" {
		return c, false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("locate cloudflared origin certificate: %w", err)
	}
	return filepath.Join(home, ".cloudflared", "cert.pem"), true, nil
}

// ensureCloudflareLogin ensures a locally-managed tunnel has its origin cert (from
// `cloudflared tunnel login`). If missing, runs the browser-based login when interactive
// and on the default path, else fails fast. No-op for quick/remote tunnels (cf.Tunnel == "").
func ensureCloudflareLogin(ctx context.Context, cf tunnel.Cloudflare, interactive bool) error {
	if cf.Tunnel == "" {
		return nil
	}
	cert, isDefault, err := cloudflareCertPath()
	if err != nil {
		return err
	}
	if exists, _ := util.FileExists(cert); exists {
		return nil // already logged in
	}
	// Only auto-login for the default path: that's where `tunnel login` writes; a
	// custom TUNNEL_ORIGIN_CERT means the user is managing the cert themselves.
	if !interactive || !isDefault {
		return fmt.Errorf("cloudflare locally-managed tunnel needs an origin certificate at %s: run 'cloudflared tunnel login' first (or set TUNNEL_ORIGIN_CERT)", cert)
	}
	shell.StdErrF("argus start: no Cloudflare origin certificate at %s; launching 'cloudflared tunnel login' (a browser will open)…\n", cert)
	login := shell.NewCommandContext(ctx, cf.Bin, "tunnel", "login").
		WithStdIn(os.Stdin).WithStdOut(os.Stderr).WithStdErr(os.Stderr)
	if err := login.Run(); err != nil {
		return fmt.Errorf("cloudflared tunnel login: %w", err)
	}
	if exists, _ := util.FileExists(cert); !exists {
		return fmt.Errorf("cloudflared tunnel login completed but no certificate found at %s", cert)
	}
	return nil
}

// originFromListen turns a listen address (e.g. ":8443") into the loopback origin URL
// the tunnel points at (plain http: the edge terminates TLS).
func originFromListen(listenAddr string) (string, error) {
	_, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", fmt.Errorf("parse --listen-addr %q: %w", listenAddr, err)
	}
	return "http://127.0.0.1:" + port, nil
}
