package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"slices"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/config"
)

// combinedMode runs a local node and serves a gateway. argus start then runs
// each one as a managed child, so that each restarts in place alone.
func combinedMode(cfg *config.Config) bool { return runLocalNode(cfg) && serveGatewayMode(cfg) }

// runSupervisor does the interactive tunnel setup here, because the children
// have no terminal. It blocks until ctx is done or a child exits. The node
// child reconciles the hooks, so the supervisor does not.
func runSupervisor(ctx context.Context, cmd *cobra.Command, cfg *config.Config) error {
	if err := validateMode(cfg); err != nil {
		return fail(cmd, err)
	}
	if err := setupLogger(cfg, false); err != nil {
		return fail(cmd, err)
	}
	tun, _, err := resolveTunnel(tunnelOpts(cfg, true))
	if err != nil {
		return fail(cmd, err)
	}
	if err := prepareTunnel(ctx, tun, isatty.IsTerminal(os.Stdin.Fd())); err != nil {
		return fail(cmd, err)
	}
	if err := refuseRunning(gatewaySocketPath(cfg.Socket)); err != nil {
		return fail(cmd, err)
	}
	addr, bound, err := resolveListenAddr(cfg.Gateway.ListenAddr)
	if err != nil {
		return fail(cmd, err)
	}
	color := isatty.IsTerminal(os.Stderr.Fd())
	g, err := startManagedGroup(supervisedSpecs(cmd, cfg, addr, bound, color), os.Stderr)
	if err != nil {
		return fail(cmd, err)
	}
	if err := waitSupervised(ctx, g); err != nil {
		return fail(cmd, err)
	}
	return nil
}

// waitSupervised treats a child exit after ctx is done as part of the
// shutdown: under systemd the children get the stop signal too.
func waitSupervised(ctx context.Context, g *managedGroup) error {
	defer g.stop(managedStopTimeout)
	select {
	case <-ctx.Done():
		return nil
	case err := <-g.Exited():
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("exit status 0")
		}
		return fmt.Errorf("a managed server exited: %w", err)
	}
}

func supervisedSpecs(cmd *cobra.Command, cfg *config.Config, listenAddr string, bound *net.TCPAddr, colorStderr bool) []spawnSpec {
	cfgArgs := configArgs(cmd)
	logArgs := setFlags("log-level", cfg.Log.Level, "log-format", cfg.Log.Format)
	var env []string
	if colorStderr && cfg.Log.Format != "json" {
		env = append(env, "CLICOLOR_FORCE=1")
	}
	gwEnv := slices.Clone(env)
	if cfg.Tunnel.Cloudflare.Token != "" {
		gwEnv = append(gwEnv, "ARGUS_CLOUDFLARE_TOKEN="+cfg.Tunnel.Cloudflare.Token)
	}
	tunnelArgs := setFlags(
		"tunnel", cfg.Tunnel.Provider,
		"cloudflare-tunnel-name", cfg.Tunnel.Cloudflare.TunnelName,
		"cloudflare-hostname", cfg.Tunnel.Cloudflare.Hostname,
		"external-url", cfg.Tunnel.External.URL,
		"zrok-name", cfg.Tunnel.Zrok.Name,
		"ngrok-domain", cfg.Tunnel.Ngrok.Domain,
	)
	return []spawnSpec{
		{
			role:       roleGateway,
			socket:     cfg.Socket,
			listenAddr: listenAddr,
			token:      cfg.Token,
			configArgs: cfgArgs,
			extraArgs:  append(tunnelArgs, logArgs...),
			extraEnv:   gwEnv,
		},
		{
			socket:     cfg.Socket,
			gatewayURL: "ws://" + loopbackDialAddr(bound),
			token:      cfg.Token,
			configArgs: cfgArgs,
			extraArgs:  append(setFlags("id", cfg.Node.ID, "label", cfg.Node.Label), logArgs...),
			extraEnv:   env,
		},
	}
}

// setFlags gives --name=value for each name and value pair with a value. An
// empty value is left to the child, which resolves the same config.
func setFlags(pairs ...string) []string {
	var out []string
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			out = append(out, "--"+pairs[i]+"="+pairs[i+1])
		}
	}
	return out
}
