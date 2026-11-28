package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
		asJSON   bool
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
			var client pinger
			var links nodeLinks
			if asJSON && cfg.Gateway.URL != "" {
				// One connection for the pings and the link snapshot.
				head, err := clientHead(cfg)
				if err != nil {
					return fail(cmd, err)
				}
				c, err := dialOneShotClient(ctx, cfg, cfg.Gateway.URL, cfg.Token, cfg.Socket, head)
				if err != nil {
					return fail(cmd, err)
				}
				defer c.Close()
				client = c
				if l, ok := c.(nodeLinks); ok {
					links = l
				}
			} else {
				conn, err := dial(ctx)
				if err != nil {
					return fail(cmd, fmt.Errorf("connect: %w", err))
				}
				c := api.NewClient(conn)
				defer c.Close()
				client = c
			}

			target := cfg.Gateway.URL
			if target == "" {
				target = cfg.Socket
			}
			if !asJSON {
				shell.StdOutF("pinging %s\n", target)
			}

			rtts := runPings(ctx, client, count, interval, func(i int, rtt time.Duration, err error) {
				if asJSON {
					return
				}
				if err != nil {
					shell.StdOutF("ping %d/%d: error: %v\n", i+1, count, err)
					return
				}
				shell.StdOutF("ping %d/%d: %s\n", i+1, count, rtt.Round(time.Microsecond))
			})

			if asJSON {
				res := pingResult{Target: target, Sent: count, Received: len(rtts), Failed: count - len(rtts), Nodes: []pingNode{}}
				var rosterErr error
				if links != nil {
					res.Nodes, rosterErr = awaitPingNodes(ctx, links, pingSettle)
					if rosterErr != nil {
						res.Error = "list nodes: " + rosterErr.Error()
					}
				}
				if err := printJSON(res); err != nil {
					return fail(cmd, err)
				}
				if len(rtts) == 0 || rosterErr != nil {
					return errSilent
				}
				return nil
			}

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
	f.BoolVar(&asJSON, "json", false, "output as JSON, with the link each node uses (gateway, tcp, ssh, or unknown when a node is online but its link is not settled)")
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

// pingResult is the --json output of ping.
type pingResult struct {
	Target   string     `json:"target"`
	Sent     int        `json:"sent"`
	Received int        `json:"received"`
	Failed   int        `json:"failed"`
	Nodes    []pingNode `json:"nodes"`
	// Error is set when the roster could not be listed: nodes is then only the
	// nodes with a link, not every online node.
	Error string `json:"error,omitempty"`
}

// pingNode is a node and the link its channel uses: gateway, tcp or ssh.
type pingNode struct {
	ID  string `json:"id"`
	Via string `json:"via"`
}

// pingUnknownVia marks a node that is online but whose link has not settled.
const pingUnknownVia = "unknown"

// pingSettle bounds the wait for every online node to report its link.
const pingSettle = 3 * time.Second

// nodeLinks is a gateway client that reports the link of each open node.
type nodeLinks interface {
	Call(method string, params, out any) error
	NodeVias() map[string]string
}

// awaitPingNodes lists every node the roster reports online with its link. It
// waits up to settle for each to have one; a node still without one is listed
// as unknown rather than left out. A roster that cannot be listed by then is
// returned as the error, with only the nodes that have a link.
func awaitPingNodes(ctx context.Context, c nodeLinks, settle time.Duration) ([]pingNode, error) {
	deadline := time.After(settle)
	for {
		var roster api.NodesListResult
		err := c.Call(api.MethodNodesList, nil, &roster)
		vias := c.NodeVias()
		out := []pingNode{}
		settled := err == nil
		seen := map[string]bool{}
		for _, n := range roster.Nodes {
			if !n.Online {
				continue
			}
			seen[n.ID] = true
			via := vias[n.ID]
			if via == "" {
				via, settled = pingUnknownVia, false
			}
			out = append(out, pingNode{ID: n.ID, Via: via})
		}
		for id, via := range vias {
			if via != "" && !seen[id] {
				out = append(out, pingNode{ID: id, Via: via})
			}
		}
		slices.SortFunc(out, func(a, b pingNode) int { return strings.Compare(a.ID, b.ID) })
		if settled {
			return out, nil
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			return out, err
		case <-ctx.Done():
			return out, err
		}
	}
}
