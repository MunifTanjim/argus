package main

import "testing"

func TestResolveGatewayURLPlainAppendsRoute(t *testing.T) {
	cases := []struct {
		raw, route, want string
	}{
		{"ws://gateway.example.com", "/node", "ws://gateway.example.com/node"},
		{"wss://gateway.example.com:8443", "/client", "wss://gateway.example.com:8443/client"},
		{"ws://gateway.example.com:9000", "/node", "ws://gateway.example.com:9000/node"},
		// A reverse proxy may mount the gateway under a base path; the route appends to it.
		{"wss://example.com/gateway", "/client", "wss://example.com/gateway/client"},
		{"wss://example.com/gateway/", "/node", "wss://example.com/gateway/node"},
		{"wss://example.com/deep/base", "/client", "wss://example.com/deep/base/client"},
	}
	for _, tc := range cases {
		url, client, err := resolveGatewayURL(tc.raw, tc.route, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if url != tc.want {
			t.Errorf("%s: url = %q, want %q", tc.raw, url, tc.want)
		}
		if client != nil {
			t.Errorf("%s: client should be nil (default transport)", tc.raw)
		}
	}
}

func TestResolveGatewayURLSSH(t *testing.T) {
	cases := []struct {
		raw, route, want string
	}{
		{"ssh://user@gateway.example.com", "/node", "ws://gateway.example.com:8443/node"},          // default gateway port
		{"ssh://user@gateway.example.com:2222", "/node", "ws://gateway.example.com:8443/node"},     // :port is SSH port, not gateway
		{"ssh://gateway.example.com?port=9000", "/client", "ws://gateway.example.com:9000/client"}, // gateway port via query
		{"ssh://user@gateway.example.com:2222?port=9000", "/node", "ws://gateway.example.com:9000/node"},
	}
	for _, tc := range cases {
		url, client, err := resolveGatewayURL(tc.raw, tc.route, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if url != tc.want {
			t.Errorf("%s: url = %q, want %q", tc.raw, url, tc.want)
		}
		if client == nil {
			t.Errorf("%s: client should be non-nil (ssh transport)", tc.raw)
		}
	}
}

func TestResolveGatewayURLSSHRejectsPath(t *testing.T) {
	// ssh dials the gateway's loopback directly, so a base path makes no sense; ws/wss
	// paths, by contrast, are now supported as a reverse-proxy base (see above).
	if _, _, err := resolveGatewayURL("ssh://gateway.example.com/x", "/node", nil); err == nil {
		t.Error("ssh gateway url with a path should be rejected")
	}
}

func TestResolveGatewayURLErrors(t *testing.T) {
	for _, raw := range []string{"ssh://", "tcp://gateway.example.com", "://nope"} {
		if _, _, err := resolveGatewayURL(raw, "/node", nil); err == nil {
			t.Errorf("%s: expected an error", raw)
		}
	}
}
