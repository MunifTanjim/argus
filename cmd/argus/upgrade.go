package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/cmd/argus/completion"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/selfupgrade"
	"github.com/MunifTanjim/argus/internal/shell"
)

var restartWaitTimeout = 30 * time.Second

// upgradeInProcessFn is a var so tests can stub the network download.
var upgradeInProcessFn = upgradeInProcess

// newUpgradeCmd builds `argus upgrade`: swap in the latest release and restart
// the running argus in place.
func newUpgradeCmd() *cobra.Command {
	var noRestart, noDownload, beta bool
	cmd := &cobra.Command{
		Use:           "upgrade",
		Short:         "Upgrade to the latest version and restart the running argus",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			daemons, err := runningDaemons(upgradeSocket(cmd))
			if err != nil {
				return fail(cmd, err)
			}
			defer func() {
				for _, d := range daemons {
					d.client.Close()
				}
			}()
			steps, err := planUpgrade(noRestart, noDownload, beta, len(daemons) > 0)
			if err != nil {
				return fail(cmd, err)
			}
			if len(daemons) == 0 {
				ctx, cancel := context.WithTimeout(cmd.Context(), selfupgrade.Timeout)
				defer cancel()
				return upgradeInProcessFn(ctx, cmd, steps.beta)
			}
			for _, d := range daemons {
				if err := upgradeDaemonWithin(cmd, d, steps, daemonPrefix(d, len(daemons))); err != nil {
					return err
				}
			}
			return nil
		},
	}
	addSocketFlag(cmd.Flags())
	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "replace the binary without restarting the running argus")
	cmd.Flags().BoolVar(&noDownload, "no-download", false, "restart the running argus with the binary on disk")
	cmd.Flags().BoolVar(&beta, "beta", false, "upgrade to the newest release, betas included")
	return cmd
}

// daemonUpgradeTimeout outlasts the daemon's own download budget, so the CLI
// does not report a failure for a swap that is about to succeed.
const daemonUpgradeTimeout = selfupgrade.Timeout + time.Minute

func daemonPrefix(d daemonConn, n int) string {
	if n > 1 {
		return d.kind + ": "
	}
	return ""
}

func upgradeDaemonWithin(cmd *cobra.Command, d daemonConn, steps upgradeSteps, prefix string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), daemonUpgradeTimeout)
	defer cancel()
	return upgradeDaemon(ctx, cmd, d, steps, prefix)
}

// upgradeSocket falls back to --socket or the default path on an invalid
// config, which must not block the upgrade that may fix it.
func upgradeSocket(cmd *cobra.Command) string {
	cfg, err := resolveConfig(cmd)
	if err == nil {
		return cfg.Socket
	}
	shell.StdErrF("Warning: ignoring config: %v\n", err)
	if f := cmd.Flags().Lookup("socket"); f != nil && f.Changed {
		return f.Value.String()
	}
	return config.DefaultSocket()
}

type upgradeSteps struct{ download, restart, beta bool }

func planUpgrade(noRestart, noDownload, beta, daemon bool) (upgradeSteps, error) {
	switch {
	case noRestart && noDownload:
		return upgradeSteps{}, errors.New("--no-restart with --no-download leaves nothing to do")
	case beta && noDownload:
		return upgradeSteps{}, errors.New("--beta with --no-download downloads nothing")
	case !daemon && noDownload:
		return upgradeSteps{}, errors.New("no running argus process")
	case !daemon:
		return upgradeSteps{download: true, beta: beta}, nil
	}
	return upgradeSteps{download: !noDownload, restart: !noRestart, beta: beta}, nil
}

type daemonConn struct {
	kind   string
	socket string
	client *api.Client
}

func runningDaemons(nodeSocket string) ([]daemonConn, error) {
	var daemons []daemonConn
	for _, d := range []daemonConn{
		{kind: "node", socket: nodeSocket},
		{kind: "gateway", socket: gatewaySocketPath(nodeSocket)},
	} {
		c, err := dialDaemon(d.socket)
		if err != nil {
			for _, d := range daemons {
				d.client.Close()
			}
			return nil, err
		}
		if c != nil {
			d.client = c
			daemons = append(daemons, d)
		}
	}
	return daemons, nil
}

func dialDaemon(socket string) (*api.Client, error) {
	c, err := api.Dial(socket)
	if err == nil {
		return c, nil
	}
	if api.SocketAbsent(err) {
		return nil, nil
	}
	return nil, fmt.Errorf("connect to %s: %w", socket, err)
}

var errNoInPlaceRestart = errors.New("the running argus cannot restart itself; restart it by hand")

