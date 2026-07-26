package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
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

// resolveGenesis extracts the single agreed-upon genesis from a set of offered
// chains. Returns (nil, nil) only when no chains were offered. Returns an error
// when chains were received but none decoded (possible format mismatch,
// corruption, or gateway manipulation) or when more than one distinct genesis
// is present — competing roots mean either two networks share this gateway or
// someone is offering a fake one; picking by branch length would choose a trust
// root by popularity.
func resolveGenesis(chains [][]byte) ([]byte, error) {
	if len(chains) == 0 {
		return nil, nil
	}
	var seen [][]byte
	for _, chain := range chains {
		entries, err := trustlog.UnmarshalChain(chain)
		if err != nil || len(entries) == 0 {
			continue
		}
		seen = append(seen, trustlog.HashEntry(&entries[0]))
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("gateway offered %d chain(s) but none could be decoded; possible format mismatch or corruption", len(chains))
	}
	switch all := distinctGenesis(seen); len(all) {
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

// pullChains fetches the gateway's retained trust-log branches.
func pullChains(ctx context.Context, cfg *config.Config) ([][]byte, error) {
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
	return got.Chains, nil
}

// genesisFromNetwork pulls the gateway's retained branches and delegates
// resolution to resolveGenesis.
func genesisFromNetwork(ctx context.Context, cfg *config.Config) ([]byte, error) {
	chains, err := pullChains(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return resolveGenesis(chains)
}

// quarantiningGenesis returns the genesis that would quarantine an unpinned device
// here, or nil. It mirrors the detector rather than resolveGenesis: detection trips
// on the FIRST chain that decodes, so competing roots — an error when choosing a pin
// — are still a quarantine when reporting one.
func quarantiningGenesis(ctx context.Context, cfg *config.Config) ([]byte, error) {
	chains, err := pullChains(ctx, cfg)
	if err != nil {
		return nil, err
	}
	for _, chain := range chains {
		entries, uerr := trustlog.UnmarshalChain(chain)
		if uerr != nil || len(entries) == 0 {
			continue
		}
		return trustlog.HashEntry(&entries[0]), nil
	}
	return nil, nil
}

// applyPin pins the node role first and writes the client pin only once the node has
// accepted the genesis. The other order leaves node=X, client=Y on a rejected pin:
// the client then resolves Y, never quarantines, and silently opens zero channels —
// an empty dashboard with no error anywhere.
func applyPin(ctx context.Context, cfg *config.Config, genesis []byte) error {
	nodePinned, err := pinLocalNode(ctx, cfg, genesis)
	if err != nil {
		return err
	}
	if err := clientPinFile().Save(genesis); err != nil {
		return err
	}
	shell.StdOutF("pinned this device\n  genesis: %s\n  %s\n", base64.StdEncoding.EncodeToString(genesis), fingerprintOf(genesis))
	if nodePinned {
		shell.StdOutF("  local node pinned and enforcing\n")
	}
	return nil
}

// pinLocalNode pins the node role over RPC, reporting whether it took. A node that
// REFUSES the genesis aborts the whole command — the two roles on one machine must
// never end up on different trust roots. A node that is merely not running does not:
// its persisted pin is checked instead, and the operator is told to re-run.
func pinLocalNode(ctx context.Context, cfg *config.Config, genesis []byte) (bool, error) {
	_, err := callLocal[struct{}](ctx, cfg, api.MethodLockPin, api.LockPinParams{Genesis: genesis})
	if err == nil {
		return true, nil
	}
	var rpcErr *api.RPCError
	if errors.As(err, &rpcErr) {
		return false, fmt.Errorf("the local node refused this genesis: %s\n  nothing was written; this device is unchanged", rpcErr.Message)
	}
	if perr := guardExistingPin(nodePinFile(), genesis); perr != nil {
		return false, fmt.Errorf("the local node is unreachable (%v) and its saved pin disagrees:\n  %w", err, perr)
	}
	shell.StdErrF("note: the local node is unreachable (%v)\n  pinning the client role only; run `argus lock pin` again once the node is up\n", err)
	return false, nil
}

// configPin returns the genesis declared by lock.genesis, or nil. Both pin and unpin
// must consult it: it outranks the pin file, and trustpin.Resolve turns a file that
// disagrees with it into a hard startup failure.
func configPin(cfg *config.Config) ([]byte, error) {
	if cfg.Lock.Genesis == "" {
		return nil, nil
	}
	g, err := trustpin.Decode(cfg.Lock.Genesis)
	if err != nil {
		return nil, fmt.Errorf("lock.genesis in this device's config is unusable: %w", err)
	}
	return g, nil
}

// guardPin refuses a pin that contradicts a trust root this device already has,
// whether it comes from the config or from the client pin file.
func guardPin(cfg *config.Config, genesis []byte) error {
	cfgGenesis, err := configPin(cfg)
	if err != nil {
		return err
	}
	if cfgGenesis != nil && !bytes.Equal(cfgGenesis, genesis) {
		return fmt.Errorf("this device is pinned by config: lock.genesis is %s (%s)\n  writing a different pin would make the next `argus` run fail with a genesis pin conflict;\n  remove lock.genesis from the config first",
			base64.StdEncoding.EncodeToString(cfgGenesis), fingerprintOf(cfgGenesis))
	}
	return guardExistingPin(clientPinFile(), genesis)
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
				if perr := guardPin(cfg, genesis); perr != nil {
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
			if perr := guardExistingPin(clientPinFile(), genesis); perr != nil {
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

func guardExistingPin(pf *trustpin.File, genesis []byte) error {
	cur, err := pf.Load()
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

			cfgGenesis, cerr := configPin(cfg)
			if cerr != nil {
				return fail(cmd, cerr)
			}
			if cfgGenesis != nil {
				return fail(cmd, fmt.Errorf("this device is pinned by config: lock.genesis is %s (%s)\n  clearing the pin file would change nothing — the device stays pinned and enforcing;\n  remove lock.genesis from the config to unpin",
					base64.StdEncoding.EncodeToString(cfgGenesis), fingerprintOf(cfgGenesis)))
			}

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
				shell.StdErrF("note: local node unpin failed (%v) — it still holds its pin and is still enforcing\n  run `argus lock unpin` again when the node is reachable\n", err)
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
