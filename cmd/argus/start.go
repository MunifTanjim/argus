package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/logfile"
	"github.com/MunifTanjim/argus/internal/logger"
	applog "github.com/MunifTanjim/argus/internal/logger/log"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/selfupgrade"
	"github.com/MunifTanjim/argus/internal/tunnel"
)

// Run topology. --mode is authoritative when set; an empty --mode infers from
// URL/token: a gateway when a token is set with no upstream, otherwise a node.
func runLocalNode(cfg *config.Config) bool { return cfg.Mode != "gateway" }
func serveGatewayMode(cfg *config.Config) bool {
	if cfg.Mode == "gateway" {
		return true
	}
	return cfg.Mode == "" && cfg.Gateway.URL == "" && cfg.Token != ""
}
func uplinkMode(cfg *config.Config) bool { return cfg.Gateway.URL != "" }

func tunnelOpts(cfg *config.Config, runGateway bool) tunnelOptions {
	return tunnelOptions{
		provider:     cfg.Tunnel.Provider,
		cfToken:      cfg.Tunnel.Cloudflare.Token,
		cfTunnelName: cfg.Tunnel.Cloudflare.TunnelName,
		cfHostname:   cfg.Tunnel.Cloudflare.Hostname,
		externalURL:  cfg.Tunnel.External.URL,
		zrokName:     cfg.Tunnel.Zrok.Name,
		ngrokDomain:  cfg.Tunnel.Ngrok.Domain,
		runGateway:   runGateway,
		listenAddr:   cfg.Gateway.ListenAddr,
		logLevel:     config.LogLevel.Level(),
	}
}

// prepareTunnel runs the provider's login or token setup: interactively at a
// terminal, else it fails fast. No-op without a tunnel.
func prepareTunnel(ctx context.Context, tun tunnel.Provider, interactive bool) error {
	switch t := tun.(type) {
	case tunnel.Cloudflare:
		return ensureCloudflareLogin(ctx, t, interactive)
	case *tunnel.Zrok:
		return ensureZrokEnabled(ctx, t.Bin, interactive)
	case tunnel.Ngrok:
		return ensureNgrokAuth(ctx, t.Bin, interactive)
	}
	return nil
}

func validateMode(cfg *config.Config) error {
	switch cfg.Mode {
	case "":
		return nil
	case "node":
		if cfg.Token != "" && cfg.Gateway.URL == "" {
			return fmt.Errorf("--token in --mode node is for connecting to a gateway; add --gateway, or use --mode gateway to run one")
		}
		return nil
	case "gateway":
		if cfg.Token == "" {
			return fmt.Errorf("--mode gateway requires --token")
		}
		if cfg.Gateway.URL != "" {
			return fmt.Errorf("--mode gateway does not connect upstream; remove --gateway")
		}
		return nil
	default:
		return fmt.Errorf("invalid --mode %q (use 'node' or 'gateway')", cfg.Mode)
	}
}

