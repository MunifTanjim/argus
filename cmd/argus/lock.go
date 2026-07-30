package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/keyfmt"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/trustlog"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

func newLockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Manage locked mode (network trust log)",
	}
	cmd.AddCommand(newLockInitCmd(), newLockStatusCmd(), newLockLogCmd(), newLockSignCmd(), newLockRevokeCmd(), newLockAddSignerCmd(), newLockRemoveSignerCmd(), newLockRevokeSignerCmd(), newLockDisableCmd(), newLockLocalDisableCmd(), newLockPinCmd(), newLockUnpinCmd())
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

// parseSignerKeys parses each `lock init` argument as a sigpub: key.
//
// Node names are deliberately not accepted here. A name can only become a key by
// way of the roster, which the gateway serves and which no trust log constrains at
// init time — so naming a co-signer would let the gateway substitute its own key
// into the genesis and hold a signing seat forever. A key read off
// `argus lock status` on the node itself reaches this command without passing
// through the gateway at all.
func parseSignerKeys(args []string) ([][]byte, error) {
	out := make([][]byte, 0, len(args))
	for _, arg := range args {
		pub, err := keyfmt.SignerKey.Decode(arg)
		if err != nil {
			return nil, fmt.Errorf("signer %q: %w\n  read it with `argus lock status` on that node", arg, err)
		}
		out = append(out, pub)
	}
	return out, nil
}

// requireOwnSignerKey refuses an init whose signer list omits the local node's own
// key. The node enforces this too; doing it here as well means the operator is told
// which key is missing, and the exact command to run, before anything is created.
func requireOwnSignerKey(ctx context.Context, cfg *config.Config, sigPubs [][]byte, args []string) error {
	st, err := lockStatusOnNode(ctx, cfg)
	if err != nil {
		return fmt.Errorf("reading this node's signer key: %w", err)
	}
	if len(st.SignerPubKey) == 0 {
		return fmt.Errorf("this node has no signer key")
	}
	own := keyfmt.SignerKey.Encode(st.SignerPubKey)
	for _, p := range sigPubs {
		if bytes.Equal(p, st.SignerPubKey) {
			return nil
		}
	}
	return fmt.Errorf("this node's own signer key must be listed explicitly:\n  argus lock init %s\n\nthe signer keys you pass are the complete set the new trust log will trust",
		strings.Join(append([]string{own}, args...), " "))
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
	var genDisablements int
	cmd := &cobra.Command{
		Use:   "init sigpub:<hex> [sigpub:<hex>...]",
		Short: "Enable locked mode: create the trust log with exactly these signer keys",
		Long: "Enable locked mode. The keys given are the complete set of signers the new\n" +
			"trust log will trust — including this node's own key, which must be listed.\n" +
			"Read each key with `argus lock status` on the node that holds it.",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			if cfg.Gateway.URL == "" {
				return fail(cmd, fmt.Errorf("lock init needs a gateway (set gateway.url) to read the node roster"))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigPubs, err := parseSignerKeys(args)
			if err != nil {
				return fail(cmd, err)
			}
			if err := requireOwnSignerKey(ctx, cfg, sigPubs, args); err != nil {
				return fail(cmd, err)
			}

			// Roster from the gateway (devices to authorize).
			roster, err := fetchRoster(ctx, cfg)
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
			genesis := keyfmt.Genesis.Encode(res.Tip)
			shell.StdOutF("locked mode enabled\n  genesis: %s\n  signers: %d\n", genesis, res.SignerCount)
			for _, s := range res.DisablementSecrets {
				shell.StdOutF("  disablement secret: %s\n", keyfmt.Disablement.Encode(s))
			}
			if len(res.DisablementSecrets) > 0 {
				shell.StdErrF("\nSAVE the disablement secret(s) above NOW — shown only once. Each one disables\nlocked mode network-wide (break-glass recovery if signer keys are lost).\n")
			}
			if res.SignerCount < 2 && len(res.DisablementSecrets) == 0 {
				shell.StdErrF("\nWARNING: only one signer and no disablement secrets — if this node is lost\nor compromised there is NO recovery. Add a second signer key\nto the init command, or generate a disablement secret (--gen-disablements).\n")
			} else if res.SignerCount < 2 {
				shell.StdErrF("\nNote: only one signer. If it is lost, use a saved disablement secret to recover.\nConsider re-initialising with a second signer key listed.\n")
			}
			if w := lockInitFewSignersWarning(res.SignerCount); w != "" {
				shell.StdErrF("%s", w)
			}
			pinClientRole(cfg, res.Tip)
			shell.StdOutF("\nTo pin your other devices, run on each of them:\n  argus lock pin\n(or set lock.genesis: %s in their config)\n", genesis)
			return nil
		},
	}
	cmd.Flags().IntVar(&genDisablements, "gen-disablements", 1, "number of disablement (recovery) secrets to generate")
	addClientFlags(cmd.Flags())
	return cmd
}

