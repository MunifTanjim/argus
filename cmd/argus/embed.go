package main

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/shell"
)

// connect returns a client that recovers from temporary connection failures: it dials
// the node/gateway through a Dialer and re-dials with backoff if the connection drops.
// With a gateway URL it dials the gateway over WebSocket; otherwise it connects to the
// local node's unix socket (the node must already be running; use connectLocalSpawn to
// start one first).
func connect(ctx context.Context, gatewayURL, token, socket string) (*api.ReconnectingClient, error) {
	dial, err := gatewayDialer(gatewayURL, token, socket)
	if err != nil {
		shell.StdErrF("argus: %v\n", err)
		return nil, err
	}
	c, err := api.NewReconnectingClient(ctx, dial)
	if err != nil {
		if gatewayURL != "" {
			shell.StdErrF("argus: cannot connect to gateway at %s: %v\n", gatewayURL, err)
		} else {
			shell.StdErrF("argus: cannot connect to argusd at %s: %v\n", socket, err)
		}
		return nil, err
	}
	return c, nil
}

// gatewayDialer builds the Dialer connect uses. With a gateway URL it resolves
// it once and dials over WebSocket; otherwise it dials the local node socket.
// The local node must already be listening — startup spawns it explicitly (see
// connectLocalSpawn) rather than dialing one into existence here.
func gatewayDialer(gatewayURL, token, socket string) (api.Dialer, error) {
	if gatewayURL != "" {
		wsURL, gatewayClient, err := resolveGatewayURL(gatewayURL, routeClient, nil)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (net.Conn, error) {
			return api.DialWSConn(ctx, wsURL, token, gatewayClient)
		}, nil
	}
	return func(ctx context.Context) (net.Conn, error) {
		return net.Dial("unix", socket)
	}, nil
}

// localNodeRunning reports whether a node is already listening on the socket.
// A nodeAbsent error (missing or refused socket) means "not running"; any other
// dial error is returned so the caller can treat it as a real failure.
func localNodeRunning(socket string) (bool, error) {
	conn, err := net.Dial("unix", socket)
	if err == nil {
		conn.Close()
		return true, nil
	}
	if nodeAbsent(err) {
		return false, nil
	}
	return false, err
}

// connectLocalSpawn starts an ephemeral embedded node (tied to ctx), waits for it
// to accept, then connects. The embedded node stops when ctx is cancelled.
func connectLocalSpawn(ctx context.Context, token, socket string) (*api.ReconnectingClient, error) {
	startEmbeddedNode(ctx, socket)
	conn, err := dialWithRetry(socket, 3*time.Second)
	if err != nil {
		shell.StdErrF("argus: embedded node did not start at %s: %v\n", socket, err)
		return nil, err
	}
	conn.Close() // probe only; the client opens its own connection
	return connect(ctx, "", token, socket)
}

// nodeAbsent reports whether a dial error means "no node is listening": a
// missing socket file (ENOENT) or a stale one with no listener (ECONNREFUSED).
func nodeAbsent(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

// startEmbeddedNode runs a node in-process, serving the unix socket until ctx
// is cancelled. It stays quiet on stderr so it never corrupts the TUI's
// alt-screen. Unlike `argus start` it does not reconcile installed Claude Code
// hooks: this launch is ephemeral and may run from a different binary path than
// the installed one, so rewriting global hook config here would be surprising.
func startEmbeddedNode(ctx context.Context, socket string) {
	d := node.New()
	go func() { _ = d.Run(ctx, socket) }()
}

// dialWithRetry polls the socket until a node accepts a connection or timeout.
func dialWithRetry(socket string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(25 * time.Millisecond)
	}
}