// runStart is split from the cobra RunE so tests can drive the node/gateway wiring
// with a cancellable context instead of a real signal.
func runStart(ctx context.Context, stop context.CancelFunc, cmd *cobra.Command, cfg *config.Config, version string, managed bool, logOut io.Writer) error {
	if err := validateMode(cfg); err != nil {
		return fail(cmd, err)
	}
	if err := setupLogger(cfg, logOut, managed); err != nil {
		return fail(cmd, err)
	}
	local := runLocalNode(cfg)
	if local {
		if err := refuseRunning(cfg.Socket); err != nil {
			return fail(cmd, err)
		}
		// The TUI records the servers it starts; any state file left now is stale.
		if logPath, _ := cmd.Flags().GetString("argus-log-file"); logPath == "" {
			if err := removeLaunchState(cfg.Socket); err != nil {
				return fail(cmd, err)
			}
		}
	}
	onTTY := isatty.IsTerminal(os.Stdin.Fd())
	if local {
		logger.Scoped("node").Info("starting argus node", "version", version)
	} else {
		logger.Scoped("gateway").Info("starting argus gateway", "version", version)
	}
	var upgrades *selfupgrade.Service
	if up, err := selfupgrade.New(version); err != nil {
		logger.Scoped("upgrade").Warn("in-place upgrade unavailable", "err", err)
	} else {
		upgrades = selfupgrade.NewService(up, func() { requestRestart(up.ExecPath, stop) })
	}
	if config.RuntimeDirIsFallback {
		logger.Scoped("config").Warn("XDG runtime dir unavailable; using fallback", "dir", config.RuntimeDir)
	}

	tun, tunOrigin, err := resolveTunnel(tunnelOpts(cfg, !local))
	if err != nil {
		return fail(cmd, err)
	}

	var d *node.Node
	if local {
		d = node.New()
		d.SetIdentity(cfg.Node.ID, cfg.Node.Label)
		d.SetVersion(version)
		d.SetStop(stop)
		d.SetSelfUpgrade(upgrades)
		d.SetWakelockPath(config.GetStatePath("wakelock.json"))
		d.SetMirrorAffixes(cfg.Tmux.MirrorSessionPrefix, cfg.Tmux.MirrorSessionSuffix)
		d.SetLogger(logger.Scoped("node").L)
		if err := enableProjectRegistry(d, cfg, logger.Scoped("node").L); err != nil {
			return fail(cmd, err)
		}
		if err := setWorkspaceTemplates(d, cfg); err != nil {
			return fail(cmd, err)
		}
		clickCmd := desktopClickCmd(cfg) // shared by the node's desktop notifier and the local Watch below
		d.SetDesktopNotify(cfg.Push.Desktop.Enabled, clickCmd)
	}

	if err := prepareTunnel(ctx, tun, onTTY); err != nil {
		return fail(cmd, err)
	}

	if local && cfg.E2EE.Enabled {
		if err := configureNodeLock(d, cfg, slog.Default().With("scope", "node")); err != nil {
			return fail(cmd, err)
		}
	}

	if local {
		go reconcileHooks(logger.Scoped("hooks").L, adapter.InstallOptions{ArgusBin: cfg.Bin, Socket: node.ModSocketPath(cfg.Socket)})
		if err := connectGateway(ctx, cfg, d); err != nil {
			return fail(cmd, err)
		}
	}

	// Set when a background subsystem fails fatally; read after the blocking
	// wait so the process exits non-zero.
	var nodeFailed atomic.Bool
	var httpSrv *http.Server
	var tunnelWG sync.WaitGroup
	var gwSock net.Listener
	if !local {
		// A running gateway holds the TCP port too, and its socket gives the
		// clearer error.
		if err := refuseRunning(gatewaySocketPath(cfg.Socket)); err != nil {
			return fail(cmd, err)
		}
		// TCP first: a managed gateway is ready once its unix socket answers.
		ln, err := net.Listen("tcp", cfg.Gateway.ListenAddr)
		if err != nil {
			return fail(cmd, err)
		}
		gwSock, err = api.ListenUnix(gatewaySocketPath(cfg.Socket), logger.Scoped("gateway").L)
		if err != nil {
			ln.Close()
			return fail(cmd, err)
		}
		defer gwSock.Close()
		httpSrv = serveGateway(ctx, gatewayServeOpts{
			token:         cfg.Token,
			listener:      ln,
			log:           logger.New(context.Background()).L,
			onFatal:       func() { nodeFailed.Store(true); stop() },
			version:       version,
			publicURL:     gatewayBaseURL(cfg),
			enablePairing: true,
			enablePush:    true,
			tunnel:        tun,
			tunnelOrigin:  tunOrigin,
			selfUpgrade:   upgrades,
			tunnelWG:      &tunnelWG,
		})
	}

	// A node drives its own push loop when it uplinks (any cipher) or runs E2EE
	// alone. An uplink node gets a push store in either cipher mode, because
	// push.register and push.test reach nodes over relay channels.
	if local && (cfg.E2EE.Enabled || uplinkMode(cfg)) {
		if uplinkMode(cfg) {
			d.SetPushStore(push.NewStore(config.GetStatePath("push-tokens")))
		}
		go d.StartPush(ctx, cfg.Push.Mobile.Delay)
	}

	// A node with no uplink and no E2EE runs a local Watch for desktop alerts.
	if local && cfg.Push.Desktop.Enabled && !uplinkMode(cfg) && !cfg.E2EE.Enabled {
		events, cancel := d.Registry().Subscribe()
		go func() {
			defer cancel()
			push.Watch(ctx, events, push.Sinks{Immediate: []push.Sink{d.DesktopSink()}}, logger.Scoped("push").L)
		}()
	}

	if local {
		err = d.Run(ctx, cfg.Socket) // blocks until the signal cancels ctx
	} else {
		err = serveGatewaySocket(ctx, gwSock, version, upgrades, stop)
	}

	if httpSrv != nil {
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
	}
	// An early d.Run or socket failure leaves ctx live; the tunnel child exits
	// only once it is cancelled.
	stop()
	tunnelWG.Wait()
	if err != nil {
		return fail(cmd, err)
	}
	if nodeFailed.Load() {
		return errSilent // a background subsystem already printed the cause
	}
	return nil
}

