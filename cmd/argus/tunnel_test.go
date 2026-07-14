package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/tunnel"
)

// existingCert writes a stand-in origin certificate and returns its path.
func existingCert(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, []byte("cert"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return path
}

func baseOpts() tunnelOptions {
	return tunnelOptions{
		provider:   "cloudflare",
		bin:        "/usr/bin/cloudflared", // non-empty => no PATH lookup
		runGateway: true,
		listenAddr: ":8443",
	}
}

func TestResolveTunnelDisabled(t *testing.T) {
	o := baseOpts()
	o.provider = ""
	p, origin, err := resolveTunnel(o)
	if err != nil || p != nil || origin != "" {
		t.Fatalf("disabled: got (%v, %q, %v)", p, origin, err)
	}
}

func TestResolveTunnelHappyQuick(t *testing.T) {
	p, origin, err := resolveTunnel(baseOpts())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p == nil || p.Name() != "cloudflare" {
		t.Fatalf("provider = %v", p)
	}
	if origin != "http://127.0.0.1:8443" {
		t.Errorf("origin = %q", origin)
	}
}

func TestResolveTunnelRequiresGatewayMode(t *testing.T) {
	o := baseOpts()
	o.runGateway = false
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "gateway mode") {
		t.Fatalf("err = %v, want mention of gateway mode", err)
	}
}

func TestResolveTunnelUnknownProvider(t *testing.T) {
	o := baseOpts()
	o.provider = "wireguard"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "wireguard") {
		t.Fatalf("err = %v, want mention of provider name", err)
	}
}

// --- mode inferred from params (bare "cloudflare") ---

func TestResolveTunnelInferRemote(t *testing.T) {
	o := baseOpts()
	o.cfToken = "tok123"
	p, _, err := resolveTunnel(o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cf := p.(tunnel.Cloudflare); cf.Token != "tok123" || cf.Tunnel != "" {
		t.Errorf("provider = %+v, want remote (token set, no tunnel name)", cf)
	}
}

func TestResolveTunnelInferLocalDefaultsName(t *testing.T) {
	o := baseOpts()
	o.cfHostname = "argus.example.com"
	p, origin, err := resolveTunnel(o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cf, ok := p.(tunnel.Cloudflare)
	if !ok {
		t.Fatalf("provider type = %T", p)
	}
	if cf.Tunnel != "argus" {
		t.Errorf("tunnel name = %q, want default argus", cf.Tunnel)
	}
	if cf.Hostname != "argus.example.com" {
		t.Errorf("hostname = %q", cf.Hostname)
	}
	if origin != "http://127.0.0.1:8443" {
		t.Errorf("origin = %q", origin)
	}
}

func TestResolveTunnelInferLocalCustomName(t *testing.T) {
	o := baseOpts()
	o.cfTunnelName = "desktop"
	o.cfHostname = "argus.example.com"
	p, _, err := resolveTunnel(o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cf := p.(tunnel.Cloudflare); cf.Tunnel != "desktop" {
		t.Errorf("tunnel name = %q, want desktop", cf.Tunnel)
	}
}

func TestResolveTunnelInferLocalRequiresHostname(t *testing.T) {
	o := baseOpts()
	o.cfTunnelName = "desktop" // local intent, but no hostname
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "hostname") {
		t.Fatalf("err = %v, want hostname requirement", err)
	}
}

func TestResolveTunnelRejectsRemoteAndLocal(t *testing.T) {
	o := baseOpts()
	o.cfToken = "tok"
	o.cfHostname = "argus.example.com"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("err = %v, want remote+local conflict error", err)
	}
}

// --- explicit mode suffix (cloudflare:<mode>) ---

func TestResolveTunnelExplicitRemote(t *testing.T) {
	o := baseOpts()
	o.provider = "cloudflare:remote"
	o.cfToken = "tok123"
	if _, _, err := resolveTunnel(o); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

func TestResolveTunnelExplicitRemoteRequiresToken(t *testing.T) {
	o := baseOpts()
	o.provider = "cloudflare:remote"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "--cloudflare-token") {
		t.Fatalf("err = %v, want token requirement", err)
	}
}

func TestResolveTunnelExplicitQuickRejectsParams(t *testing.T) {
	o := baseOpts()
	o.provider = "cloudflare:quick"
	o.cfToken = "tok"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "quick") {
		t.Fatalf("err = %v, want quick-takes-no-flags error", err)
	}
}

