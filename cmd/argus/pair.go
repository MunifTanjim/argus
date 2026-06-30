package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
)

// newPairCmd builds `argus pair`: ask the gateway for a fresh per-client token, show its
// pairing QR, and wait for a device to connect. The token is persisted (and thus
// revocable via `argus unpair`) only once a device actually connects.
func newPairCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:           "pair",
		Short:         "Pair a new client device with the gateway",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			if cfg.Gateway.URL == "" {
				return fail(cmd, fmt.Errorf("--gateway ws(s)://host is required (pairing talks to the gateway over the wire)"))
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, err := dialGatewayClient(ctx, cfg.Gateway.URL, cfg.Token)
			if err != nil {
				return fail(cmd, fmt.Errorf("connect: %w", err))
			}
			defer client.Close()

			var start api.PairStartResult
			if err := client.Call(api.MethodClientsPairStart, nil, &start); err != nil {
				return fail(cmd, fmt.Errorf("start pairing: %w", err))
			}

			qrURL := start.URL
			if u, _ := cmd.Flags().GetString("url"); u != "" {
				qrURL = u
			}
			if qrURL == "" {
				return fail(cmd, fmt.Errorf("gateway did not report a public URL; pass --url wss://host"))
			}

			shell.StdOutF("scan to pair a device (expires in %s):\n", timeout)
			if err := printPairingQR(os.Stdout, qrURL, start.Token); err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("\nwaiting for the device to connect…\n")

			// pairAwait blocks server-side until the device connects; bound it client-side
			// with --timeout by closing the connection, which unblocks the in-flight Call.
			type res struct {
				out api.PairAwaitResult
				err error
			}
			done := make(chan res, 1)
			go func() {
				var out api.PairAwaitResult
				e := client.Call(api.MethodClientsPairAwait, api.PairAwaitParams{Token: start.Token}, &out)
				done <- res{out, e}
			}()
			select {
			case r := <-done:
				if r.err != nil {
					return fail(cmd, fmt.Errorf("await pairing: %w", r.err))
				}
				if !r.out.Connected {
					return fail(cmd, fmt.Errorf("no device connected within %s", timeout))
				}
			case <-time.After(timeout):
				return fail(cmd, fmt.Errorf("no device connected within %s", timeout))
			}

			shell.StdOutF("paired: device connected and token saved\n")
			return nil
		},
	}
	f := cmd.Flags()
	addGatewayClientFlags(f)
	f.String("url", "", "override the base URL embedded in the QR (default: the gateway's public URL)")
	f.DurationVar(&timeout, "timeout", 60*time.Second, "how long to wait for the device to connect")
	return cmd
}

// addGatewayClientFlags registers the over-the-wire flags shared by `pair`/`unpair`:
// gateway URL and master bearer token. (No --socket: these always talk to a gateway.)
func addGatewayClientFlags(f *pflag.FlagSet) {
	f.String("gateway", "", "gateway to manage (the /client route is implicit): ws(s)://host, or ssh://[user@]host[:ssh-port][?port=N] [$ARGUS_GATEWAY_URL]")
	f.String("token", "", "master bearer token for the gateway (admin) [$ARGUS_TOKEN]")
}

// dialGatewayClient opens a one-shot client RPC connection to a gateway's /client
// route over WebSocket.
func dialGatewayClient(ctx context.Context, gatewayURL, token string) (*api.Client, error) {
	wsURL, httpClient, err := resolveGatewayURL(gatewayURL, routeClient, nil)
	if err != nil {
		return nil, err
	}
	return api.DialWS(ctx, wsURL, token, httpClient)
}