// gatewaySocketPath keeps a gateway off the node's socket, so both can run on
// one host.
func gatewaySocketPath(nodeSocket string) string {
	return filepath.Join(filepath.Dir(nodeSocket), "argus-gateway.sock")
}

// serveGatewaySocket lets `argus upgrade` reach a gateway. It blocks until ctx
// is done.
func serveGatewaySocket(ctx context.Context, l net.Listener, version string, upgrades *selfupgrade.Service, stop func()) error {
	srv := api.NewServer()
	srv.Handle(api.MethodServerInfo, func(context.Context, json.RawMessage) (any, error) {
		return api.ServerInfo{Version: version, StartedAt: api.StartedAtRFC3339()}, nil
	})
	upgrades.Register(srv)
	api.HandleStop(srv, stop)
	return srv.ServeContext(ctx, l)
}

const serverLogMaxSize = 10 << 20

// newStartCmd builds `argus start`: runs the local node (discovery + tmux control + the
// unix-socket API), and optionally serves as a gateway (no --gateway + token set) or
// connects to one (--gateway). See --mode to force a standalone gateway or node-only.
func newStartCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "start",
		Short:         "Run a node (optionally a gateway)",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Logging through the log file, not fd 2, keeps rotation sizes exact.
			var logOut io.Writer = os.Stderr
			logPath, _ := cmd.Flags().GetString("argus-log-file")
			if logPath != "" {
				lf, err := logfile.Open(logPath, serverLogMaxSize)
				if err != nil {
					return fail(cmd, err)
				}
				defer lf.Close()
				if err := lf.RedirectStd(); err != nil {
					return fail(cmd, err)
				}
				logOut = lf
			}
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			managed, _ := cmd.Flags().GetBool("argus-managed")
			if managed {
				setupManagedChild(os.Stdin, stop, logPath == "")
			}
			if combinedMode(cfg) {
				return runSupervisor(ctx, cmd, cfg, logOut)
			}
			return runStart(ctx, stop, cmd, cfg, version, managed, logOut)
		},
	}

	// Flags default to zero; real defaults come from config (viper), so a flag only
	// overrides when set. See resolveConfig.
	f := cmd.Flags()
	f.String("socket", "", "unix socket path for the local JSON-RPC API (default: XDG runtime path)")
	f.String("id", "", "stable node id announced to a gateway (default: hostname)")
	f.String("label", "", "human-friendly node name shown in clients (default: hostname)")

	f.String("mode", "", "run mode: 'node' (local node) or 'gateway' (standalone gateway, no local node); default: inferred from --token/--gateway [$ARGUS_MODE]")

	f.String("listen-addr", "", "address for the gateway's WebSocket listener when serving as a gateway (see --mode) (default :8443; terminate TLS via a tunnel, ssh, or a reverse proxy)")

	f.String("gateway", "", "connect to a gateway (the /node route is implicit): ws(s)://host, or ssh://[user@]host[:ssh-port][?port=N] to tunnel over SSH [$ARGUS_GATEWAY_URL]")
	f.String("token", "", "gateway token: required from incoming clients/nodes when this node is a gateway, and presented to the --gateway it connects to (empty: allow all) [$ARGUS_TOKEN]")

	f.String("log-level", "", "log verbosity: trace, debug, info, warn, error, fatal (default info) [$ARGUS_LOG_LEVEL]")
	f.String("log-format", "", "log format: pretty or json (default pretty) [$ARGUS_LOG_FORMAT]")

	f.String("tunnel", "", "expose the gateway via a tunnel: cloudflare, zrok, ngrok, or external")
	f.String("cloudflare-token", "", "[remote] Cloudflare tunnel token [$ARGUS_CLOUDFLARE_TOKEN]")
	f.String("cloudflare-tunnel-name", "", "[local] name of the tunnel argus creates (if absent) and runs (default: argus) [$ARGUS_CLOUDFLARE_TUNNEL_NAME]")
	f.String("cloudflare-hostname", "", "[local] public hostname argus routes to the tunnel [$ARGUS_CLOUDFLARE_HOSTNAME]")
	f.String("external-url", "", "[external] the gateway's public URL for pairing QRs, e.g. wss://host[/base-path] [$ARGUS_EXTERNAL_URL]")
	f.String("zrok-name", "", "[zrok] reserved name for a stable URL: 'namespace:name' or 'name' (default: argus) [$ARGUS_ZROK_NAME]")
	f.String("ngrok-domain", "", "[ngrok] reserved/custom domain (default: the account's static dev domain) [$ARGUS_NGROK_DOMAIN]")

	f.Bool("argus-managed", false, "")
	_ = f.MarkHidden("argus-managed")
	f.String("argus-log-file", "", "")
	_ = f.MarkHidden("argus-log-file")

	_ = cmd.RegisterFlagCompletionFunc("tunnel", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return tunnelFlagCompletions(), cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("mode", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"node", "gateway"}, cobra.ShellCompDirectiveNoFileComp
	})

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

