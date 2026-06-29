package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/clienttoken"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/logger"
	applog "github.com/MunifTanjim/argus/internal/logger/log"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tunnel"
)

// uplinkMode reports whether this node dials an upstream gateway (it then does
// not listen). gatewayMode reports whether this node serves as a gateway: it has
// no upstream and a token to require from incoming connections. A node with
// neither is a local node (unix socket only).
func uplinkMode(cfg *config.Config) bool  { return cfg.Gateway.URL != "" }
func gatewayMode(cfg *config.Config) bool { return cfg.Gateway.URL == "" && cfg.Token != "" }

// newStartCmd builds `argus start`: it runs the local node (discovery + tmux
// control + the unix-socket API that `argus hook` and a local TUI use), and
// optionally serves as a gateway (no --gateway + token set) or connects to one (--gateway).
func newStartCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "start",
		Short:         "Run a node (optionally a gateway)",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			if err := setupLogger(cfg); err != nil {
				return fail(cmd, err)
			}
			if config.RuntimeDirIsFallback {
				shell.StdErrF("argus start: XDG runtime dir unavailable; using %s for runtime files\n", config.RuntimeDir)
			}

			tun, tunOrigin, err := resolveTunnel(tunnelOptions{
				provider:     cfg.Tunnel.Provider,
				cfToken:      cfg.Tunnel.Cloudflare.Token,
				cfTunnelName: cfg.Tunnel.Cloudflare.TunnelName,
				cfHostname:   cfg.Tunnel.Cloudflare.Hostname,
				runGateway:   gatewayMode(cfg),
				listenAddr:   cfg.Gateway.ListenAddr,
				logLevel:     config.LogLevel.Level(),
			})
			if err != nil {
				return fail(cmd, err)
			}

			d := node.New()
			d.SetIdentity(cfg.Node.ID, cfg.Node.Label)
			// Standalone node: operational logs go to the configured logger (the embedded
			// node, which shares a TUI's terminal, leaves logging at its discard default).
			// The tunnel supervisor gets its own scope so its output lands in the same stream.
			d.SetLogger(logger.Scoped("node").L)
			clickCmd := desktopClickCmd(cfg) // shared by the node's desktop notifier and the local Watch below
			d.SetDesktopNotify(cfg.Push.Desktop.Enabled, clickCmd)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// A locally-managed Cloudflare tunnel needs an origin certificate; run the
			// interactive `cloudflared tunnel login` for the user when on a terminal, else
			// fail fast. No-op for quick/remote tunnels and when already logged in.
			if cf, ok := tun.(tunnel.Cloudflare); ok {
				if err := ensureCloudflareLogin(ctx, cf, isatty.IsTerminal(os.Stdin.Fd())); err != nil {
					return fail(cmd, err)
				}
			}

			reconcileInstalledHooks()

			if err := connectGateway(ctx, cfg, d); err != nil {
				return fail(cmd, err)
			}

			// Set when a background subsystem fails fatally and brings the node down;
			// read after d.Run so the process exits non-zero (it already cancelled ctx).
			var nodeFailed atomic.Bool
			httpSrv := serveGateway(ctx, cfg, d, tun, tunOrigin, stop, &nodeFailed)

			// Plain local node (no gateway, no uplink): nothing upstream drives desktop
			// notifications, so run a local Watch over our own registry that renders
			// directly. Gateway mode reaches the co-located node via Fanout; uplink mode
			// is driven by the remote gateway's push.desktop RPC.
			if cfg.Push.Desktop.Enabled && !uplinkMode(cfg) && !gatewayMode(cfg) {
				events, cancel := d.Registry().Subscribe()
				// Render through the node's focus-aware sink (same path as gateway-pushed
				// notifications): it suppresses alerts for a session already on screen.
				go func() {
					defer cancel()
					push.Watch(ctx, events, []push.Sink{d.DesktopSink()}, logger.Scoped("push").L)
				}()
			}

			shell.StdErrF("argus start %s: local API on %s\n", version, cfg.Socket)
			// serveGateway already prints "gateway listening on …" when it runs; connectGateway
			// prints "connecting to gateway …". A plain local node prints only the line above.
			err = d.Run(ctx, cfg.Socket) // blocks until the signal cancels ctx

			if httpSrv != nil {
				sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = httpSrv.Shutdown(sctx)
			}
			if err != nil {
				return fail(cmd, err)
			}
			if nodeFailed.Load() {
				return errSilent // a background subsystem already printed the cause
			}
			return nil
		},
	}

	// Flags default to zero values; real defaults come from config (viper), so a flag
	// only overrides config/env when actually set. See resolveConfig / config.Load.
	f := cmd.Flags()
	f.String("socket", "", "unix socket path for the local JSON-RPC API (default: XDG runtime path)")
	f.String("id", "", "stable node id announced to a gateway (default: hostname)")
	f.String("label", "", "human-friendly node name shown in clients (default: hostname)")

	f.String("listen-addr", "", "address for the gateway's WebSocket listener when this node is a gateway (default :8443; terminate TLS via a tunnel, ssh, or a reverse proxy)")

	f.String("gateway", "", "connect to a gateway (the /node route is implicit): ws(s)://host, or ssh://[user@]host[:ssh-port][?port=N] to tunnel over SSH [$ARGUS_GATEWAY_URL]")
	f.String("token", "", "gateway token: required from incoming clients/nodes when this node is a gateway, and presented to the --gateway it connects to (empty: allow all) [$ARGUS_TOKEN]")

	f.String("log-level", "", "log verbosity: trace, debug, info, warn, error, fatal (default info) [$ARGUS_LOG_LEVEL]")
	f.String("log-format", "", "log format: pretty or json (default pretty) [$ARGUS_LOG_FORMAT]")

	f.String("tunnel", "", "expose the gateway via a tunnel: cloudflare[:quick|remote|local] — mode inferred from --cloudflare-* flags when omitted (requires gateway mode)")
	f.String("cloudflare-token", "", "[remote] Cloudflare tunnel token [$ARGUS_CLOUDFLARE_TOKEN]")
	f.String("cloudflare-tunnel-name", "", "[local] name of the tunnel argus creates (if absent) and runs (default: argus) [$ARGUS_CLOUDFLARE_TUNNEL_NAME]")
	f.String("cloudflare-hostname", "", "[local] public hostname argus routes to the tunnel [$ARGUS_CLOUDFLARE_HOSTNAME]")

	return cmd
}

