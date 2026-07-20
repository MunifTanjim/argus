package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/logbuf"
	"github.com/MunifTanjim/argus/internal/logger"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/shell"
)

// connect returns a client that re-dials with backoff if the connection drops. With a
// gateway URL it dials over WebSocket; otherwise the local node's unix socket (which
// must already be running — use connectLocalSpawn to start one first).
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

// gatewayDialer builds the Dialer connect uses: WebSocket for a gateway URL, else the
// local node socket. The local node must already be listening (see connectLocalSpawn).
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

// localNodeRunning reports whether a node is already listening on the socket. A
// nodeAbsent error means "not running"; any other dial error is returned as a real failure.
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

// connectLocalSpawn starts an ephemeral embedded node (tied to ctx), waits for it to
// accept, then connects. Returns the node's log buffer for the TUI's Logs tab.
func connectLocalSpawn(ctx context.Context, cfg *config.Config, token, socket string) (*api.ReconnectingClient, *logbuf.Buffer, error) {
	return connectLocalSpawnWithGateway(ctx, cfg, "", token, socket)
}

// connectLocalSpawnWithGateway starts an ephemeral embedded node (tied to ctx). Empty
// gatewayURL (isolated spawn): the TUI drives the local socket. Set gatewayURL
// (connected spawn): the node uplinks to that gateway so this machine joins the fleet,
// and the TUI drives the gateway so it sees the whole fleet, this machine included.
func connectLocalSpawnWithGateway(ctx context.Context, cfg *config.Config, gatewayURL, token, socket string) (*api.ReconnectingClient, *logbuf.Buffer, error) {
	var wsURL string
	var gatewayClient *http.Client
	if gatewayURL != "" {
		var err error
		if wsURL, gatewayClient, err = resolveGatewayURL(gatewayURL, routeNode, nil); err != nil {
			shell.StdErrF("argus: %v\n", err)
			return nil, nil, err
		}
		// Probe synchronously so a bad host/token is reported before the TUI takes the
		// screen; d.ConnectGateway (below) only retries silently in the background.
		probe, err := api.DialWSConn(ctx, wsURL, token, gatewayClient)
		if err != nil {
			shell.StdErrF("argus: cannot reach gateway at %s: %v\n", gatewayURL, err)
			return nil, nil, err
		}
		probe.Close()
	}
	d, logs := startEmbeddedNode(ctx, cfg, socket)
	if gatewayURL != "" {
		go d.ConnectGateway(ctx, wsURL, token, gatewayClient)
	} else if cfg.Push.Desktop.Enabled {
		// Isolated spawn has no gateway to drive alerts, so watch our own registry.
		// (A connected spawn gets push.desktop RPCs from its gateway instead.)
		events, cancel := d.Registry().Subscribe()
		go func() {
			defer cancel()
			push.Watch(ctx, events, push.Sinks{Immediate: []push.Sink{d.DesktopSink()}}, logger.NewBufferLogger(logs).With("scope", "push"))
		}()
	}
	conn, err := dialWithRetry(func() (net.Conn, error) { return net.Dial("unix", socket) }, 3*time.Second)
	if err != nil {
		shell.StdErrF("argus: embedded node did not start at %s: %v\n", socket, err)
		return nil, nil, err
	}
	conn.Close() // probe only; the client opens its own connection
	// Isolated spawn drives the local node; connected spawn drives the gateway.
	client, err := connect(ctx, gatewayURL, token, socket)
	if err != nil {
		return nil, nil, err
	}
	return client, logs, nil
}

