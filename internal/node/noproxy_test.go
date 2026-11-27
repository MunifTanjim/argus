package node

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
)

// TestDirectDialIgnoresProxyEnv: a direct dial connects straight to the
// endpoint even with a proxy in the environment; a proxy would bypass the
// tailnet peer check.
func TestDirectDialIgnoresProxyEnv(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	tr, ok := directHTTPClient.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Fatalf("direct transport = %T with proxy set, want *http.Transport without a proxy", directHTTPClient.Transport)
	}
	if directHTTPClient.CheckRedirect == nil {
		t.Fatal("direct client follows redirects")
	}
	// Record where the client actually connects.
	rec := tr.Clone()
	var dialed string
	errStop := errors.New("stop")
	rec.DialContext = func(_ context.Context, _, addr string) (net.Conn, error) {
		dialed = addr
		return nil, errStop
	}
	c := &http.Client{Transport: rec, CheckRedirect: directHTTPClient.CheckRedirect}
	_, _ = c.Get("http://100.64.0.1:7777/p2p")
	if dialed != "100.64.0.1:7777" {
		t.Fatalf("dialed %q, want the endpoint 100.64.0.1:7777", dialed)
	}
}
