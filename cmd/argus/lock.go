package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

func newLockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Manage locked mode (network trust log)",
	}
	cmd.AddCommand(newLockInitCmd(), newLockStatusCmd(), newLockLogCmd(), newLockSignCmd(), newLockRevokeCmd(), newLockAddSignerCmd(), newLockRemoveSignerCmd(), newLockRevokeSignerCmd(), newLockDisableCmd(), newLockLocalDisableCmd())
	return cmd
}

// findNode returns the roster entry whose id or label matches name, or nil.
func findNode(roster []api.NodeDescriptor, name string) *api.NodeDescriptor {
	for i := range roster {
		if roster[i].ID == name || roster[i].Label == name {
			return &roster[i]
		}
	}
	return nil
}

// resolveSigners maps --signer names (node label or id) to their Ed25519 signer
// pubkeys from the roster. Errors on an unknown name or a node with no signer key.
func resolveSigners(roster []api.NodeDescriptor, names []string) ([][]byte, error) {
	out := make([][]byte, 0, len(names))
	for _, name := range names {
		nd := findNode(roster, name)
		if nd == nil {
			return nil, fmt.Errorf("unknown node %q (not in roster)", name)
		}
		if nd.SignerPubKey == "" {
			return nil, fmt.Errorf("node %q advertises no signer key", name)
		}
		pub, err := base64.StdEncoding.DecodeString(nd.SignerPubKey)
		if err != nil {
			return nil, fmt.Errorf("node %q signer pubkey: %w", name, err)
		}
		out = append(out, pub)
	}
	return out, nil
}

// gatherDevices returns every rostered node's identity pubkey (the devices to
// authorize at init). Nodes without a key (pre-E2E/co-located) are skipped.
func gatherDevices(roster []api.NodeDescriptor) [][]byte {
	out := make([][]byte, 0, len(roster))
	for _, nd := range roster {
		if nd.IdentityPubKey == "" {
			continue
		}
		pub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
		if err != nil {
			shell.StdErrF("WARN: node %q has an unparseable identity key; not authorizing it\n", nd.ID)
			continue
		}
		out = append(out, pub)
	}
	return out
}

