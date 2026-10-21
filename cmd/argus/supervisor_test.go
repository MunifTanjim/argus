package main

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
)

func TestCombinedMode(t *testing.T) {
	cases := []struct {
		mode, url, token string
		want             bool
	}{
		{"", "", "tok", true},
		{"", "wss://h", "tok", false},
		{"", "", "", false},
		{"node", "", "tok", false},
		{"gateway", "", "tok", false},
	}
	for _, tc := range cases {
		if got := combinedMode(cfgWithMode(tc.mode, tc.url, tc.token)); got != tc.want {
			t.Errorf("combinedMode(%q, %q, %q) = %v, want %v", tc.mode, tc.url, tc.token, got, tc.want)
		}
	}
}

func TestSetFlagsSkipsEmptyValues(t *testing.T) {
	got := setFlags("id", "box", "label", "", "log-format", "json")
	if !slices.Equal(got, []string{"--id=box", "--log-format=json"}) {
		t.Fatalf("setFlags = %v", got)
	}
}

func TestSupervisedSpecsForwardResolvedConfig(t *testing.T) {
	cfg := cfgWithMode("", "", "tok")
	cfg.Socket = "/run/argus.sock"
	cfg.Node.ID, cfg.Node.Label = "box", "Box"
	cfg.Log.Format, cfg.Log.Level = "json", "debug"
	cfg.Tunnel.Provider = "cloudflare"
	cfg.Tunnel.Cloudflare.Token = "cfsecret"
	bound := &net.TCPAddr{IP: net.IPv4zero, Port: 8443}
	specs := supervisedSpecs(newStartCmd("test"), cfg, ":8443", bound, true)
	if len(specs) != 2 || specs[0].role != roleGateway || specs[1].role != roleNode {
		t.Fatalf("specs = %+v, want gateway then node", specs)
	}
	gw, node := specs[0], specs[1]
	if gw.listenAddr != ":8443" || gw.token != "tok" || node.token != "tok" {
		t.Fatalf("gateway %+v node %+v", gw, node)
	}
	if node.gatewayURL != "ws://127.0.0.1:8443" {
		t.Fatalf("node gatewayURL = %q", node.gatewayURL)
	}
	for _, want := range []string{"--tunnel=cloudflare", "--log-format=json", "--log-level=debug"} {
		if !slices.Contains(gw.args(), want) {
			t.Errorf("gateway args %v lack %q", gw.args(), want)
		}
	}
	for _, want := range []string{"--id=box", "--label=Box", "--log-format=json"} {
		if !slices.Contains(node.args(), want) {
			t.Errorf("node args %v lack %q", node.args(), want)
		}
	}
	for _, a := range append(gw.args(), node.args()...) {
		if strings.Contains(a, "cfsecret") || a == "--token=tok" {
			t.Fatalf("secret in argv: %q", a)
		}
	}
	if !slices.Contains(gw.env(nil), "ARGUS_CLOUDFLARE_TOKEN=cfsecret") || slices.Contains(node.env(nil), "ARGUS_CLOUDFLARE_TOKEN=cfsecret") {
		t.Fatalf("cloudflare token must reach the gateway only: gw %v node %v", gw.env(nil), node.env(nil))
	}
	inherited := []string{"ARGUS_CLOUDFLARE_TOKEN=inherited"}
	if env := node.env(inherited); slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "ARGUS_CLOUDFLARE_TOKEN=") }) {
		t.Fatalf("the node inherited the cloudflare token: %v", env)
	}
	if slices.Contains(gw.env(nil), "CLICOLOR_FORCE=1") {
		t.Fatal("JSON logs must not force color")
	}
}

func TestSupervisedSpecsForceColorOnlyForAPrettyTTY(t *testing.T) {
	cfg := cfgWithMode("", "", "tok")
	bound := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	if s := supervisedSpecs(newStartCmd("test"), cfg, "127.0.0.1:1", bound, true); !slices.Contains(s[1].env(nil), "CLICOLOR_FORCE=1") {
		t.Fatal("a pretty TTY must force color in the children")
	}
	if s := supervisedSpecs(newStartCmd("test"), cfg, "127.0.0.1:1", bound, false); slices.Contains(s[1].env(nil), "CLICOLOR_FORCE=1") {
		t.Fatal("stderr that is not a TTY must not force color")
	}
}

func TestWaitSupervisedTreatsExitDuringShutdownAsClean(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 50 {
		g := &managedGroup{exited: make(chan error, 1)}
		g.exited <- nil
		if err := waitSupervised(ctx, g); err != nil {
			t.Fatalf("waitSupervised = %v, want nil once ctx is done", err)
		}
	}
}

func TestWaitSupervisedTreatsCleanChildExitAsStop(t *testing.T) {
	g := &managedGroup{exited: make(chan error, 1)}
	g.exited <- nil
	if err := waitSupervised(context.Background(), g); err != nil {
		t.Fatalf("waitSupervised = %v, want nil for a clean child exit", err)
	}
}

func TestWaitSupervisedReportsFailedChildExit(t *testing.T) {
	g := &managedGroup{exited: make(chan error, 1)}
	g.exited <- errors.New("exit status 1")
	err := waitSupervised(context.Background(), g)
	if err == nil || !strings.Contains(err.Error(), "a managed server exited") {
		t.Fatalf("waitSupervised = %v, want a managed server exited error", err)
	}
}
