package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"

	"github.com/MunifTanjim/argus/internal/sshconn"
)

// Gateway WebSocket routes, appended by resolveGatewayURL per role. Kept as constants so
// a typo can't silently produce a 404 at a call site.
const (
	routeNode   = "/node"   // node uplink endpoint
	routeClient = "/client" // dashboard/client endpoint
)

// resolveGatewayURL turns a --gateway base into the WebSocket URL and *http.Client that
// ConnectGateway / DialWS should use, appending the caller's role route ("/node" or
// "/client"). The gateway URL carries no route path of its own — the route is determined
// by the role, not the user — so a non-empty path is rejected.
//
//   - ws:// or wss://  → "<scheme>://<host>[:port]<route>", nil client (default transport).
//   - ssh://[user@]host[:ssh-port][?port=<gateway-port>] → a client that tunnels every dial
//     through a managed `ssh -W` child to the gateway's loopback port on the SSH host, and
//     "ws://<host>:<gateway-port><route>". The authority port is the SSH port (ssh -p); the
//     "port" query is the gateway's loopback listener port (default 8443); SSH identity /
//     jump hosts come from the user's ssh config. The forward target is always
//     127.0.0.1:<gateway-port> — the gateway is expected to bind loopback.
//
// log receives the ssh child's stderr (auth / host-key failures); pass nil to discard.
func resolveGatewayURL(raw, route string, log *slog.Logger) (string, *http.Client, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", nil, fmt.Errorf("parse --gateway: %w", err)
	}
	if u.Path != "" && u.Path != "/" {
		return "", nil, fmt.Errorf("--gateway takes no path (the %s route is implicit): use scheme://[user@]host[:port]", route)
	}

	switch u.Scheme {
	case "ws", "wss":
		return u.Scheme + "://" + u.Host + route, nil, nil

	case "ssh":
		host := u.Hostname()
		if host == "" {
			return "", nil, fmt.Errorf("ssh gateway url needs a host: %s", raw)
		}
		sshPort := u.Port() // SSH port (ssh -p); empty => ssh config / 22
		gatewayPort := u.Query().Get("port")
		if gatewayPort == "" {
			gatewayPort = "8443" // gateway's default --listen-addr port
		}
		sshDest := host
		if u.User != nil {
			if name := u.User.Username(); name != "" {
				sshDest = name + "@" + host
			}
		}
		remote := net.JoinHostPort("127.0.0.1", gatewayPort)

		client := &http.Client{Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return sshconn.Dial(sshDest, remote, sshPort, log)
			},
		}}
		wsURL := "ws://" + net.JoinHostPort(host, gatewayPort) + route
		return wsURL, client, nil

	default:
		return "", nil, fmt.Errorf("unsupported --gateway scheme %q (use ws, wss, or ssh)", u.Scheme)
	}
}