// setupLogger resolves log level/format from config and installs the global logger,
// writing to w, before anything logs. A managed server's parent sets CLICOLOR_FORCE for our
// log output only, so tmux and setup scripts must not inherit it; startEnv
// keeps it for a restart.
func setupLogger(cfg *config.Config, w io.Writer, managed bool) error {
	if err := applyLogLevel(cfg); err != nil {
		return err
	}
	config.LogFormat = cfg.Log.Format
	logger.InitWriter(w)
	if managed {
		_ = os.Unsetenv("CLICOLOR_FORCE")
	}
	return nil
}

// applyLogLevel sets the global config.LogLevel WITHOUT installing the stderr handler
// (logger.Init). The TUI uses this so it leaves stderr untouched: the stderr handler
// would corrupt the alt-screen.
func applyLogLevel(cfg *config.Config) error {
	var lvl applog.Level
	if err := lvl.UnmarshalText([]byte(cfg.Log.Level)); err != nil {
		return fmt.Errorf("invalid log level %q (use trace, debug, info, warn, error, or fatal)", cfg.Log.Level)
	}
	config.LogLevel.Set(lvl.Level())
	return nil
}

// reconcileHooks skips agents that were never run. Best-effort, and slow when
// it runs the claude CLI, so callers run it in the background.
func reconcileHooks(log *slog.Logger, opts adapter.InstallOptions) {
	for _, a := range adapters.All() {
		path, err := a.SettingsPath()
		if err != nil {
			log.Warn("reconcile hooks failed", "agent", a.Agent(), "err", err)
			continue
		}
		if path != "" && !dirExists(filepath.Dir(path)) {
			continue
		}
		added, err := a.Reconcile(opts)
		switch {
		case errors.Is(err, adapter.ErrAgentNotInstalled):
		case err != nil:
			log.Warn("reconcile hooks failed", "agent", a.Agent(), "err", err)
		case len(added) > 0:
			log.Info("reconciled argus hooks", "agent", a.Agent(), "added", added)
		}
	}
}

