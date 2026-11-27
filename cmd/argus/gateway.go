package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/MunifTanjim/argus/internal/sshconn"
)

// Gateway WebSocket routes, appended by resolveGatewayURL per role.
const (
	routeNode   = "/node"   // node uplink endpoint
	routeClient = "/client" // dashboard/client endpoint
)

// resolveGatewayURL turns a --gateway base into the WebSocket URL and *http.Client to
// use, appending the role route ("/node" or "/client") after any base path.
//
//   - ws:// or wss:// → "<scheme>://<host>[:port][/base-path]<route>", nil client (default
//     transport). A reverse proxy may expose the gateway under a base path (e.g.
//     wss://example.com/gateway); the role route is appended to it. The proxy is expected
//     to strip the prefix, since the gateway serves /node and /client at root.
//   - ssh://[user@]host[:ssh-port][?port=<gateway-port>] → a client tunneling every dial
//     through a managed `ssh -W` child to 127.0.0.1:<gateway-port> on the SSH host, and
//     "ws://<host>:<gateway-port><route>". Authority port is the SSH port (ssh -p); the
//     "port" query is the gateway's loopback port (default 8443); identity/jump hosts come
//     from ssh config. The gateway is expected to bind loopback. No base path: ssh dials
//     the gateway directly, bypassing any reverse proxy.
//
// log receives the ssh child's stderr (auth / host-key failures); nil to discard.
func resolveGatewayURL(raw, route string, log *slog.Logger) (string, *http.Client, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", nil, fmt.Errorf("parse --gateway: %w", err)
	}

	switch u.Scheme {
	case "ws", "wss":
		return u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/") + route, nil, nil

	case "ssh":
		if u.Path != "" && u.Path != "/" {
			return "", nil, fmt.Errorf("ssh gateway url takes no path (it dials the gateway directly): %s", raw)
		}
		gatewayPort := u.Query().Get("port")
		if gatewayPort == "" {
			gatewayPort = "8443" // gateway's default --listen-addr port
		}
		q := *u
		q.RawQuery = ""
		sshDest, sshPort, err := sshconn.ParseSSHTarget(q.String()) // SSH port empty => ssh config / 22
		if err != nil {
			return "", nil, fmt.Errorf("ssh gateway url: %w", err)
		}
		host := u.Hostname()
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
