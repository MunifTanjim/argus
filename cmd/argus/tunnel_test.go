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