func TestResolveTunnelExplicitLocal(t *testing.T) {
	o := baseOpts()
	o.provider = "cloudflare:local"
	o.cfHostname = "argus.example.com"
	p, _, err := resolveTunnel(o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cf := p.(tunnel.Cloudflare); cf.Tunnel != "argus" {
		t.Errorf("tunnel name = %q, want default argus", cf.Tunnel)
	}
}

func TestResolveTunnelUnknownType(t *testing.T) {
	o := baseOpts()
	o.provider = "cloudflare:bogus"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("err = %v, want unknown-type error", err)
	}
}

// --- external (tunnel argus does not run) ---

func TestResolveTunnelExternalHappy(t *testing.T) {
	o := baseOpts()
	o.provider = "external"
	o.externalURL = "wss://argus.example.com"
	p, origin, err := resolveTunnel(o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ext, ok := p.(tunnel.External)
	if !ok {
		t.Fatalf("provider type = %T", p)
	}
	if ext.URL != "wss://argus.example.com" {
		t.Errorf("URL = %q", ext.URL)
	}
	if origin != "" {
		t.Errorf("origin = %q, want empty (no local process)", origin)
	}
}

func TestResolveTunnelExternalRequiresURL(t *testing.T) {
	o := baseOpts()
	o.provider = "external"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "--external-url") {
		t.Fatalf("err = %v, want --external-url requirement", err)
	}
}

func TestResolveTunnelExternalRequiresGatewayMode(t *testing.T) {
	o := baseOpts()
	o.provider = "external"
	o.externalURL = "wss://argus.example.com"
	o.runGateway = false
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "gateway mode") {
		t.Fatalf("err = %v, want gateway-mode requirement", err)
	}
}

func TestResolveTunnelExternalRejectsMode(t *testing.T) {
	o := baseOpts()
	o.provider = "external:quick"
	o.externalURL = "wss://argus.example.com"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "no mode suffix") {
		t.Fatalf("err = %v, want no-mode-suffix error", err)
	}
}

func TestResolveTunnelExternalRejectsCloudflareFlags(t *testing.T) {
	o := baseOpts()
	o.provider = "external"
	o.externalURL = "wss://argus.example.com"
	o.cfToken = "tok"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "--cloudflare-") {
		t.Fatalf("err = %v, want cloudflare-flag rejection", err)
	}
}

func TestResolveTunnelExternalValidatesScheme(t *testing.T) {
	for _, bad := range []string{"argus.example.com", "ftp://h", "://h"} {
		o := baseOpts()
		o.provider = "external"
		o.externalURL = bad
		if _, _, err := resolveTunnel(o); err == nil {
			t.Errorf("externalURL %q should be rejected", bad)
		}
	}
	for _, ok := range []string{"ws://h", "wss://h", "http://h", "https://h"} {
		o := baseOpts()
		o.provider = "external"
		o.externalURL = ok
		if _, _, err := resolveTunnel(o); err != nil {
			t.Errorf("externalURL %q should be accepted, got %v", ok, err)
		}
	}
}

