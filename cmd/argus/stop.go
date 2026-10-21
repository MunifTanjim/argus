package main

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

var stopWaitTimeout = 10 * time.Second

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "stop",
		Short:         "Stop the running argus node and gateway",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := stopServers(cmd.OutOrStdout(), upgradeSocket(cmd)); err != nil {
				return fail(cmd, err)
			}
			return nil
		},
	}
	addSocketFlag(cmd.Flags())
	return cmd
}

func stopServers(out io.Writer, socket string) error {
	daemons, err := runningDaemons(socket)
	if err != nil {
		return err
	}
	defer func() {
		for _, d := range daemons {
			d.client.Close()
		}
	}()
	if len(daemons) == 0 {
		fmt.Fprintln(out, "argus is not running")
		return removeLaunchState(socket)
	}
	for _, d := range daemons {
		if err := stopDaemon(d); err != nil {
			return err
		}
		fmt.Fprintf(out, "stopped %s (%s)\n", d.kind, d.socket)
	}
	return removeLaunchState(socket)
}

func stopDaemon(d daemonConn) error {
	if err := d.client.Call(api.MethodServerStop, nil, nil); err != nil {
		if api.IsMethodNotFound(err) {
			return fmt.Errorf("%s at %s does not support stop; stop it another way", d.kind, d.socket)
		}
		running, rerr := localNodeRunning(d.socket)
		if rerr != nil || running {
			return errors.Join(fmt.Errorf("stop %s at %s: %w", d.kind, d.socket, err), rerr)
		}
		return nil
	}
	deadline := time.Now().Add(stopWaitTimeout)
	for {
		running, err := localNodeRunning(d.socket)
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s at %s did not stop", d.kind, d.socket)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