func newLockInitCmd() *cobra.Command {
	var signers []string
	var genDisablements int
	cmd := &cobra.Command{
		Use:           "init",
		Short:         "Enable locked mode: create the trust log and authorize current nodes",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			if cfg.Gateway.URL == "" {
				return fail(cmd, fmt.Errorf("lock init needs a gateway (set gateway.url) to read the node roster"))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// 1. Roster from the gateway.
			roster, err := fetchRoster(ctx, cfg)
			if err != nil {
				return fail(cmd, err)
			}
			sigPubs, err := resolveSigners(roster, signers)
			if err != nil {
				return fail(cmd, err)
			}
			devices := gatherDevices(roster)

			// 2. lock.init on the local node.
			res, err := lockInitOnNode(ctx, cfg, api.LockInitParams{Signers: sigPubs, Devices: devices, GenDisablements: genDisablements})
			if err != nil {
				return fail(cmd, err)
			}

			// 3. Report.
			tip := base64.StdEncoding.EncodeToString(res.Tip)
			shell.StdOutF("locked mode enabled\n  genesis: %s\n  signers: %d\n", tip, res.SignerCount)
			for _, s := range res.DisablementSecrets {
				shell.StdOutF("  disablement secret: %s\n", base64.StdEncoding.EncodeToString(s))
			}
			if len(res.DisablementSecrets) > 0 {
				shell.StdErrF("\nSAVE the disablement secret(s) above NOW — shown only once. Each one disables\nlocked mode network-wide (break-glass recovery if signer keys are lost).\n")
			}
			if res.SignerCount < 2 && len(res.DisablementSecrets) == 0 {
				shell.StdErrF("\nWARNING: only one signer and no disablement secrets — if this node is lost\nor compromised there is NO recovery. Add a second signer (--signer <node>) or\ngenerate a disablement secret (--gen-disablements).\n")
			} else if res.SignerCount < 2 {
				shell.StdErrF("\nNote: only one signer. If it is lost, use a saved disablement secret to recover.\nConsider adding a second signer: argus lock init --signer <node>.\n")
			}
			if w := lockInitFewSignersWarning(res.SignerCount); w != "" {
				shell.StdErrF("%s", w)
			}
			shell.StdOutF("\nTo pin your other nodes and clients, set in their config:\n  lock.genesis: %s\n", tip)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&signers, "signer", nil, "additional signer node (label or id); repeatable")
	cmd.Flags().IntVar(&genDisablements, "gen-disablements", 1, "number of disablement (recovery) secrets to generate")
	addClientFlags(cmd.Flags())
	return cmd
}

func newLockStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "status",
		Short:         "Show locked-mode status (tip fingerprint, signers, this node's roles)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			st, err := lockStatusOnNode(ctx, cfg)
			if err != nil {
				// No local node (client-only machine): print this device's client identity
				// pubkey + how to get it authorized, offline.
				kp, ierr := e2e.LoadOrCreateIdentity(config.GetStatePath("client-identity.json"))
				if ierr != nil {
					return fail(cmd, err) // surface the original node-dial error
				}
				pub := base64.StdEncoding.EncodeToString(kp.Public)
				shell.StdOutF("locked mode: (client — no local node)\n  this device identity: %s\n  to authorize, run on a signer node:\n    %s\n", pub, lockSignHint(kp.Public))
				return nil
			}
			printLockStatus(st)
			// Enrollment hint: when this node isn't authorized yet, show the exact sign command.
			if st.Enabled && !st.Authorized && len(st.IdentityPubKey) > 0 {
				shell.StdOutF("\n  to authorize this node, run on a signer node:\n    %s\n", lockSignHint(st.IdentityPubKey))
			}
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

// lockSignHint returns the "argus lock sign <pubkey>" instruction string for
// the given raw Ed25519 public key. Used in enrollment / authorization hints.
func lockSignHint(pub []byte) string {
	return "argus lock sign " + base64.StdEncoding.EncodeToString(pub)
}

// fetchRoster dials the gateway and returns nodes.list.
func fetchRoster(ctx context.Context, cfg *config.Config) ([]api.NodeDescriptor, error) {
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
	var r api.NodesListResult
	if err := c.Call(api.MethodNodesList, nil, &r); err != nil {
		return nil, fmt.Errorf("nodes.list: %w", err)
	}
	return r.Nodes, nil
}

// callLocal dials the LOCAL node socket, sends one RPC, and returns the decoded
// result. It centralizes the dial→NewClient→Call→Close boilerplate every lock
// subcommand shares.
func callLocal[R any](ctx context.Context, cfg *config.Config, method string, params any) (R, error) {
	var res R
	dial, err := gatewayDialer("", "", cfg.Socket) // force local socket
	if err != nil {
		return res, err
	}
	conn, err := dial(ctx)
	if err != nil {
		return res, err
	}
	c := api.NewClient(conn)
	defer c.Close()
	if err := c.Call(method, params, &res); err != nil {
		return res, err
	}
	return res, nil
}

func lockInitOnNode(ctx context.Context, cfg *config.Config, p api.LockInitParams) (api.LockInitResult, error) {
	return callLocal[api.LockInitResult](ctx, cfg, api.MethodLockInit, p)
}

func lockStatusOnNode(ctx context.Context, cfg *config.Config) (api.LockStatusResult, error) {
	return callLocal[api.LockStatusResult](ctx, cfg, api.MethodLockStatus, nil)
}

// lockInitFewSignersWarning returns the warning text to print when the trust log has fewer
// than 3 signers, because the revoke-signer co-signing ceremony requires ≥3 to out-vote
// one compromised key. Returns "" for ≥3 signers.
func lockInitFewSignersWarning(signerCount int) string {
	if signerCount < 3 {
		return "\nNote: fewer than 3 signers — 'lock revoke-signer' needs ≥3 signers to out-vote\none compromised key; with fewer, recovery is 'lock disable' + reinit.\n"
	}
	return ""
}

// signerCountAfterRevoke returns how many signers from current would remain after
// removing the revoked set. Used to pre-check for a sole-root guard in revoke-signer.
func signerCountAfterRevoke(current, revoked [][]byte) int {
	revokedSet := make(map[string]bool, len(revoked))
	for _, r := range revoked {
		revokedSet[string(r)] = true
	}
	remaining := 0
	for _, c := range current {
		if !revokedSet[string(c)] {
			remaining++
		}
	}
	return remaining
}

// lockLogOnNode dials the LOCAL socket and calls lock.log.
func lockLogOnNode(ctx context.Context, cfg *config.Config) (api.LockLogResult, error) {
	return callLocal[api.LockLogResult](ctx, cfg, api.MethodLockLog, nil)
}

// printLockLogEntry prints one trust-log entry to stdout.
func printLockLogEntry(e api.LockLogEntry) {
	switch e.Kind {
	case "genesis":
		shell.StdOutF("[%d] genesis: %d signer(s)\n", e.Index, len(e.Signers))
		for _, s := range e.Signers {
			shell.StdOutF("  signer: %s\n", base64.StdEncoding.EncodeToString(s))
		}
	case "add-signer":
		shell.StdOutF("[%d] add-signer: %s\n", e.Index, base64.StdEncoding.EncodeToString(e.Target))
	case "remove-signer":
		shell.StdOutF("[%d] remove-signer: %s\n", e.Index, base64.StdEncoding.EncodeToString(e.Target))
	case "authorize-device":
		shell.StdOutF("[%d] authorize-device: %s\n", e.Index, base64.StdEncoding.EncodeToString(e.Target))
	case "revoke-device":
		shell.StdOutF("[%d] revoke-device: %s\n", e.Index, base64.StdEncoding.EncodeToString(e.Target))
	case "revoke-signer":
		shell.StdOutF("[%d] revoke-signer: %d revoked, %d co-sign(s)\n", e.Index, len(e.Revoked), e.CoSignCount)
		for _, r := range e.Revoked {
			shell.StdOutF("  revoked: %s\n", base64.StdEncoding.EncodeToString(r))
		}
		for _, r := range e.Replaces {
			shell.StdOutF("  replaces: %s\n", base64.StdEncoding.EncodeToString(r))
		}
	case "disable":
		shell.StdOutF("[%d] disable\n", e.Index)
	default:
		shell.StdOutF("[%d] %s\n", e.Index, e.Kind)
	}
}

func newLockLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "log",
		Short:         "Show trust-log history (read-only)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			res, err := lockLogOnNode(ctx, cfg)
			if err != nil {
				return fail(cmd, err)
			}
			for _, e := range res.Entries {
				printLockLogEntry(e)
			}
			if len(res.Signers) > 0 {
				fp := strings.Join(trustlog.SignerSetFingerprint(res.Signers), " ")
				shell.StdOutF("\ntip fingerprint: %s\n", fp)
			}
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

// resolveDevice maps a device argument to a 32-byte identity pubkey: a roster node's
// label or id resolves to its IdentityPubKey; otherwise the arg is parsed as a raw
// base64 pubkey (which must be 32 bytes).
func resolveDevice(roster []api.NodeDescriptor, arg string) ([]byte, error) {
	if nd := findNode(roster, arg); nd != nil {
		if nd.IdentityPubKey == "" {
			return nil, fmt.Errorf("node %q advertises no identity key", arg)
		}
		pub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
		if err != nil {
			return nil, fmt.Errorf("node %q identity pubkey: %w", arg, err)
		}
		return pub, nil
	}
	pub, err := base64.StdEncoding.DecodeString(arg)
	if err != nil || len(pub) != 32 {
		return nil, fmt.Errorf("device %q is neither a known node (label/id) nor a 32-byte base64 pubkey", arg)
	}
	return pub, nil
}

func newLockSignCmd() *cobra.Command {
	return newLockDeviceCmd("sign", "Authorize a device", api.MethodLockSign)
}
func newLockRevokeCmd() *cobra.Command {
	return newLockDeviceCmd("revoke-device", "Revoke a device", api.MethodLockRevoke)
}

func newLockAddSignerCmd() *cobra.Command {
	return newLockSignerCmd("add-signer", "Add a trusted signer", api.MethodLockAddSigner)
}
func newLockRemoveSignerCmd() *cobra.Command {
	return newLockSignerCmd("remove-signer", "Remove a trusted signer", api.MethodLockRemoveSigner)
}

func newLockSignerCmd(use, short, method string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           use + " <signer>",
		Short:         short + " (node label/id or base64 signer pubkey)",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// Resolve the signer pubkey: node label/id via roster, or raw base64.
			roster, _ := fetchRoster(ctx, cfg) // best-effort
			pubs, err := resolveSignerArgs(roster, []string{args[0]})
			if err != nil {
				return fail(cmd, err)
			}
			pub := pubs[0]
			res, err := lockSignerOnNode(ctx, cfg, method, pub)
			if err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("%s ok\n  current tip (audit): %s\n", use, base64.StdEncoding.EncodeToString(res.Tip))
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

func lockSignerOnNode(ctx context.Context, cfg *config.Config, method string, signer []byte) (api.LockDeviceResult, error) {
	return callLocal[api.LockDeviceResult](ctx, cfg, method, api.LockSignerParams{Signer: signer})
}

func newLockDeviceCmd(use, short, method string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           use + " <device>",
		Short:         short + " (node label/id or base64 identity pubkey)",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Resolve the device: try a raw pubkey first (no gateway needed); if that
			// fails and a gateway is configured, resolve against the roster.
			device, derr := resolveDevice(nil, args[0])
			if derr != nil {
				if cfg.Gateway.URL == "" {
					return fail(cmd, fmt.Errorf("%v (no gateway configured to resolve a node name)", derr))
				}
				roster, rerr := fetchRoster(ctx, cfg)
				if rerr != nil {
					return fail(cmd, rerr)
				}
				if device, derr = resolveDevice(roster, args[0]); derr != nil {
					return fail(cmd, derr)
				}
			}

			res, err := lockDeviceOnNode(ctx, cfg, method, device)
			if err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("%s ok\n  current tip (audit): %s\n", use, base64.StdEncoding.EncodeToString(res.Tip))
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

// lockDeviceOnNode dials the LOCAL node socket and calls the sign/revoke method.
func lockDeviceOnNode(ctx context.Context, cfg *config.Config, method string, device []byte) (api.LockDeviceResult, error) {
	return callLocal[api.LockDeviceResult](ctx, cfg, method, api.LockDeviceParams{Device: device})
}

// resolveSignerArgs resolves a list of signer arguments to 32-byte Ed25519 pubkeys.
// Each arg is first tried against the roster (by node label or id); if that fails,
// it is parsed as a raw base64 32-byte pubkey (mirroring the Phase-2 pattern).
func resolveSignerArgs(roster []api.NodeDescriptor, args []string) ([][]byte, error) {
	out := make([][]byte, 0, len(args))
	for _, arg := range args {
		pubs, rerr := resolveSigners(roster, []string{arg})
		if rerr == nil && len(pubs) == 1 {
			out = append(out, pubs[0])
			continue
		}
		raw, berr := base64.StdEncoding.DecodeString(arg)
		if berr != nil || len(raw) != 32 {
			if rerr != nil {
				return nil, fmt.Errorf("resolve signer %q: %w", arg, rerr)
			}
			return nil, fmt.Errorf("resolve signer %q: not a known node and not a valid 32-byte base64 pubkey", arg)
		}
		out = append(out, raw)
	}
	return out, nil
}

// revokeSignerStartOnNode dials the LOCAL socket and calls lock.revokeSignerStart.
func revokeSignerStartOnNode(ctx context.Context, cfg *config.Config, p api.LockRevokeSignerStartParams) (api.LockRevokeSignerBlobResult, error) {
	return callLocal[api.LockRevokeSignerBlobResult](ctx, cfg, api.MethodLockRevokeSignerStart, p)
}

// revokeSignerCosignOnNode dials the LOCAL socket and calls lock.revokeSignerCosign.
func revokeSignerCosignOnNode(ctx context.Context, cfg *config.Config, blob []byte) (api.LockRevokeSignerBlobResult, error) {
	return callLocal[api.LockRevokeSignerBlobResult](ctx, cfg, api.MethodLockRevokeSignerCosign, api.LockRevokeSignerCosignParams{Blob: blob})
}

// revokeSignerFinishOnNode dials the LOCAL socket and calls lock.revokeSignerFinish.
func revokeSignerFinishOnNode(ctx context.Context, cfg *config.Config, blob []byte) (api.LockRevokeSignerFinishResult, error) {
	return callLocal[api.LockRevokeSignerFinishResult](ctx, cfg, api.MethodLockRevokeSignerFinish, api.LockRevokeSignerFinishParams{Blob: blob})
}

// newLockRevokeSignerCmd implements the three-phase revoke-signer co-signing ceremony:
//
//	Start:   argus lock revoke-signer <signer...> [--replacement <node>...] [--fork-from <hash>]
//	Co-sign: argus lock revoke-signer --cosign <blob>
//	Finish:  argus lock revoke-signer --finish <blob>
func newLockRevokeSignerCmd() *cobra.Command {
	var cosignBlob string
	var finishBlob string
	var replacements []string
	var forkFrom string

	cmd := &cobra.Command{
		Use:           "revoke-signer",
		Short:         "Revoke a signer via co-signing ceremony (start / --cosign / --finish)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cosignBlob != "" && finishBlob != "" {
				return fail(cmd, fmt.Errorf("--cosign and --finish are mutually exclusive"))
			}
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// --finish mode: finalize a completed blob and apply the revocation.
			if finishBlob != "" {
				if len(args) > 0 {
					return fail(cmd, fmt.Errorf("--finish does not take positional arguments"))
				}
				blob, berr := base64.StdEncoding.DecodeString(finishBlob)
				if berr != nil {
					return fail(cmd, fmt.Errorf("--finish: invalid base64 blob: %w", berr))
				}
				res, ferr := revokeSignerFinishOnNode(ctx, cfg, blob)
				if ferr != nil {
					return fail(cmd, ferr)
				}
				shell.StdOutF("revocation applied\n  new tip (audit): %s\n", base64.StdEncoding.EncodeToString(res.Tip))
				shell.StdErrF("\nRevocation propagates to the network within ~30s.\n")
				return nil
			}

			// --cosign mode: add this node's co-sign to an existing blob.
			if cosignBlob != "" {
				if len(args) > 0 {
					return fail(cmd, fmt.Errorf("--cosign does not take positional arguments"))
				}
				blob, berr := base64.StdEncoding.DecodeString(cosignBlob)
				if berr != nil {
					return fail(cmd, fmt.Errorf("--cosign: invalid base64 blob: %w", berr))
				}
				res, cerr := revokeSignerCosignOnNode(ctx, cfg, blob)
				if cerr != nil {
					return fail(cmd, cerr)
				}
				blobStr := base64.StdEncoding.EncodeToString(res.Blob)
				shell.StdOutF("co-signed\n  blob: %s\n", blobStr)
				shell.StdErrF("\nIf more co-signs are needed, run on another signer node:\n  argus lock revoke-signer --cosign %s\n", blobStr)
				shell.StdErrF("When you have enough co-signs, run on any signer node:\n  argus lock revoke-signer --finish %s\n", blobStr)
				return nil
			}

			// Start mode: begin the ceremony.
			if len(args) == 0 {
				return fail(cmd, fmt.Errorf("revoke-signer: specify signer(s) to revoke, or use --cosign / --finish"))
			}
			roster, _ := fetchRoster(ctx, cfg) // best-effort; nil roster → raw-base64 fallback
			revoked, err := resolveSignerArgs(roster, args)
			if err != nil {
				return fail(cmd, err)
			}
			// Sole-root guard: if revoking would leave zero signers without a replacement,
			// fail immediately with a helpful message rather than letting the ceremony
			// proceed to an unfinishable state.
			if len(replacements) == 0 {
				if st, serr := lockStatusOnNode(ctx, cfg); serr == nil && st.Enabled {
					if signerCountAfterRevoke(st.Signers, revoked) < 1 {
						return fail(cmd, fmt.Errorf(
							"revocation would remove all signers and leave the log unrecoverable\n"+
								"  use --replacement <node> to atomically add a successor signer, or\n"+
								"  'argus lock disable <secret>' + reinit to abandon locked mode"))
					}
				}
			}
			var replaces [][]byte
			if len(replacements) > 0 {
				replaces, err = resolveSignerArgs(roster, replacements)
				if err != nil {
					return fail(cmd, fmt.Errorf("--replacement: %w", err))
				}
			}
			var forkFromBytes []byte
			if forkFrom != "" {
				forkFromBytes, err = base64.StdEncoding.DecodeString(forkFrom)
				if err != nil {
					return fail(cmd, fmt.Errorf("--fork-from: invalid base64: %w", err))
				}
			}
			res, serr := revokeSignerStartOnNode(ctx, cfg, api.LockRevokeSignerStartParams{
				Revoked:  revoked,
				Replaces: replaces,
				ForkFrom: forkFromBytes,
			})
			if serr != nil {
				return fail(cmd, serr)
			}
			blobStr := base64.StdEncoding.EncodeToString(res.Blob)
			shell.StdOutF("revoke-signer started\n  blob: %s\n", blobStr)
			shell.StdErrF("\nNext: run on another signer node:\n  argus lock revoke-signer --cosign %s\n", blobStr)
			shell.StdErrF("After collecting enough co-signs, run on any signer node:\n  argus lock revoke-signer --finish <blob>\n")
			shell.StdErrF("\nNote: entries appended after the fork point by revoked signers will be erased.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&cosignBlob, "cosign", "", "add this node's co-sign to a ceremony blob (from start or a prior --cosign)")
	cmd.Flags().StringVar(&finishBlob, "finish", "", "finalize a completed ceremony blob and apply the revocation")
	cmd.Flags().StringArrayVar(&replacements, "replacement", nil, "replacement signer node (label/id or base64 pubkey); repeatable")
	cmd.Flags().StringVar(&forkFrom, "fork-from", "", "override the fork-point hash (base64); default: parent of revoked signer's earliest entry")
	addClientFlags(cmd.Flags())
	return cmd
}

func newLockDisableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "disable <secret>",
		Short:         "Disable locked mode network-wide using a disablement secret",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			secret, err := base64.StdEncoding.DecodeString(args[0])
			if err != nil {
				return fail(cmd, fmt.Errorf("secret must be base64: %w", err))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			res, err := callLocal[api.LockDisableResult](ctx, cfg, api.MethodLockDisable, api.LockDisableParams{Secret: secret})
			if err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("locked mode disabled network-wide\n  current tip (audit): %s\n", base64.StdEncoding.EncodeToString(res.Tip))
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

func newLockLocalDisableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "local-disable",
		Short:         "Disable locked-mode enforcement on THIS node only (persisted escape hatch)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if _, err := callLocal[struct{}](ctx, cfg, api.MethodLockLocalDisable, nil); err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("locked-mode enforcement disabled on this node\n")
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

func b64OrNone(b []byte) string {
	if len(b) == 0 {
		return "(none)"
	}
	return base64.StdEncoding.EncodeToString(b)
}

func printLockStatus(st api.LockStatusResult) {
	if !st.Enabled {
		shell.StdOutF("locked mode: disabled\n  this node signer: %s\n  this node identity: %s\n",
			b64OrNone(st.SignerPubKey),
			b64OrNone(st.IdentityPubKey))
		if st.LocalDisabled {
			shell.StdOutF("  local-disable: active\n")
		}
		return
	}
	shell.StdOutF("locked mode: enabled\n  current tip (audit): %s\n  signers: %d\n  devices: %d\n  this node is signer: %v\n  this node authorized: %v\n",
		base64.StdEncoding.EncodeToString(st.Tip), len(st.Signers), st.DeviceCount, st.SignerTrusted, st.Authorized)
	if len(st.Signers) > 0 {
		shell.StdOutF("  trust fingerprint: %s\n", strings.Join(trustlog.SignerSetFingerprint(st.Signers), " "))
		for _, s := range st.Signers {
			shell.StdOutF("    signer: %s\n", base64.StdEncoding.EncodeToString(s))
		}
	}
	if st.Disabled {
		shell.StdOutF("  network-wide disabled: true\n")
	}
	if st.LocalDisabled {
		shell.StdOutF("  local-disable: active\n")
	}
	if st.Equivocation {
		shell.StdErrF("\n⚠ equivocation detected: node beacons diverge — the gateway may be showing inconsistent trust-log views. Compare the tip fingerprint above across your nodes out-of-band (phone/chat) to confirm they match.\n")
	}
}