// pinClientRole pins this machine's client (TUI) role to the genesis lock.init just
// created. The client is a separate role reading a separate file, so without this the
// dashboard on the very machine that locked the network quarantines itself on the next
// tick. It is never a hard failure: the node is locked either way, and the operator
// gets the exact command to finish the job.
func pinClientRole(cfg *config.Config, genesis []byte) {
	if err := guardPin(cfg, genesis); err != nil {
		shell.StdErrF("\nNOTE: this machine's client (TUI) role was NOT pinned: %v\n", err)
		return
	}
	if err := clientPinFile().Save(genesis); err != nil {
		shell.StdErrF("\nNOTE: this machine's client (TUI) role was NOT pinned: %v\n  run here: argus lock pin %s\n", err, keyfmt.Genesis.Encode(genesis))
		return
	}
	shell.StdOutF("  this machine's client (TUI) role pinned to the same genesis\n")
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
				pub := keyfmt.DeviceKey.Encode(kp.Public)
				shell.StdOutF("locked mode: (client — no local node)\n  this device identity: %s\n  to authorize, run on a signer node:\n    %s\n", pub, lockSignHint(kp.Public))
				printClientPinStatus(ctx, cfg)
				return nil
			}
			printLockStatus(st)
			printClientPinStatus(ctx, cfg)
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

// gatewayProbeTimeout bounds the trust-log probe `lock status` makes. A gateway that
// completes the WebSocket upgrade and then answers nothing would otherwise hang the
// one command an operator runs when the gateway is the broken thing.
var gatewayProbeTimeout = 5 * time.Second

func printClientPinStatus(ctx context.Context, cfg *config.Config) {
	shell.StdOutF("%s", clientPinLine(ctx, cfg))
}

// clientPinLine reports the client (TUI) role's pin state on this machine. The client
// has no RPC surface and the node's status says nothing about it, so this is the only
// place an operator can see that the dashboard on this box is quarantined. With no pin
// of its own it asks the gateway whether the network has a trust log at all — that is
// precisely the condition that quarantines the client. A probe that times out is
// reported in the line, never returned: the node half of `lock status` must still print.
func clientPinLine(ctx context.Context, cfg *config.Config) string {
	pin, perr := trustpin.Resolve(cfg.Lock.Genesis, clientPinFile())
	var netGenesis []byte
	var neterr error
	if perr == nil && pin.Genesis == nil && cfg.Gateway.URL != "" {
		pctx, cancel := context.WithTimeout(ctx, gatewayProbeTimeout)
		defer cancel()
		netGenesis, neterr = quarantiningGenesis(pctx, cfg)
		if errors.Is(neterr, context.DeadlineExceeded) {
			neterr = fmt.Errorf("the gateway did not answer within %s", gatewayProbeTimeout)
		}
	}
	return clientPinStatus(pin, perr, netGenesis, neterr)
}