// watchParent stops a managed server once the TUI that owns it is gone. The TUI
// holds the write end of stdin, and the kernel closes it when the TUI exits.
func watchParent(r io.Reader, stop context.CancelFunc) {
	_, _ = io.Copy(io.Discard, r)
	stop()
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

// tunnelURLTimeout: a tunnel running this long without reporting a public URL is
// treated as fatal. Long enough for a slow first connect, short enough to beat a
// pairing attempt. A var so tests can shrink it.
var tunnelURLTimeout = 60 * time.Second

// gatewayServeOpts configures serveGateway. The listener is pre-bound by the
// caller so bind errors surface before backgrounding; log is the base (unscoped)
// logger — serveGateway derives the gateway/tunnel/push scopes from it.
type gatewayServeOpts struct {
	token         string
	listener      net.Listener
	log           *slog.Logger
	onFatal       func() // listener/tunnel death handler; may be nil
	version       string
	publicURL     string
	enablePairing bool
	enablePush    bool
	tunnel        tunnel.Provider
	tunnelOrigin  string
	selfUpgrade   *selfupgrade.Service // nil: no server.* upgrade methods
	tunnelWG      *sync.WaitGroup      // done once the tunnel child has exited; required with tunnel
}

// serveGateway serves clients and dialed-in nodes over o.listener and, when
// enabled, wires client-token pairing, mobile push, and a tunnel. Returns the
// *http.Server to shut down.
func serveGateway(ctx context.Context, o gatewayServeOpts) *http.Server {
	agg := gateway.New(0)

	var store *clienttoken.Store
	if o.enablePairing {
		store = clienttoken.New(config.GetStatePath("client-tokens"))
	}
	// Client connections authenticate with the master token; when pairing is on,
	// minted per-client tokens also pass.
	token := o.token
	clientAuth := func(tok string) bool {
		return (token != "" && tok == token) || (store != nil && store.Authorize(tok))
	}

	gwLog := o.log.With("scope", "gateway")
	hsrv := gateway.NewServer(agg, tokenAuth(o.token), clientAuth)
	if store != nil {
		hsrv.SetClientTokens(store, o.token)
	}
	hsrv.SetVersion(o.version)
	hsrv.SetPublicURL(o.publicURL)
	hsrv.SetLogger(gwLog)
	hsrv.SetSelfUpgrade(o.selfUpgrade)
	if o.enablePush {
		setupBlindPush(hsrv, o.log.With("scope", "push"))
	}

	httpSrv := &http.Server{Handler: hsrv.Handler()}
	gwLog.Info("gateway listening", "addr", o.listener.Addr().String())
	go func() {
		if err := httpSrv.Serve(o.listener); err != nil && err != http.ErrServerClosed {
			gwLog.Error("gateway listener failed", "err", err)
			if o.onFatal != nil {
				o.onFatal()
			}
		}
	}()

	if o.tunnel != nil {
		tunLog := o.log.With("scope", "tunnel")
		sup := tunnel.Supervisor{Logger: tunLog}
		tunLog.Info("opening tunnel", "provider", o.tunnel.Name(), "origin", o.tunnelOrigin)
		// If no URL arrives, pairing would silently hand out the LAN URL from
		// gatewayBaseURL — fail instead of serving a wrong QR. Also catches a child the
		// supervisor crash-restarts forever.
		var gotURL atomic.Bool
		urlTimer := time.AfterFunc(tunnelURLTimeout, func() {
			// CAS, not Load: Stop() loses to a timer already firing, so a URL landing at
			// exactly t=timeout would otherwise be both published and declared missing.
			if !gotURL.CompareAndSwap(false, true) || ctx.Err() != nil {
				return
			}
			tunLog.Error("tunnel opened but reported no public URL; pairing would use the local URL",
				"provider", o.tunnel.Name(), "timeout", tunnelURLTimeout)
			if o.onFatal != nil {
				o.onFatal()
			}
		})
		o.tunnelWG.Add(1)
		go func() {
			defer o.tunnelWG.Done()
			defer urlTimer.Stop()
			rerr := sup.Run(ctx, o.tunnel, o.tunnelOrigin, func(u string) {
				gotURL.Store(true)
				urlTimer.Stop()
				hsrv.SetPublicURL(u)
				tunLog.Info("tunnel public URL; run `argus pair` to add a device", "url", u)
			})
			if rerr != nil && ctx.Err() == nil {
				tunLog.Error("tunnel failed", "err", rerr)
				if o.onFatal != nil {
					o.onFatal()
				}
			}
		}()
	}
	return httpSrv
}

// setupBlindPush wires the gateway side of push: the gateway holds the VAPID
// key and relays opaque push.deliver bodies that nodes encrypted.
func setupBlindPush(hsrv *gateway.Server, log *slog.Logger) {
	vapid, err := push.LoadOrCreateVAPID(config.GetStatePath("vapid_key.pem"))
	if err != nil {
		log.Warn("vapid disabled", "err", err)
		return
	}
	hsrv.SetVAPIDPublicKey(vapid.PublicKey())
	tokenPath := config.GetStatePath("pushport-token")
	ppHost := ""
	if u, err := url.Parse(pushPortBaseURL); err == nil {
		ppHost = u.Host
	}
	var pp *push.PushPortAuth
	if tok, _ := push.ReadInstanceToken(tokenPath); tok != "" && ppHost != "" {
		pp = &push.PushPortAuth{Host: ppHost, Token: tok}
	}
	dv := push.NewGatewayDeliverer(vapid, pp)
	hsrv.SetPushDeliverer(dv)
	hsrv.SetPushPortTokenSetter(func(tok string) error {
		if err := push.WriteInstanceToken(tokenPath, tok); err != nil {
			return err
		}
		dv.SetPushPort(ppHost, tok)
		return nil
	})
	hsrv.SetPushPortStatus(func() bool { return dv.HasPushPort() })
}

// setupManagedChild runs before anything starts a process. Processes we start
// (tmux, setup scripts) must not inherit the token. The TUI reads our output; once
// it is gone, a log write must fail, not kill us before shutdown. Notify, not
// Ignore, so that children get the default SIGPIPE back on exec. A detached
// child has no parent to watch: its stdin is /dev/null, which reads EOF at once.
func setupManagedChild(parent io.Reader, stop context.CancelFunc, watch bool) {
	_ = os.Unsetenv("ARGUS_TOKEN")
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	if watch {
		go watchParent(parent, stop)
	}
}
