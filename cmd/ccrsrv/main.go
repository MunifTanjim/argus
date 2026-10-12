// Command ccrsrv runs a self-hosted Claude Code Remote Control session-ingress
// server (see devdocs/claude-remote-control-protocol.md).
//
// It serves the worker endpoints a headless `claude --print --sdk-url …`
// connects to, plus a small controller API to drive sessions, and optionally
// reverse-proxies everything else to the real Anthropic API so a
// hosts-override machine keeps working normally.
//
// Setup sketch (single machine):
//
//	/etc/hosts:  127.0.0.1 api.anthropic.com
//	$ go run ./cmd/ccrsrv --ca-file /tmp/ccr-ca.pem
//	$ export NODE_EXTRA_CA_CERTS=/tmp/ccr-ca.pem
//	$ export ANTHROPIC_API_KEY=sk-ant-…        # inference, pay-as-you-go
//	$ curl -X POST https://api.anthropic.com:8443/client/sessions \
//	    -H "Authorization: Bearer $TOKEN" -d '{"config":{"cwd":"'"$PWD"'"}}'
//	$ curl -X POST https://api.anthropic.com:8443/client/sessions/<id>/messages \
//	    -H "Authorization: Bearer $TOKEN" -d '{"text":"hello"}'
//	$ curl -N https://api.anthropic.com:8443/client/sessions/<id>/events \
//	    -H "Authorization: Bearer $TOKEN"
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/MunifTanjim/argus/internal/ccr"
)

func main() {
	var (
		addr     = flag.String("addr", ":8443", "listen address")
		hostname = flag.String("hostname", "api.anthropic.com", "ingress hostname (must be allowlisted for --sdk-url)")
		token    = flag.String("token", "", "controller token (minted when empty)")
		caFile   = flag.String("ca-file", "", "write the generated CA certificate here (for NODE_EXTRA_CA_CERTS)")
		certFile = flag.String("cert", "", "TLS cert PEM (default: generated self-signed)")
		keyFile  = flag.String("key", "", "TLS key PEM (default: generated self-signed)")
		upstream = flag.String("upstream", "https://api.anthropic.com", "reverse-proxy origin for non-ingress paths; empty disables")
		noProxy  = flag.Bool("no-proxy", false, "disable the upstream reverse proxy")
		claude   = flag.String("claude", os.Getenv("CLAUDE_BIN"), "claude binary to spawn per session; empty disables spawning")
		apiKey   = flag.String("api-key", os.Getenv("ANTHROPIC_API_KEY"), "ANTHROPIC_API_KEY exported to spawned workers")
		debug    = flag.Bool("debug", false, "spawn workers with --verbose")
	)
	flag.Parse()

	srv := ccr.NewServer(ccr.ServerConfig{
		Hostname:   *hostname,
		AdminToken: *token,
		Upstream:   *upstream,
	})
	if *noProxy {
		srv = ccr.NewServer(ccr.ServerConfig{Hostname: *hostname, AdminToken: srv.AdminToken()})
	}
	if *claude != "" {
		srv.SetSpawner(&ccr.SpawnerConfig{
			Claude:  *claude,
			BaseURL: fmt.Sprintf("https://%s%s", *hostname, publicPort(*addr)),
			APIKey:  *apiKey,
			Debug:   *debug,
		})
	}

	handler := srv.Handler()

	var cert tls.Certificate
	if *certFile != "" && *keyFile != "" {
		var err error
		cert, err = tls.LoadX509KeyPair(*certFile, *keyFile)
		if err != nil {
			log.Fatalf("ccrsrv: load cert: %v", err)
		}
	} else {
		artifacts, err := ccr.GenerateTLS(*hostname)
		if err != nil {
			log.Fatalf("ccrsrv: generate tls: %v", err)
		}
		if *caFile != "" {
			if err := os.MkdirAll(filepath.Dir(*caFile), 0o755); err == nil {
				_ = os.WriteFile(*caFile, artifacts.CA, 0o644)
			}
		}
		cert, err = tls.X509KeyPair(artifacts.Leaf, artifacts.Key)
		if err != nil {
			log.Fatalf("ccrsrv: keypair: %v", err)
		}
	}

	base := fmt.Sprintf("https://%s%s", *hostname, publicPort(*addr))
	fmt.Printf("ccrsrv: ingress  %s/v1/code/sessions\n", base)
	fmt.Printf("ccrsrv: hostname %s (must resolve here; /etc/hosts or split-horizon DNS)\n", *hostname)
	if *caFile != "" {
		fmt.Printf("ccrsrv: trust the CA: export NODE_EXTRA_CA_CERTS=%s\n", *caFile)
	}
	if *claude != "" {
		fmt.Printf("ccrsrv: spawner  %s (workers inherit ANTHROPIC_API_KEY)\n", *claude)
	} else {
		fmt.Printf("ccrsrv: spawner  disabled (--claude to enable)\n")
	}
	fmt.Printf("ccrsrv: token    %s\n", srv.AdminToken())

	httpSrv := &http.Server{
		Addr:      *addr,
		Handler:   handler,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		_ = httpSrv.Close()
	}()

	if err := httpSrv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
		log.Fatalf("ccrsrv: %v", err)
	}
}

// publicPort returns ":port" or "" when the port is the default for the
// scheme (443 for https).
func publicPort(addr string) string {
	switch addr {
	case ":443", "443":
		return ""
	default:
		if len(addr) > 0 && addr[0] == ':' {
			return addr
		}
		return ":" + addr
	}
}
