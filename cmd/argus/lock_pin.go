package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/trustlog"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

func fingerprintOf(genesis []byte) string {
	return strings.Join(trustlog.HashFingerprint(genesis), " ")
}

// distinctGenesis returns the unique genesis hashes in first-seen order.
func distinctGenesis(all [][]byte) [][]byte {
	var out [][]byte
	for _, g := range all {
		seen := false
		for _, have := range out {
			if bytes.Equal(have, g) {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, append([]byte(nil), g...))
		}
	}
	return out
}

// confirmGenesis shows the genesis fingerprint and asks for explicit consent.
// Anything other than y/yes declines, so a stray newline never pins a device.
func confirmGenesis(r io.Reader, w io.Writer, genesis []byte) (bool, error) {
	fmt.Fprintf(w, "genesis offered by this network:\n  %s\n  %s\n\n",
		base64.StdEncoding.EncodeToString(genesis), fingerprintOf(genesis))
	fmt.Fprintf(w, "Compare these words against `argus lock status` on a node you trust.\nPin this device to it? [y/N]: ")
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// genesisFromNetwork pulls the gateway's retained branches and returns the single
// genesis they agree on. More than one distinct genesis is refused: competing
// roots mean either two networks share this gateway or someone is offering a fake
// one, and picking by branch length would choose a trust root by popularity.
func genesisFromNetwork(ctx context.Context, cfg *config.Config) ([]byte, error) {
	dial, err := gatewayDialer(cfg.Gateway.URL, cfg.Token, cfg.Socket)
	if err != nil {
		return nil, err
	}
	conn, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	c := api.NewClient(conn)
	defer c.Close()
	var got api.TrustLogPullResult
	if err := c.Call(api.MethodTrustLogPull, nil, &got); err != nil {
		return nil, fmt.Errorf("trustlog.pull: %w", err)
	}
	var seen [][]byte
	for _, chain := range got.Chains {
		entries, err := trustlog.UnmarshalChain(chain)
		if err != nil || len(entries) == 0 {
			continue
		}
		seen = append(seen, trustlog.HashEntry(&entries[0]))
	}
	switch all := distinctGenesis(seen); len(all) {
	case 0:
		return nil, nil
	case 1:
		return all[0], nil
	default:
		var b strings.Builder
		for _, g := range all {
			fmt.Fprintf(&b, "\n  %s  %s", base64.StdEncoding.EncodeToString(g), fingerprintOf(g))
		}
		return nil, fmt.Errorf("this gateway is offering %d different trust roots:%s\n\npin the right one explicitly: argus lock pin <genesis-b64>", len(all), b.String())
	}
}

// applyPin writes this device's client pin and, when a local node answers, pins
// it live over RPC so a quarantined node recovers without a restart.
func applyPin(ctx context.Context, cfg *config.Config, genesis []byte) error {
	if err := clientPinFile().Save(genesis); err != nil {
		return err
	}
	shell.StdOutF("pinned this device\n  genesis: %s\n  %s\n", base64.StdEncoding.EncodeToString(genesis), fingerprintOf(genesis))
	if _, err := callLocal[struct{}](ctx, cfg, api.MethodLockPin, api.LockPinParams{Genesis: genesis}); err != nil {
		shell.StdErrF("note: no local node pinned (%v)\n  if a node runs on this machine, pin it there too\n", err)
		return nil
	}
	shell.StdOutF("  local node pinned and enforcing\n")
	return nil
}

func newLockPinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "pin [genesis-b64]",
		Short:         "Pin this device to the network's trust-log genesis",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if len(args) == 1 {
				genesis, derr := trustpin.Decode(args[0])
				if derr != nil {
					return fail(cmd, derr)
				}
				if perr := guardExistingPin(genesis); perr != nil {
					return fail(cmd, perr)
				}
				return applyPin(ctx, cfg, genesis)
			}

			genesis, gerr := genesisFromNetwork(ctx, cfg)
			if gerr != nil {
				return fail(cmd, gerr)
			}
			if genesis == nil {
				shell.StdOutF("no trust log on this network; nothing to pin\n")
				return nil
			}
			if perr := guardExistingPin(genesis); perr != nil {
				return fail(cmd, perr)
			}
			ok, cerr := confirmGenesis(os.Stdin, os.Stdout, genesis)
			if cerr != nil {
				return fail(cmd, cerr)
			}
			if !ok {
				shell.StdOutF("not pinned\n")
				return nil
			}
			return applyPin(ctx, cfg, genesis)
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

// guardExistingPin refuses to overwrite a different pin already on this device.
func guardExistingPin(genesis []byte) error {
	cur, err := clientPinFile().Load()
	if err != nil {
		return err
	}
	if cur == nil || bytes.Equal(cur, genesis) {
		return nil
	}
	return fmt.Errorf("this device is already pinned to %s (%s); run `argus lock unpin` first",
		base64.StdEncoding.EncodeToString(cur), fingerprintOf(cur))
}

func newLockUnpinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "unpin",
		Short:         "Clear this device's trust-log genesis pin",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if cerr := clientPinFile().Clear(); cerr != nil {
				return fail(cmd, cerr)
			}
			// A chain from the old genesis can never ingest again; leaving it behind
			// only produces a confusing error on the next sync.
			if rerr := os.Remove(config.GetStatePath("client-trustlog-chain")); rerr != nil && !os.IsNotExist(rerr) {
				return fail(cmd, rerr)
			}
			shell.StdOutF("unpinned this device\n")
			if _, err := callLocal[struct{}](ctx, cfg, api.MethodLockUnpin, nil); err != nil {
				shell.StdErrF("note: no local node unpinned (%v)\n", err)
			} else {
				shell.StdOutF("  local node unpinned\n")
			}
			shell.StdErrF("\nThis device now has no trust root. It will refuse all channels while the\nnetwork has a trust log, until you run `argus lock pin`.\n")
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}