func TestResolveTunnelExternalAcceptsBasePath(t *testing.T) {
	// A reverse proxy may mount the gateway under a base path; the URL is kept verbatim
	// and /client|/node append to it at connect time.
	o := baseOpts()
	o.provider = "external"
	o.externalURL = "wss://example.com/gateway"
	p, _, err := resolveTunnel(o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ext := p.(tunnel.External); ext.URL != "wss://example.com/gateway" {
		t.Errorf("URL = %q, want base path preserved", ext.URL)
	}
}

func TestResolveTunnelExternalRejectsQueryFragmentUserinfo(t *testing.T) {
	// These would leak verbatim into the pairing QR; a base path is fine but these are not.
	for _, bad := range []string{"wss://h?x=1", "wss://h#frag", "wss://user:pass@h"} {
		o := baseOpts()
		o.provider = "external"
		o.externalURL = bad
		if _, _, err := resolveTunnel(o); err == nil {
			t.Errorf("externalURL %q should be rejected", bad)
		}
	}
}

// --- zrok (named public share over zrok2) ---

func zrokOpts() tunnelOptions {
	o := baseOpts()
	o.provider = "zrok"
	o.bin = "/usr/bin/zrok2" // non-empty => no PATH lookup
	return o
}

func TestResolveTunnelZrokHappy(t *testing.T) {
	o := zrokOpts()
	o.zrokName = "myapp"
	p, origin, err := resolveTunnel(o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	z, ok := p.(*tunnel.Zrok)
	if !ok {
		t.Fatalf("provider type = %T", p)
	}
	if z.Selection != "myapp" {
		t.Errorf("provider = %+v, want name myapp", z)
	}
	if origin != "http://127.0.0.1:8443" {
		t.Errorf("origin = %q", origin)
	}
}

func TestResolveTunnelZrokReservedAlias(t *testing.T) {
	o := zrokOpts()
	o.provider = "zrok:reserved"
	o.zrokName = "myapp"
	if _, _, err := resolveTunnel(o); err != nil {
		t.Fatalf("zrok:reserved should be a valid alias, got %v", err)
	}
}

func TestResolveTunnelZrokRejectsBadSuffix(t *testing.T) {
	o := zrokOpts()
	o.provider = "zrok:bogus"
	o.zrokName = "myapp"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "zrok") {
		t.Fatalf("err = %v, want bad-suffix error", err)
	}
}

func TestResolveTunnelZrokDefaultsName(t *testing.T) {
	o := zrokOpts() // no zrokName set
	p, _, err := resolveTunnel(o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	z, ok := p.(*tunnel.Zrok)
	if !ok {
		t.Fatalf("provider type = %T", p)
	}
	if z.Selection != "argus" {
		t.Errorf("provider = %+v, want default name argus", z)
	}
}

func TestResolveTunnelZrokRejectsOtherProviderFlags(t *testing.T) {
	o := zrokOpts()
	o.zrokName = "myapp"
	o.cfToken = "tok"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "not valid with --tunnel zrok") {
		t.Fatalf("err = %v, want cross-provider flag rejection", err)
	}
}

func TestResolveTunnelZrokRequiresGatewayMode(t *testing.T) {
	o := zrokOpts()
	o.zrokName = "myapp"
	o.runGateway = false
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "gateway mode") {
		t.Fatalf("err = %v, want gateway-mode requirement", err)
	}
}

// --- ensureZrokEnabled (overview-based detection + interactive enable) ---

func TestEnsureZrokEnabledNoopWhenEnabled(t *testing.T) {
	defer stubZrokEnabled(true)()
	if err := ensureZrokEnabled(context.Background(), "zrok2", false); err != nil {
		t.Fatalf("enabled env should be a no-op, got %v", err)
	}
}

func TestEnsureZrokEnabledFailFastNonInteractive(t *testing.T) {
	defer stubZrokEnabled(false)()
	// Not enabled and not a terminal: must fail fast rather than block on a prompt.
	err := ensureZrokEnabled(context.Background(), "zrok2", false)
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("err = %v, want fail-fast not-enabled guidance", err)
	}
}

// stubZrokEnabled overrides the enabled-check and returns a restore func.
func stubZrokEnabled(enabled bool) func() {
	prev := zrokEnabled
	zrokEnabled = func(context.Context, string) bool { return enabled }
	return func() { zrokEnabled = prev }
}

// --- ngrok (public tunnel over the ngrok agent) ---

func ngrokOpts() tunnelOptions {
	o := baseOpts()
	o.provider = "ngrok"
	o.bin = "/usr/bin/ngrok" // non-empty => no PATH lookup
	return o
}