// clientPinStatus renders the client pin line. netGenesis is the genesis this
// network is offering (nil when there is none or it was not checked), neterr why the
// check failed.
func clientPinStatus(pin trustpin.Pin, perr error, netGenesis []byte, neterr error) string {
	switch {
	case perr != nil:
		return fmt.Sprintf("  client pin: UNUSABLE — %v\n       argus refuses to start until this is resolved\n", perr)
	case pin.Genesis != nil:
		return fmt.Sprintf("  client pin: %s (source: %s)\n", fingerprintOf(pin.Genesis), pin.Source)
	case netGenesis != nil:
		return fmt.Sprintf("  client pin: none — QUARANTINED (chain seen: %s)\n       the dashboard on this machine opens no channels; run: argus lock pin\n", fingerprintOf(netGenesis))
	case neterr != nil:
		return fmt.Sprintf("  client pin: none (could not check this network for a trust log: %v)\n", neterr)
	default:
		return "  client pin: none\n"
	}
}

// lockSignHint returns the "argus lock sign <pubkey>" instruction string for
// the given raw Ed25519 public key. Used in enrollment / authorization hints.
func lockSignHint(pub []byte) string {
	return "argus lock sign " + keyfmt.DeviceKey.Encode(pub)
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
			shell.StdOutF("  signer: %s\n", keyfmt.SignerKey.Encode(s))
		}
	case "add-signer":
		shell.StdOutF("[%d] add-signer: %s\n", e.Index, keyfmt.SignerKey.Encode(e.Target))
	case "remove-signer":
		shell.StdOutF("[%d] remove-signer: %s\n", e.Index, keyfmt.SignerKey.Encode(e.Target))
	case "authorize-device":
		shell.StdOutF("[%d] authorize-device: %s\n", e.Index, keyfmt.DeviceKey.Encode(e.Target))
	case "revoke-device":
		shell.StdOutF("[%d] revoke-device: %s\n", e.Index, keyfmt.DeviceKey.Encode(e.Target))
	case "revoke-signer":
		shell.StdOutF("[%d] revoke-signer: %d revoked, %d co-sign(s)\n", e.Index, len(e.Revoked), e.CoSignCount)
		for _, r := range e.Revoked {
			shell.StdOutF("  revoked: %s\n", keyfmt.SignerKey.Encode(r))
		}
		for _, r := range e.Replaces {
			shell.StdOutF("  replaces: %s\n", keyfmt.SignerKey.Encode(r))
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
// label or id resolves to its IdentityPubKey; otherwise the arg is parsed as a
// devpub: key.
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
	if !keyfmt.Tagged(arg) {
		return nil, fmt.Errorf("device %q is neither a known node (label/id) nor a %s key", arg, keyfmt.DeviceKey.Prefix())
	}
	pub, err := keyfmt.DeviceKey.Decode(arg)
	if err != nil {
		return nil, fmt.Errorf("device %q: %w", arg, err)
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
		Short:         short + " (node label/id or sigpub: key)",
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
			shell.StdOutF("%s ok\n  current tip (audit): %s\n", use, keyfmt.Tip.Encode(res.Tip))
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
		Short:         short + " (node label/id or devpub: key)",
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
			shell.StdOutF("%s ok\n  current tip (audit): %s\n", use, keyfmt.Tip.Encode(res.Tip))
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

// resolveSignerArgs resolves signer arguments for the post-init signer commands to
// 32-byte Ed25519 pubkeys: a sigpub: key is used as given, a node label or id is
// looked up in the gateway's roster.
//
// Unlike `lock init --signer`, a name is still accepted here. The roster mapping is
// gateway-supplied either way, so naming a signer to add carries the same
// substitution risk as it did at init — pass a sigpub: key when that matters.
func resolveSignerArgs(roster []api.NodeDescriptor, args []string) ([][]byte, error) {
	out := make([][]byte, 0, len(args))
	for _, arg := range args {
		if keyfmt.Tagged(arg) {
			pub, err := keyfmt.SignerKey.Decode(arg)
			if err != nil {
				return nil, fmt.Errorf("resolve signer %q: %w", arg, err)
			}
			out = append(out, pub)
			continue
		}
		nd := findNode(roster, arg)
		if nd == nil {
			return nil, fmt.Errorf("resolve signer %q: not a known node and not a %s key", arg, keyfmt.SignerKey.Prefix())
		}
		if nd.SignerPubKey == "" {
			return nil, fmt.Errorf("resolve signer %q: node advertises no signer key", arg)
		}
		pub, err := base64.StdEncoding.DecodeString(nd.SignerPubKey)
		if err != nil {
			return nil, fmt.Errorf("resolve signer %q: %w", arg, err)
		}
		out = append(out, pub)
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
				shell.StdOutF("revocation applied\n  new tip (audit): %s\n", keyfmt.Tip.Encode(res.Tip))
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
				forkFromBytes, err = keyfmt.DecodeAny(forkFrom, keyfmt.Tip, keyfmt.Genesis)
				if err != nil {
					return fail(cmd, fmt.Errorf("--fork-from: %w", err))
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
	cmd.Flags().StringArrayVar(&replacements, "replacement", nil, "replacement signer node (label/id or sigpub: key); repeatable")
	cmd.Flags().StringVar(&forkFrom, "fork-from", "", "override the fork point (tip: or gen:); default: parent of revoked signer's earliest entry")
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
			secret, err := keyfmt.Disablement.Decode(args[0])
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			res, err := callLocal[api.LockDisableResult](ctx, cfg, api.MethodLockDisable, api.LockDisableParams{Secret: secret})
			if err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("locked mode disabled network-wide\n  current tip (audit): %s\n", keyfmt.Tip.Encode(res.Tip))
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

func keyOrNone(k keyfmt.Kind, b []byte) string {
	if len(b) == 0 {
		return "(none)"
	}
	return k.Encode(b)
}

func printLockStatus(st api.LockStatusResult) {
	if !st.Enabled {
		shell.StdOutF("locked mode: disabled\n  this node signer: %s\n  this node identity: %s\n",
			keyOrNone(keyfmt.SignerKey, st.SignerPubKey),
			keyOrNone(keyfmt.DeviceKey, st.IdentityPubKey))
		switch {
		case st.Quarantined:
			shell.StdOutF("  pin: none — QUARANTINED (chain seen: %s)\n       run: argus lock pin\n",
				strings.Join(trustlog.HashFingerprint(st.PinGenesis), " "))
		case st.Pinned:
			shell.StdOutF("  pin: %s (source: %s)\n",
				strings.Join(trustlog.HashFingerprint(st.PinGenesis), " "), st.PinSource)
		default:
			shell.StdOutF("  pin: none\n")
		}
		if st.LocalDisabled {
			shell.StdOutF("  local-disable: active\n")
		}
		return
	}
	shell.StdOutF("locked mode: enabled\n  current tip (audit): %s\n  signers: %d\n  devices: %d\n  this node is signer: %v\n  this node authorized: %v\n",
		keyfmt.Tip.Encode(st.Tip), len(st.Signers), st.DeviceCount, st.SignerTrusted, st.Authorized)
	switch {
	case st.Quarantined:
		shell.StdOutF("  pin: none — QUARANTINED (chain seen: %s)\n       run: argus lock pin\n",
			strings.Join(trustlog.HashFingerprint(st.PinGenesis), " "))
	case st.Pinned:
		shell.StdOutF("  pin: %s (source: %s)\n",
			strings.Join(trustlog.HashFingerprint(st.PinGenesis), " "), st.PinSource)
	default:
		shell.StdOutF("  pin: none\n")
	}
	if len(st.Signers) > 0 {
		shell.StdOutF("  trust fingerprint: %s\n", strings.Join(trustlog.SignerSetFingerprint(st.Signers), " "))
		for _, s := range st.Signers {
			shell.StdOutF("    signer: %s\n", keyfmt.SignerKey.Encode(s))
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