// gatewayBaseURL is a best-effort reachable base URL (scheme://host[:port], no
// path) for this gateway, used as the pairing-QR default before a tunnel URL is
// known. Falls back to the local hostname when the listener binds all interfaces;
// `argus pair --url` overrides it when this guess is wrong.
func gatewayBaseURL(cfg *config.Config) string {
	if cfg.Gateway.URL != "" {
		return cfg.Gateway.URL
	}
	host, port, err := net.SplitHostPort(cfg.Gateway.ListenAddr)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		if h, e := os.Hostname(); e == nil {
			host = h
		}
	}
	if port == "" {
		return "ws://" + host
	}
	return "ws://" + net.JoinHostPort(host, port)
}

// tokenAuth builds an auth predicate that requires an exact token match, or nil
// (allow all) when want is empty.
func tokenAuth(want string) func(string) bool {
	if want == "" {
		return nil
	}
	return func(got string) bool { return got == want }
}

// setupLogger resolves the log level/format from config and installs the global logger,
// before anything logs. config.LogLevel is a *slog.LevelVar the handler reads live.
func setupLogger(cfg *config.Config) error {
	var lvl applog.Level
	if err := lvl.UnmarshalText([]byte(cfg.Log.Level)); err != nil {
		return fmt.Errorf("invalid log level %q (use trace, debug, info, warn, error, or fatal)", cfg.Log.Level)
	}
	config.LogLevel.Set(lvl.Level())
	config.LogFormat = cfg.Log.Format
	logger.Init()
	return nil
}

// reconcileInstalledHooks keeps already-installed Claude Code hooks in sync with this
// binary's event set, so a newly added hook takes effect without a manual reinstall.
// Best-effort: a no-op if hooks were never installed, and failures are logged, not fatal.
func reconcileInstalledHooks() {
	if added, err := claudecode.ReconcileIfInstalled(detectArgusBin()); err != nil {
		shell.StdErrF("argus start: reconcile hooks: %v\n", err)
	} else if len(added) > 0 {
		shell.StdErrF("argus start: reconciled argus hooks, added: %v\n", added)
	}
}