func TestResolveTunnelNgrokDefaultDevDomain(t *testing.T) {
	p, origin, err := resolveTunnel(ngrokOpts()) // no domain, no mode => account dev domain
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	n, ok := p.(tunnel.Ngrok)
	if !ok {
		t.Fatalf("provider type = %T", p)
	}
	if n.Domain != "" {
		t.Errorf("provider = %+v, want no domain (dev domain default)", n)
	}
	if origin != "http://127.0.0.1:8443" {
		t.Errorf("origin = %q", origin)
	}
}

func TestResolveTunnelNgrokReservedDomain(t *testing.T) {
	o := ngrokOpts()
	o.ngrokDomain = "argus.ngrok.app"
	p, _, err := resolveTunnel(o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if n := p.(tunnel.Ngrok); n.Domain != "argus.ngrok.app" {
		t.Errorf("domain = %q, want argus.ngrok.app", n.Domain)
	}
}

func TestResolveTunnelNgrokRejectsMode(t *testing.T) {
	o := ngrokOpts()
	o.provider = "ngrok:random"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "no mode suffix") {
		t.Fatalf("err = %v, want no-mode-suffix error", err)
	}
}

func TestResolveTunnelNgrokRejectsOtherProviderFlags(t *testing.T) {
	o := ngrokOpts()
	o.zrokName = "myapp"
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "not valid with --tunnel ngrok") {
		t.Fatalf("err = %v, want cross-provider flag rejection", err)
	}
}

func TestResolveTunnelNgrokRequiresGatewayMode(t *testing.T) {
	o := ngrokOpts()
	o.runGateway = false
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "gateway mode") {
		t.Fatalf("err = %v, want gateway-mode requirement", err)
	}
}

// --- ensureNgrokAuth (authtoken prerequisite) ---

func TestEnsureNgrokAuthNoopWhenAuthed(t *testing.T) {
	defer stubNgrokAuthed(true)()
	if err := ensureNgrokAuth(context.Background(), "ngrok", false); err != nil {
		t.Fatalf("authed should be a no-op, got %v", err)
	}
}

func TestEnsureNgrokAuthFailFastNonInteractive(t *testing.T) {
	defer stubNgrokAuthed(false)()
	err := ensureNgrokAuth(context.Background(), "ngrok", false)
	if err == nil || !strings.Contains(err.Error(), "authtoken") {
		t.Fatalf("err = %v, want fail-fast authtoken guidance", err)
	}
}

// stubNgrokAuthed overrides the authtoken-check and returns a restore func.
func stubNgrokAuthed(authed bool) func() {
	prev := ngrokAuthed
	ngrokAuthed = func(context.Context, string) bool { return authed }
	return func() { ngrokAuthed = prev }
}

// --- ensureCloudflareLogin (cert prerequisite for local tunnels) ---

func TestEnsureCloudflareLoginNonLocalNoop(t *testing.T) {
	// A custom path that does not exist: a local tunnel would error, but a
	// quick/remote tunnel (empty Tunnel) must skip the check entirely.
	t.Setenv("TUNNEL_ORIGIN_CERT", filepath.Join(t.TempDir(), "absent.pem"))
	if err := ensureCloudflareLogin(context.Background(), tunnel.Cloudflare{}, false); err != nil {
		t.Fatalf("non-local should be a no-op, got %v", err)
	}
}

func TestEnsureCloudflareLoginCertPresent(t *testing.T) {
	t.Setenv("TUNNEL_ORIGIN_CERT", existingCert(t))
	cf := tunnel.Cloudflare{Bin: "cloudflared", Tunnel: "argus", Hostname: "h"}
	for _, interactive := range []bool{false, true} {
		if err := ensureCloudflareLogin(context.Background(), cf, interactive); err != nil {
			t.Errorf("interactive=%v: cert present should pass, got %v", interactive, err)
		}
	}
}

