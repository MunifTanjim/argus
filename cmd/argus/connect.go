package main

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/trustpin"
	"github.com/MunifTanjim/argus/internal/tui"
)

// connect returns a client that re-dials with backoff if the connection drops. With a
// gateway URL it always uses the channel client (Noise when E2EE is enabled, identity
// cipher otherwise). The local unix socket uses api.NewReconnectingClient. When head is
// non-nil the E2E client enforces the trust log (locked mode); otherwise it uses TOFU.
func connect(ctx context.Context, cfg *config.Config, gatewayURL, token, socket string, head []byte) (tui.Client, error) {
	c, err := dialClient(ctx, cfg, gatewayURL, token, socket, head)
	if err != nil {
		shell.StdErrF("argus: %v\n", err)
		return nil, err
	}
	return c, nil
}

// dialClient is connect without the printing: the error text carries what connect prints.
func dialClient(ctx context.Context, cfg *config.Config, gatewayURL, token, socket string, head []byte) (tui.Client, error) {
	dial, err := gatewayDialer(gatewayURL, token, socket)
	if err != nil {
		return nil, err
	}
	if gatewayURL != "" {
		if !cfg.E2EE.Enabled {
			c, err := client.NewReconnectingPlainClient(ctx, dial)
			if err != nil {
				return nil, fmt.Errorf("cannot connect to gateway at %s: %w", gatewayURL, err)
			}
			return c, nil
		}
		if len(head) > 0 {
			static, ierr := e2e.LoadOrCreateIdentity(config.GetStatePath("client-identity.json"))
			if ierr != nil {
				return nil, fmt.Errorf("client identity: %w", ierr)
			}
			c, err := client.NewReconnectingE2EClientLocked(ctx, dial, head, static, config.GetStatePath("client-trustlog-chain"))
			if err != nil {
				return nil, fmt.Errorf("cannot establish e2e session with gateway at %s: %w", gatewayURL, err)
			}
			return c, nil
		}
		c, err := client.NewReconnectingE2EClient(ctx, dial)
		if err != nil {
			return nil, fmt.Errorf("cannot connect to gateway at %s: %w", gatewayURL, err)
		}
		return c, nil
	}
	c, err := api.NewReconnectingClient(ctx, dial) // local unix socket only
	if err != nil {
		return nil, fmt.Errorf("cannot connect to argusd at %s: %w", socket, err)
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

// localNodeRunning reports whether a node is already listening on the socket. An
// api.SocketAbsent error means "not running"; any other dial error is returned as a real failure.
func localNodeRunning(socket string) (bool, error) {
	conn, err := net.Dial("unix", socket)
	if err == nil {
		conn.Close()
		return true, nil
	}
	if api.SocketAbsent(err) {
		return false, nil
	}
	return false, err
}

func refuseRunning(socket string) error {
	running, err := localNodeRunning(socket)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("argus is already running at %s; run 'argus stop' first", socket)
	}
	return nil
}

// connectLocalSpawn starts an isolated managed node and connects to its socket.
func connectLocalSpawn(ctx context.Context, cfg *config.Config, spec spawnSpec) (tui.Client, []string, error) {
	return connectLocalSpawnWithGateway(ctx, cfg, spec, "", "", nil)
}

// connectLocalSpawnWithGateway starts a managed node. Empty gatewayURL: an
// isolated node, and the TUI drives its socket. Set gatewayURL: the node uplinks
// to that gateway, and the TUI drives the gateway to see the whole fleet.
// genesisHash is the client's resolved trust root; nil means TOFU.
func connectLocalSpawnWithGateway(ctx context.Context, cfg *config.Config, spec spawnSpec, gatewayURL, token string, genesisHash []byte) (tui.Client, []string, error) {
	if gatewayURL != "" {
		wsURL, gatewayClient, err := resolveGatewayURL(gatewayURL, routeNode, nil)
		if err != nil {
			shell.StdErrF("argus: %v\n", err)
			return nil, nil, err
		}
		// The node's uplink retries silently, so report a bad host or token now.
		probe, err := api.DialWSConn(ctx, wsURL, token, gatewayClient)
		if err != nil {
			shell.StdErrF("argus: cannot reach gateway at %s: %v\n", gatewayURL, err)
			return nil, nil, err
		}
		probe.Close()
		spec.gatewayURL, spec.token = gatewayURL, token
	}
	return spawnAndConnect(ctx, cfg, []spawnSpec{spec}, gatewayURL, genesisHash)
}

// connectLocalGateway starts a managed gateway (pairing and push, no tunnel)
// and a managed node that uplinks to it, then points the TUI at the gateway
// over loopback. It returns the gateway URL.
func connectLocalGateway(ctx context.Context, cfg *config.Config, spec spawnSpec) (tui.Client, []string, string, error) {
	addr, bound, err := resolveListenAddr(cfg.Gateway.ListenAddr)
	if err != nil {
		shell.StdErrF("argus: cannot bind gateway listener at %s: %v\n", cfg.Gateway.ListenAddr, err)
		return nil, nil, "", err
	}
	head, err := resolveTrustHead(cfg)
	if err != nil {
		shell.StdErrF("argus: %v\n", err)
		return nil, nil, "", err
	}
	url := "ws://" + loopbackDialAddr(bound)
	gw := spec
	gw.role, gw.listenAddr, gw.token = roleGateway, addr, cfg.Token
	node := spec
	node.gatewayURL, node.token = url, cfg.Token
	c, logs, err := spawnAndConnect(ctx, cfg, []spawnSpec{gw, node}, url, head)
	return c, logs, url, err
}

// resolveTrustHead gives the trust log head a client pins, or nil when unpinned.
func resolveTrustHead(cfg *config.Config) ([]byte, error) {
	pin, err := trustpin.Resolve(cfg.Lock.Genesis, clientPinFile())
	if err != nil {
		return nil, fmt.Errorf("refusing to connect open: %w", err)
	}
	return pin.Genesis, nil
}

// spawnAndConnect starts the servers detached, so that they outlive the TUI,
// and returns their log paths.
func spawnAndConnect(ctx context.Context, cfg *config.Config, specs []spawnSpec, gatewayURL string, genesisHash []byte) (tui.Client, []string, error) {
	for i := range specs {
		specs[i].logFile = serverLogPath(specs[i].socket, specs[i].role)
	}
	servers, err := startDetachedGroup(specs)
	if err != nil {
		shell.StdErrF("argus: %v\n", err)
		return nil, nil, err
	}
	last := specs[len(specs)-1]
	c, err := connect(ctx, cfg, gatewayURL, last.token, last.socket, genesisHash)
	if err != nil {
		if serr := stopSpawned(servers); serr != nil {
			shell.StdErrF("argus: %v\n", serr)
		}
		return nil, nil, err
	}
	logs := make([]string, len(specs))
	for i, spec := range specs {
		logs[i] = spec.logFile
	}
	return c, logs, nil
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