// connectGateway dials an upstream gateway in the background when one is configured, so
// the node enrolls itself as a source. A no-op when no --gateway is set.
func connectGateway(ctx context.Context, cfg *config.Config, d *node.Node) error {
	if !uplinkMode(cfg) {
		return nil
	}
	wsURL, gatewayClient, err := resolveGatewayURL(cfg.Gateway.URL, routeNode, logger.Scoped("gateway-ssh").L)
	if err != nil {
		return err
	}
	shell.StdErrF("argus start: connecting to gateway %s\n", cfg.Gateway.URL)
	go d.ConnectGateway(ctx, wsURL, cfg.Token, gatewayClient)
	return nil
}

// setupPush wires device push notifications (gateway mode): a device target store
// behind push.register/unregister, a Web Push (UnifiedPush) sender signing with a
// self-generated VAPID key, and a watcher that notifies registered devices when a
// session needs the user. The watcher stops when ctx is cancelled.
func setupPush(ctx context.Context, agg *gateway.Aggregator, hsrv *gateway.Server) {
	log := logger.Scoped("push").L
	store := push.NewStore(config.GetStatePath("push-tokens"))
	// VAPID key (self-generated, persisted) signs Web Push requests; the public
	// half is served to devices (push.vapidKey) to bind their subscription.
	vapid, err := push.LoadOrCreateVAPID(config.GetStatePath("vapid_key.pem"))
	if err != nil {
		shell.StdErrF("argus start: push: vapid disabled: %v\n", err)
	} else {
		hsrv.SetVAPIDPublicKey(vapid.PublicKey())
	}
	dispatcher := push.NewDispatcher(store, push.NewUnifiedPushSender(vapid), log)
	hsrv.SetPush(store, dispatcher)

	events, cancel := agg.Subscribe()
	broadcaster := fanoutNotifier{agg: agg, log: log}
	go func() {
		defer cancel()
		push.Watch(ctx, events, []push.Sink{dispatcher, broadcaster}, log)
	}()
}

// serveGateway starts the co-located gateway in gateway mode (no --gateway + token set):
// it aggregates the local node (in-process) plus nodes that dial in, serves clients over
// WebSocket, and supervises the optional outbound tunnel. It returns the *http.Server
// (nil when not in gateway mode) for the caller to shut down. A fatal listener/tunnel
// error sets nodeFailed and calls stop.
func serveGateway(ctx context.Context, cfg *config.Config, d *node.Node, tun tunnel.Provider, tunOrigin string, stop context.CancelFunc, nodeFailed *atomic.Bool) *http.Server {
	if !gatewayMode(cfg) {
		return nil
	}
	agg := gateway.New(0)
	agg.AddSource(gateway.NewInProcessSource(d.ID(), d.Label(), d.Capabilities(), d.Registry(), d.DispatchFunc()))
	// Client connections authenticate with either the master token (admin: the TUI
	// and `argus pair`/`unpair`) or a minted per-client token (revocable devices).
	store := clienttoken.New(config.GetStatePath("client-tokens"))
	clientAuth := func(tok string) bool {
		return (cfg.Token != "" && tok == cfg.Token) || store.Authorize(tok)
	}
	hsrv := gateway.NewServer(agg, tokenAuth(cfg.Token), clientAuth)
	hsrv.SetClientTokens(store, cfg.Token)
	hsrv.SetPublicURL(gatewayBaseURL(cfg))
	hsrv.SetLogger(logger.Scoped("gateway").L)
	setupPush(ctx, agg, hsrv)
	httpSrv := &http.Server{Addr: cfg.Gateway.ListenAddr, Handler: hsrv.Handler()}
	shell.StdErrF("argus start: gateway listening on %s (ws://…/node, ws://…/client)\n", cfg.Gateway.ListenAddr)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			shell.StdErrF("argus start: gateway listener: %v\n", err)
			nodeFailed.Store(true)
			stop() // bring the node down if the gateway can't serve
		}
	}()

	if tun != nil {
		sup := tunnel.Supervisor{Logger: logger.Scoped("tunnel").L}
		shell.StdErrF("argus start: opening %s tunnel to %s\n", tun.Name(), tunOrigin)
		go func() {
			rerr := sup.Run(ctx, tun, tunOrigin, func(u string) {
				hsrv.SetPublicURL(u)
				shell.StdErrF("argus start: tunnel public URL: %s — run `argus pair` to add a device\n", u)
			})
			if rerr != nil && ctx.Err() == nil {
				shell.StdErrF("argus start: tunnel: %v\n", rerr)
				nodeFailed.Store(true)
				stop() // bring the node down if a requested tunnel can't come up
			}
		}()
	}
	return httpSrv
}