// The prefix keeps the output of several daemons apart.
func upgradeDaemon(ctx context.Context, cmd *cobra.Command, d daemonConn, steps upgradeSteps, prefix string) error {
	if steps.download {
		shell.StdErrLn(prefix + "Upgrading the running argus...")
		var c api.CheckUpgradeResult
		err := d.client.CallContext(ctx, api.MethodServerCheckUpgrade, api.UpgradeParams{Beta: steps.beta, Fresh: true}, &c)
		if api.IsMethodNotFound(err) {
			if err := upgradeInProcessFn(ctx, cmd, steps.beta); err != nil {
				return err
			}
			shell.StdErrLn(prefix + "The running argus cannot restart itself. Restart it to use the new version.")
			return nil
		}
		if err != nil {
			return fail(cmd, fmt.Errorf("%supgrade check failed: %w", prefix, err))
		}
		if steps.beta && !c.Beta {
			// A daemon from before --beta ignores it, so swap the binary here
			// and restart the daemon onto it.
			if err := upgradeInProcessFn(ctx, cmd, true); err != nil {
				return err
			}
		} else {
			// Daemons before the Newer check in Apply would swap a build made
			// after the latest tag back to that tag.
			if printVersions(prefix, c.Current, c.Latest) {
				return nil
			}
			var r api.UpgradeResult
			if err := d.client.CallContext(ctx, api.MethodServerUpgrade, api.UpgradeParams{Beta: steps.beta}, &r); err != nil {
				return fail(cmd, fmt.Errorf("%supgrade failed: %w", prefix, err))
			}
			if r.To == r.From {
				shell.StdErrLn(prefix + "Already on the latest version!")
				return nil
			}
			printUpgraded(cmd, prefix, r.To)
		}
	}
	if !steps.restart {
		return nil
	}
	if err := restartDaemon(ctx, d.client, d.socket, prefix); err != nil {
		return fail(cmd, fmt.Errorf("%s%w", prefix, err))
	}
	return nil
}

// printVersions reports whether current is already the latest version.
func printVersions(prefix, current, latest string) bool {
	shell.StdErrF("%sCurrent version: %s\n", prefix, current)
	shell.StdErrF("%sLatest version: %s\n", prefix, latest)
	if !selfupgrade.Newer(latest, current) {
		shell.StdErrLn(prefix + "Already on the latest version!")
		return true
	}
	return false
}

func restartDaemon(ctx context.Context, c *api.Client, socket, prefix string) error {
	var before api.ServerInfo
	if err := c.CallContext(ctx, api.MethodServerInfo, nil, &before); err != nil {
		return fmt.Errorf("read server info: %w", err)
	}
	err := c.CallContext(ctx, api.MethodServerRestart, nil, nil)
	if api.IsMethodNotFound(err) {
		return errNoInPlaceRestart
	}
	if err != nil {
		return fmt.Errorf("restart failed: %w", err)
	}
	shell.StdErrLn(prefix + "Restarting...")
	info, err := waitForRestart(ctx, socket, before.StartedAt, restartWaitTimeout)
	if err != nil {
		return err
	}
	shell.StdErrF("%sRestarted argus %s!\n", prefix, info.Version)
	return nil
}

func waitForRestart(ctx context.Context, socket, oldStartedAt string, timeout time.Duration) (api.ServerInfo, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := probeServerInfo(socket); err == nil && info.StartedAt != "" && info.StartedAt != oldStartedAt {
			return info, nil
		}
		select {
		case <-ctx.Done():
			return api.ServerInfo{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return api.ServerInfo{}, fmt.Errorf("argus did not come back within %s", timeout)
}

func probeServerInfo(socket string) (api.ServerInfo, error) {
	c, err := api.Dial(socket)
	if err != nil {
		return api.ServerInfo{}, err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var info api.ServerInfo
	err = c.CallContext(ctx, api.MethodServerInfo, nil, &info)
	return info, err
}

func upgradeInProcess(ctx context.Context, cmd *cobra.Command, beta bool) error {
	up, err := selfupgrade.New(cmd.Root().Version)
	if err != nil {
		return fail(cmd, err)
	}
	c, err := up.Check(ctx, beta)
	if err != nil {
		return fail(cmd, err)
	}
	if printVersions("", c.Current, c.Latest) {
		return nil
	}
	shell.StdErrF("Downloading %s...\n", c.Latest)
	_, to, err := up.Apply(ctx, beta)
	if err != nil {
		return fail(cmd, err)
	}
	printUpgraded(cmd, "", to)
	return nil
}

// printUpgraded reports success before refreshing completion, so a completion
// problem does not mask that the upgrade itself succeeded.
func printUpgraded(cmd *cobra.Command, prefix, tag string) {
	shell.StdErrF("%sUpgraded to %s!\n", prefix, tag)
	switch err := completion.Install(cmd); {
	case err == nil:
		shell.StdErrLn(prefix + "Updated shell completion!")
	case errors.Is(err, completion.ErrCompletionNotEnabled):
	default:
		shell.StdErrF("%sWarning: failed to refresh shell completion: %v\n", prefix, err)
	}
}
