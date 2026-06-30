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
	"github.com/MunifTanjim/argus/internal/tunnel"
)

// uplinkMode reports whether this node dials an upstream gateway (then it doesn't
// listen). gatewayMode reports whether it serves as a gateway (no upstream, a token to
// require). Neither = a local node (unix socket only).
func uplinkMode(cfg *config.Config) bool  { return cfg.Gateway.URL != "" }
func gatewayMode(cfg *config.Config) bool { return cfg.Gateway.URL == "" && cfg.Token != "" }

// newStartCmd builds `argus start`: runs the local node (discovery + tmux control + the
// unix-socket API), and optionally serves as a gateway (no --gateway + token set) or
// connects to one (--gateway).
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
			logger.Scoped("node").Info("starting argus node", "version", version)
			if config.RuntimeDirIsFallback {
				logger.Scoped("config").Warn("XDG runtime dir unavailable; using fallback", "dir", config.RuntimeDir)
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
			d.SetVersion(version)
			// Standalone node logs to the configured logger (the embedded node, sharing a
			// TUI's terminal, stays at its discard default).
			d.SetLogger(logger.Scoped("node").L)
			clickCmd := desktopClickCmd(cfg) // shared by the node's desktop notifier and the local Watch below
			d.SetDesktopNotify(cfg.Push.Desktop.Enabled, clickCmd)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// A locally-managed Cloudflare tunnel needs an origin cert: run interactive
			// `cloudflared tunnel login` when on a terminal, else fail fast. No-op
			// otherwise.
			if cf, ok := tun.(tunnel.Cloudflare); ok {
				if err := ensureCloudflareLogin(ctx, cf, isatty.IsTerminal(os.Stdin.Fd())); err != nil {
					return fail(cmd, err)
				}
			}

			reconcileInstalledHooks()

			if err := connectGateway(ctx, cfg, d); err != nil {
				return fail(cmd, err)
			}

			// Set when a background subsystem fails fatally; read after d.Run so the
			// process exits non-zero.
			var nodeFailed atomic.Bool
			httpSrv := serveGateway(ctx, cfg, d, tun, tunOrigin, stop, &nodeFailed)

			// Plain local node: nothing upstream drives desktop notifications, so run a
			// local Watch over our own registry. (Gateway mode reaches the node via
			// Fanout; uplink mode via the gateway's push.desktop RPC.)
			if cfg.Push.Desktop.Enabled && !uplinkMode(cfg) && !gatewayMode(cfg) {
				events, cancel := d.Registry().Subscribe()
				// Focus-aware sink: suppresses alerts for a session already on screen.
				go func() {
					defer cancel()
					push.Watch(ctx, events, []push.Sink{d.DesktopSink()}, logger.Scoped("push").L)
				}()
			}

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

	// Flags default to zero; real defaults come from config (viper), so a flag only
	// overrides when set. See resolveConfig.
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

// gatewayBaseURL is a best-effort reachable base URL for this gateway, the pairing-QR
// default before a tunnel URL is known. Falls back to the hostname when the listener
// binds all interfaces; `argus pair --url` overrides a wrong guess.
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

// setupLogger resolves log level/format from config and installs the global logger
// before anything logs.
func setupLogger(cfg *config.Config) error {
	if err := applyLogLevel(cfg); err != nil {
		return err
	}
	config.LogFormat = cfg.Log.Format
	logger.Init()
	return nil
}

// applyLogLevel sets the global config.LogLevel WITHOUT installing the stderr handler
// (logger.Init). The TUI uses this so the embedded node honors the level while leaving
// stderr untouched — the stderr handler would corrupt the alt-screen.
func applyLogLevel(cfg *config.Config) error {
	var lvl applog.Level
	if err := lvl.UnmarshalText([]byte(cfg.Log.Level)); err != nil {
		return fmt.Errorf("invalid log level %q (use trace, debug, info, warn, error, or fatal)", cfg.Log.Level)
	}
	config.LogLevel.Set(lvl.Level())
	return nil
}

// reconcileInstalledHooks syncs already-installed Claude Code hooks with this binary's
// event set, so a new hook takes effect without a manual reinstall. Best-effort: no-op
// if never installed, failures logged not fatal.
func reconcileInstalledHooks() {
	log := logger.Scoped("hooks")
	if added, err := claudecode.ReconcileIfInstalled(detectArgusBin()); err != nil {
		log.Error("reconcile hooks failed", "err", err)
	} else if len(added) > 0 {
		log.Info("reconciled argus hooks", "added", added)
	}
}

// connectGateway dials an upstream gateway in the background to enroll the node as a
// source. No-op when no --gateway is set.
func connectGateway(ctx context.Context, cfg *config.Config, d *node.Node) error {
	if !uplinkMode(cfg) {
		return nil
	}
	wsURL, gatewayClient, err := resolveGatewayURL(cfg.Gateway.URL, routeNode, logger.Scoped("gateway-ssh").L)
	if err != nil {
		return err
	}
	go d.ConnectGateway(ctx, wsURL, cfg.Token, gatewayClient)
	return nil
}

// setupPush wires device push notifications (gateway mode): a token store, a Web Push
// (UnifiedPush) sender signing with a self-generated VAPID key, and a watcher that
// notifies registered devices when a session needs the user.
func setupPush(ctx context.Context, agg *gateway.Aggregator, hsrv *gateway.Server) {
	log := logger.Scoped("push")
	store := push.NewStore(config.GetStatePath("push-tokens"))
	// VAPID key (self-generated, persisted) signs Web Push requests; the public
	// half is served to devices (push.vapidKey) to bind their subscription.
	vapid, err := push.LoadOrCreateVAPID(config.GetStatePath("vapid_key.pem"))
	if err != nil {
		log.Warn("vapid disabled", "err", err)
	} else {
		hsrv.SetVAPIDPublicKey(vapid.PublicKey())
	}
	dispatcher := push.NewDispatcher(store, push.NewUnifiedPushSender(vapid), log.L)
	hsrv.SetPush(store, dispatcher)

	events, cancel := agg.Subscribe()
	broadcaster := fanoutNotifier{agg: agg, log: log.L}
	go func() {
		defer cancel()
		push.Watch(ctx, events, []push.Sink{dispatcher, broadcaster}, log.L)
	}()
}

// serveGateway starts the co-located gateway (gateway mode only): aggregates the local
// node plus dialed-in nodes, serves clients over WebSocket, supervises the optional
// tunnel. Returns the *http.Server (nil when not in gateway mode) to shut down. A fatal
// listener/tunnel error sets nodeFailed and calls stop.
func serveGateway(ctx context.Context, cfg *config.Config, d *node.Node, tun tunnel.Provider, tunOrigin string, stop context.CancelFunc, nodeFailed *atomic.Bool) *http.Server {
	if !gatewayMode(cfg) {
		return nil
	}
	agg := gateway.New(0)
	agg.AddSource(gateway.NewInProcessSource(d.ID(), d.Label(), d.Version(), d.Capabilities(), d.Registry(), d.DispatchFunc()))
	// Client connections authenticate with either the master token (admin: the TUI
	// and `argus pair`/`unpair`) or a minted per-client token (revocable devices).
	store := clienttoken.New(config.GetStatePath("client-tokens"))
	clientAuth := func(tok string) bool {
		return (cfg.Token != "" && tok == cfg.Token) || store.Authorize(tok)
	}
	gatewayLog := logger.Scoped("gateway")
	hsrv := gateway.NewServer(agg, tokenAuth(cfg.Token), clientAuth)
	hsrv.SetClientTokens(store, cfg.Token)
	hsrv.SetVersion(version)
	hsrv.SetPublicURL(gatewayBaseURL(cfg))
	hsrv.SetLogger(gatewayLog.L)
	setupPush(ctx, agg, hsrv)
	httpSrv := &http.Server{Addr: cfg.Gateway.ListenAddr, Handler: hsrv.Handler()}
	gatewayLog.Info("gateway listening", "addr", cfg.Gateway.ListenAddr)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			gatewayLog.Error("gateway listener failed", "err", err)
			nodeFailed.Store(true)
			stop() // bring the node down if the gateway can't serve
		}
	}()

	if tun != nil {
		tunnelLog := logger.Scoped("tunnel")
		sup := tunnel.Supervisor{Logger: tunnelLog.L}
		tunnelLog.Info("opening tunnel", "provider", tun.Name(), "origin", tunOrigin)
		go func() {
			rerr := sup.Run(ctx, tun, tunOrigin, func(u string) {
				hsrv.SetPublicURL(u)
				tunnelLog.Info("tunnel public URL; run `argus pair` to add a device", "url", u)
			})
			if rerr != nil && ctx.Err() == nil {
				tunnelLog.Error("tunnel failed", "err", rerr)
				nodeFailed.Store(true)
				stop() // bring the node down if a requested tunnel can't come up
			}
		}()
	}
	return httpSrv
}