// connectLocalGateway starts an ephemeral embedded node AND a co-located gateway
// (pairing + push, no tunnel), then points the TUI at that gateway over loopback
// so it sees the whole fleet. Mirrors `argus start --token` minus the tunnel.
func connectLocalGateway(ctx context.Context, cfg *config.Config, socket string) (*api.ReconnectingClient, *logbuf.Buffer, error) {
	// Bind before the TUI takes the screen so a port-in-use fails cleanly.
	ln, err := net.Listen("tcp", cfg.Gateway.ListenAddr)
	if err != nil {
		shell.StdErrF("argus: cannot bind gateway listener at %s: %v\n", cfg.Gateway.ListenAddr, err)
		return nil, nil, err
	}

	// Drive the in-process gateway over loopback using the actually-bound address
	// (cfg.Gateway.ListenAddr may specify port 0 or an unspecified host). The TUI
	// reaches the node through the gateway's in-process source, so the node socket is
	// never dialed here.
	gwURL := "ws://" + loopbackDialAddr(ln.Addr().(*net.TCPAddr))

	// A gateway that dies after startup can't recover on a fixed loopback port, so tear
	// the whole embedded stack down (mirrors `argus start`); the TUI surfaces the drop
	// as a disconnect.
	ctx, cancel := context.WithCancel(ctx)

	d, logs := startEmbeddedNode(ctx, cfg, socket)
	baseLog := logger.NewBufferLogger(logs)
	gwLog := baseLog.With("scope", "gateway")
	httpSrv := serveGateway(ctx, gatewayServeOpts{
		node:          d,
		token:         cfg.Token,
		listener:      ln,
		log:           baseLog,
		onFatal:       func() { gwLog.Error("embedded gateway stopped"); cancel() },
		version:       version,
		publicURL:     gwURL,
		enablePairing: true,
		enablePush:    true,
		pushDelay:     cfg.Push.Mobile.Delay,
	})
	go func() {
		<-ctx.Done()
		sctx, sc := context.WithTimeout(context.Background(), 3*time.Second)
		defer sc()
		_ = httpSrv.Shutdown(sctx)
	}()

	wsURL, gwClient, err := resolveGatewayURL(gwURL, routeClient, nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	// serveGateway backgrounds Serve with no readiness signal, so wait for it to
	// accept before connect() (which dials once and fails hard).
	probe, err := dialWithRetry(func() (net.Conn, error) {
		dctx, dc := context.WithTimeout(ctx, time.Second)
		defer dc()
		return api.DialWSConn(dctx, wsURL, cfg.Token, gwClient)
	}, 3*time.Second)
	if err != nil {
		cancel()
		shell.StdErrF("argus: embedded gateway did not accept at %s: %v\n", gwURL, err)
		return nil, nil, err
	}
	probe.Close()

	client, err := connect(ctx, gwURL, cfg.Token, socket)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return client, logs, nil
}

// loopbackDialAddr returns a host:port for reaching a listener from this host: its
// bound port, and 127.0.0.1 when it binds an unspecified host (0.0.0.0/::), else its
// concrete bound IP.
func loopbackDialAddr(addr *net.TCPAddr) string {
	host := "127.0.0.1"
	if addr.IP != nil && !addr.IP.IsUnspecified() {
		host = addr.IP.String()
	}
	return net.JoinHostPort(host, strconv.Itoa(addr.Port))
}

// nodeAbsent reports whether a dial error means "no node is listening": a missing
// socket (ENOENT) or a stale one with no listener (ECONNREFUSED).
func nodeAbsent(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

// startEmbeddedNode runs a node in-process until ctx is cancelled. Logs go to an
// in-memory buffer (returned) for the TUI's Logs tab, not stderr, to keep the
// alt-screen clean. Like `argus start` it reconciles installed Claude Code hooks,
// but keeps the installed binary path: this ephemeral launch may run from a
// different path than the install, which must not be written into the user's hooks.
func startEmbeddedNode(ctx context.Context, cfg *config.Config, socket string) (*node.Node, *logbuf.Buffer) {
	d := node.New()
	logs := logbuf.New(1000)
	log := logger.NewBufferLogger(logs)
	d.SetLogger(log.With("scope", "node"))
	d.SetMirrorAffixes(cfg.Tmux.MirrorSessionPrefix, cfg.Tmux.MirrorSessionSuffix)
	d.SetIdentity(cfg.Node.ID, cfg.Node.Label)
	d.SetVersion(version)
	if kp, err := node.LoadOrCreateIdentity(config.GetStatePath("node-identity.json")); err != nil {
		log.With("scope", "node").Warn("identity load failed; E2E unavailable", "err", err)
	} else {
		d.SetIdentityKey(kp)
	}
	// Without this the embedded node drops every desktop alert.
	d.SetDesktopNotify(cfg.Push.Desktop.Enabled, desktopClickCmd(cfg))
	reconcileEmbeddedHooks(log.With("scope", "hooks"))
	go func() { _ = d.Run(ctx, socket) }()
	return d, logs
}

// reconcileEmbeddedHooks reconciles hooks best-effort (empty bin keeps the installed path).
func reconcileEmbeddedHooks(log *slog.Logger) {
	for _, a := range adapters.All() {
		if added, err := a.ReconcileIfInstalled(""); err != nil {
			log.Error("reconcile hooks failed", "agent", a.Agent(), "err", err)
		} else if len(added) > 0 {
			log.Info("reconciled argus hooks", "agent", a.Agent(), "added", added)
		}
	}
}

// dialWithRetry polls dial until it succeeds or timeout elapses.
func dialWithRetry(dial func() (net.Conn, error), timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := dial()
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(25 * time.Millisecond)
	}
}
