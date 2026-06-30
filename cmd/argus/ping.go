package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
)

// pinger is the subset of a client the ping loop needs (satisfied by *api.Client).
type pinger interface {
	Call(method string, params, out any) error
}

// newPingCmd builds `argus ping`: measure round-trip latency to the configured endpoint
// (a gateway via --gateway, or the local node socket) by timing a no-op ping RPC.
func newPingCmd() *cobra.Command {
	var (
		count    int
		interval time.Duration
	)
	cmd := &cobra.Command{
		Use:           "ping",
		Short:         "Measure round-trip latency to the node or gateway",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if count < 1 {
				return fail(cmd, fmt.Errorf("--count must be at least 1"))
			}
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}

			dial, err := gatewayDialer(cfg.Gateway.URL, cfg.Token, cfg.Socket)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, err := dial(ctx)
			if err != nil {
				return fail(cmd, fmt.Errorf("connect: %w", err))
			}
			client := api.NewClient(conn)
			defer client.Close()

			target := cfg.Gateway.URL
			if target == "" {
				target = cfg.Socket
			}
			shell.StdOutF("pinging %s\n", target)

			rtts := runPings(ctx, client, count, interval, func(i int, rtt time.Duration, err error) {
				if err != nil {
					shell.StdOutF("ping %d/%d: error: %v\n", i+1, count, err)
					return
				}
				shell.StdOutF("ping %d/%d: %s\n", i+1, count, rtt.Round(time.Microsecond))
			})

			shell.StdOutF("\n%d sent, %d received, %d failed\n", count, len(rtts), count-len(rtts))
			if len(rtts) > 0 {
				lo, hi, sum := rtts[0], rtts[0], time.Duration(0)
				for _, r := range rtts {
					if r < lo {
						lo = r
					}
					if r > hi {
						hi = r
					}
					sum += r
				}
				avg := sum / time.Duration(len(rtts))
				shell.StdOutF("rtt min/avg/max = %s / %s / %s\n",
					lo.Round(time.Microsecond), avg.Round(time.Microsecond), hi.Round(time.Microsecond))
			} else {
				return errSilent // every ping failed; the per-ping errors were already printed
			}
			return nil
		},
	}
	f := cmd.Flags()
	addClientFlags(f) // --socket / --gateway / --token, shared with the root client
	f.IntVar(&count, "count", 5, "number of pings to send")
	f.DurationVar(&interval, "interval", time.Second, "delay between pings")
	return cmd
}

// runPings sends count ping RPCs, waiting interval between them, reports each result, and
// returns the successful round-trip durations. A cancelled ctx stops the loop early.
func runPings(ctx context.Context, client pinger, count int, interval time.Duration, report func(i int, rtt time.Duration, err error)) []time.Duration {
	var rtts []time.Duration
	for i := 0; i < count; i++ {
		if i > 0 {
			select {
			case <-time.After(interval):
			case <-ctx.Done():
				return rtts
			}
		}
		t0 := time.Now()
		err := client.Call(api.MethodPing, nil, nil)
		rtt := time.Since(t0)
		report(i, rtt, err)
		if err == nil {
			rtts = append(rtts, rtt)
		}
	}
	return rtts
}