func TestEnsureCloudflareLoginMissingNonInteractive(t *testing.T) {
	t.Setenv("TUNNEL_ORIGIN_CERT", filepath.Join(t.TempDir(), "absent.pem"))
	cf := tunnel.Cloudflare{Bin: "cloudflared", Tunnel: "argus", Hostname: "h"}
	err := ensureCloudflareLogin(context.Background(), cf, false)
	if err == nil || !strings.Contains(err.Error(), "login") {
		t.Fatalf("err = %v, want fail-fast login guidance", err)
	}
}

func TestEnsureCloudflareLoginMissingCustomPathFailsFast(t *testing.T) {
	// Interactive, but a custom (non-default) cert path => no auto-login.
	t.Setenv("TUNNEL_ORIGIN_CERT", filepath.Join(t.TempDir(), "absent.pem"))
	cf := tunnel.Cloudflare{Bin: "cloudflared", Tunnel: "argus", Hostname: "h"}
	err := ensureCloudflareLogin(context.Background(), cf, true)
	if err == nil || !strings.Contains(err.Error(), "login") {
		t.Fatalf("err = %v, want fail-fast (custom path, no auto-login)", err)
	}
}

func TestResolveTunnelLookPathMissing(t *testing.T) {
	o := baseOpts()
	o.bin = ""           // force PATH lookup
	t.Setenv("PATH", "") // guarantee cloudflared is not found
	_, _, err := resolveTunnel(o)
	if err == nil || !strings.Contains(err.Error(), "cloudflared") {
		t.Fatalf("err = %v, want cloudflared-not-found", err)
	}
}

func TestOriginFromListen(t *testing.T) {
	cases := map[string]string{
		":8443":          "http://127.0.0.1:8443",
		"0.0.0.0:8443":   "http://127.0.0.1:8443",
		"127.0.0.1:9000": "http://127.0.0.1:9000",
	}
	for in, want := range cases {
		got, err := originFromListen(in)
		if err != nil || got != want {
			t.Errorf("originFromListen(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := originFromListen("garbage"); err == nil {
		t.Error("malformed listen address should error")
	}
}

func TestCloudflaredLogLevel(t *testing.T) {
	cases := map[slog.Level]string{
		slog.Level(-8):  "debug", // trace
		slog.LevelDebug: "debug",
		slog.LevelInfo:  "warn", // offset: hide cloudflared's chatty info
		slog.LevelWarn:  "warn",
		slog.LevelError: "error",
		slog.Level(12):  "error", // fatal
	}
	for in, want := range cases {
		if got := cloudflaredLogLevel(in); got != want {
			t.Errorf("cloudflaredLogLevel(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveTunnelSetsCloudflaredLogLevel(t *testing.T) {
	o := baseOpts()
	o.provider = "cloudflare:remote" // remote keeps the plain info->warn offset
	o.cfToken = "tok"
	o.logLevel = slog.LevelInfo
	p, _, err := resolveTunnel(o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cf, ok := p.(tunnel.Cloudflare)
	if !ok {
		t.Fatalf("provider type = %T", p)
	}
	if cf.LogLevel != "warn" {
		t.Errorf("cf.LogLevel = %q, want warn (offset from info)", cf.LogLevel)
	}
}

// A quick tunnel must run cloudflared at info (or below) or its public-URL banner
// is never printed; the info->warn offset that suits other modes would hide it.
func TestResolveTunnelQuickFloorsLogLevel(t *testing.T) {
	cases := map[slog.Level]string{
		slog.Level(-8):  "debug", // trace: stays at debug (URL still emitted)
		slog.LevelDebug: "debug",
		slog.LevelInfo:  "info", // would be warn without the floor
		slog.LevelWarn:  "info",
		slog.LevelError: "info",
	}
	for in, want := range cases {
		o := baseOpts() // no --cloudflare-* flags => quick mode
		o.logLevel = in
		p, _, err := resolveTunnel(o)
		if err != nil {
			t.Fatalf("resolve(%v): %v", in, err)
		}
		cf := p.(tunnel.Cloudflare)
		if cf.LogLevel != want {
			t.Errorf("quick LogLevel at argus %v = %q, want %q", in, cf.LogLevel, want)
		}
	}
}
