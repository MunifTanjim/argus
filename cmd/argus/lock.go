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

// ownSignerKey reads this node's signer public half over the local socket.
func ownSignerKey(ctx context.Context, cfg *config.Config) ([]byte, error) {
	st, err := lockStatusOnNode(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("reading this node's signer key: %w", err)
	}
	if len(st.SignerPubKey) == 0 {
		return nil, fmt.Errorf("this node has no signer key")
	}
	return st.SignerPubKey, nil
}

// requireOwnSignerKey refuses an init whose signer list omits the local node's own
// key. The node enforces this too; doing it here as well means the operator is told
// which key is missing, and the exact command to run, before anything is created.
func requireOwnSignerKey(own []byte, sigPubs [][]byte, args []string) error {
	for _, p := range sigPubs {
		if bytes.Equal(p, own) {
			return nil
		}
	}
	return fmt.Errorf("this node's own signer key must be listed explicitly:\n  argus lock init %s\n\nthe signer keys you pass are the complete set the new trust log will trust",
		strings.Join(append([]string{keyfmt.SignerKey.Encode(own)}, args...), " "))
}

// rosterDevice is one identity key `lock init` would authorize, with the roster
// label it came from.
type rosterDevice struct {
	pub   []byte
	label string
}

// gatherRosterDevices returns every rostered node's identity pubkey paired with the
// name it is listed under, so the preview can show what is about to be trusted.
func gatherRosterDevices(roster []api.NodeDescriptor) []rosterDevice {
	out := make([]rosterDevice, 0, len(roster))
	for _, nd := range roster {
		if nd.IdentityPubKey == "" {
			continue
		}
		pub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
		if err != nil {
			shell.StdErrF("WARN: node %q has an unparseable identity key; not authorizing it\n", nd.ID)
			continue
		}
		label := nd.Label
		if label == "" {
			label = nd.ID
		}
		out = append(out, rosterDevice{pub: pub, label: label})
	}
	return out
}

// initPreview renders what `lock init` would create. Printing this and exiting is
// the default: the signer set becomes permanent and the disablement secrets are
// shown exactly once, so the operator gets to read it before any of that is true.
// It also surfaces the device list, which is otherwise invisible — those identity
// keys come from the gateway's roster, and nothing has verified them.
func initPreview(own []byte, sigPubs [][]byte, devices []rosterDevice, genDisablements int, args []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "would create a trust log with:\n  signers (%d):\n", len(sigPubs))
	for _, p := range sigPubs {
		self := ""
		if bytes.Equal(p, own) {
			self = "  (this node)"
		}
		fmt.Fprintf(&b, "    %s%s\n", keyfmt.SignerKey.Encode(p), self)
	}
	fmt.Fprintf(&b, "  signer-set fingerprint: %s\n", signerSetFingerprintOf(sigPubs))
	fmt.Fprintf(&b, "  disablement secrets: %d (shown once, at creation)\n", genDisablements)
	fmt.Fprintf(&b, "  devices authorized from the gateway roster (%d):\n", len(devices))
	for _, d := range devices {
		fmt.Fprintf(&b, "    %s  %s\n", keyfmt.DeviceKey.Encode(d.pub), d.label)
	}
	if len(devices) > 0 {
		b.WriteString("  these identity keys come from the gateway, which nothing has verified yet\n")
	}
	fmt.Fprintf(&b, "\nnothing has been created. re-run with --confirm:\n  argus lock init --confirm %s\n", strings.Join(args, " "))
	return b.String()
}

func newLockInitCmd() *cobra.Command {
	var genDisablements int
	var confirm bool
	cmd := &cobra.Command{
		Use:   "init sigpub:<hex> [sigpub:<hex>...]",
		Short: "Enable locked mode: create the trust log with exactly these signer keys",
		Long: "Enable locked mode. The keys given are the complete set of signers the new\n" +
			"trust log will trust — including this node's own key, which must be listed.\n" +
			"Read each key with `argus lock status` on the node that holds it.\n\n" +
			"Without --confirm this prints what would be created and exits, changing nothing.",
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
			own, err := ownSignerKey(ctx, cfg)
			if err != nil {
				return fail(cmd, err)
			}
			if err := requireOwnSignerKey(own, sigPubs, args); err != nil {
				return fail(cmd, err)
			}

			// Roster from the gateway (devices to authorize).
			roster, err := fetchRoster(ctx, cfg)
			if err != nil {
				return fail(cmd, err)
			}
			rosterDevices := gatherRosterDevices(roster)
			if !confirm {
				shell.StdOutF("%s", initPreview(own, sigPubs, rosterDevices, genDisablements, args))
				return nil
			}
			devices := make([][]byte, 0, len(rosterDevices))
			for _, d := range rosterDevices {
				devices = append(devices, d.pub)
			}

			// A disabled log is a dead network that lock.init replaces with a new
			// genesis, so the pins pointing at it are stale rather than conflicting.
			prior, _ := lockStatusOnNode(ctx, cfg)
			reinit := prior.Enabled && prior.Disabled

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
			pinClientRole(cfg, res.Tip, reinit)
			if reinit {
				shell.StdOutF("\nThis replaced a disabled trust log, so every device still pinned to the old\ngenesis must be repinned, run on each of them:\n  argus lock unpin\n  argus lock pin\n(or set lock.genesis: %s in their config)\n", genesis)
				return nil
			}
			shell.StdOutF("\nTo pin your other devices, run on each of them:\n  argus lock pin\n(or set lock.genesis: %s in their config)\n", genesis)
			return nil
		},
	}
	cmd.Flags().IntVar(&genDisablements, "gen-disablements", 1, "number of disablement (recovery) secrets to generate")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "actually create the trust log; without it, print what would be created and exit")
	addClientFlags(cmd.Flags())
	return cmd
}

// pinClientRole pins this machine's client (TUI) role to the genesis lock.init just
// created. The client is a separate role reading a separate file, so without this the
// dashboard on the very machine that locked the network quarantines itself on the next
// tick. It is never a hard failure: the node is locked either way, and the operator
// gets the exact command to finish the job.
//
// replaceStale overwrites a pin left over from a disabled log, matching what the node
// does to its own pin: the genesis came from this machine's node over its local socket
// and the operator asked for it, so the file pin has no more standing than the node's.
// A config pin (lock.genesis) is still refused — that one is the operator's to edit.
func pinClientRole(cfg *config.Config, genesis []byte, replaceStale bool) {
	note := func(err error) {
		shell.StdErrF("\nNOTE: this machine's client (TUI) role was NOT pinned: %v\n", err)
	}
	cfgGenesis, err := configPin(cfg)
	if err != nil {
		note(err)
		return
	}
	if cfgGenesis != nil && !bytes.Equal(cfgGenesis, genesis) {
		note(configPinConflict(cfgGenesis))
		return
	}
	prior, err := clientPinFile().Load()
	if err != nil {
		note(err)
		return
	}
	stale := prior != nil && !bytes.Equal(prior, genesis)
	if stale && !replaceStale {
		note(existingPinConflict(prior))
		return
	}
	if err := clientPinFile().Save(genesis); err != nil {
		shell.StdErrF("\nNOTE: this machine's client (TUI) role was NOT pinned: %v\n  run here: argus lock pin %s\n", err, keyfmt.Genesis.Encode(genesis))
		return
	}
	if stale {
		shell.StdOutF("  this machine's client (TUI) role repinned from the disabled genesis %s\n", keyfmt.Genesis.Encode(prior))
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
			if hint := authorizeHint(st); hint != "" {
				shell.StdOutF("%s", hint)
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
	// A pinned client is checked too, not just an unpinned one: when the chain it
	// names has been disabled the pin is dead, and the network genesis is what the
	// operator needs to see to understand why the dashboard went dark.
	superseded := perr == nil && pin.Genesis != nil && clientPinSuperseded(pin.Genesis)
	var netGenesis []byte
	var neterr error
	if perr == nil && (pin.Genesis == nil || superseded) && cfg.Gateway.URL != "" {
		pctx, cancel := context.WithTimeout(ctx, gatewayProbeTimeout)
		defer cancel()
		if superseded {
			netGenesis, neterr = supersedingGenesisFromNetwork(pctx, cfg, pin.Genesis)
		} else {
			netGenesis, neterr = quarantiningGenesis(pctx, cfg)
		}
		if errors.Is(neterr, context.DeadlineExceeded) {
			neterr = fmt.Errorf("the gateway did not answer within %s", gatewayProbeTimeout)
		}
	}
	return clientPinStatus(pin, perr, netGenesis, neterr, superseded)
}

// clientPinStatus renders the client pin line. netGenesis is the genesis this
// network is offering (nil when there is none or it was not checked), neterr why the
// check failed. superseded reports that the chain this pin names was disabled
// network-wide, which makes the pin dead rather than protective.
func clientPinStatus(pin trustpin.Pin, perr error, netGenesis []byte, neterr error, superseded bool) string {
	switch {
	case perr != nil:
		return fmt.Sprintf("  client pin: UNUSABLE — %v\n       argus refuses to start until this is resolved\n", perr)
	case pin.Genesis != nil && superseded:
		moved, fix := "the network moved to a new trust root", "argus lock pin"
		if netGenesis != nil {
			moved = "the network now uses " + fingerprintOf(netGenesis)
			fix = "argus lock pin " + trustpin.Encode(netGenesis)
		}
		return fmt.Sprintf("  client pin: %s — SUPERSEDED: %s\n       the dashboard on this machine opens no channels; run:\n         %s\n       then restart argus\n",
			fingerprintOf(pin.Genesis), moved, fix)
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

// authorizeHint is the enrollment instruction for a node the chain does not yet
// authorize. Silent when there is no live root to be authorized into: a disabled
// chain authorizes nobody, and a quarantined device follows a root the network has
// left — in both cases the next step is `argus lock pin`, which the pin line says.
func authorizeHint(st api.LockStatusResult) string {
	if !st.Enabled || st.Authorized || st.Disabled || st.Quarantined || len(st.IdentityPubKey) == 0 {
		return ""
	}
	return fmt.Sprintf("\n  to authorize this node, run on a signer node:\n    %s\n", lockSignHint(st.IdentityPubKey))
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
				fp := signerSetFingerprintOf(res.Signers)
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
		Use:           use + " sigpub:<hex>",
		Short:         short + " (signer key; read it with `argus lock status` on that node)",
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
			pubs, err := parseSignerKeys([]string{args[0]})
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
			revoked, err := parseSignerKeys(args)
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
								"  use --replacement sigpub:<hex> to atomically add a successor signer, or\n"+
								"  'argus lock disable <secret>' + reinit to abandon locked mode"))
					}
				}
			}
			var replaces [][]byte
			if len(replacements) > 0 {
				replaces, err = parseSignerKeys(replacements)
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
	cmd.Flags().StringArrayVar(&replacements, "replacement", nil, "replacement signer key (sigpub:<hex>); repeatable")
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
	out, warn := lockStatusLines(st)
	shell.StdOutF("%s", out)
	if warn != "" {
		shell.StdErrF("%s", warn)
	}
}

// lockStatusLines renders `lock status` as (stdout, stderr). Pure, so the wording is
// testable — the same split clientPinStatus uses. The headline states enforcement:
// "disabled" is reserved for a network whose break-glass secret was spent, never for
// one that was simply never locked.
func lockStatusLines(st api.LockStatusResult) (string, string) {
	var b strings.Builder
	switch {
	// Supersession outranks every other headline: what the operator needs first is
	// that THIS device is serving nobody, not the history of the root it still holds.
	case st.Quarantined && st.Pinned:
		fmt.Fprintf(&b, "locked mode: QUARANTINED — this device refuses all channels\n"+
			"  it follows a trust root the network has left: its own log was disabled by\n"+
			"  break-glass (permanently), and the network has since locked again under a new one\n"+
			"  current tip (audit): %s\n", keyfmt.Tip.Encode(st.Tip))
	case !st.Enabled:
		fmt.Fprintf(&b, "locked mode: not enabled\n  this node signer: %s\n  this node identity: %s\n",
			keyOrNone(keyfmt.SignerKey, st.SignerPubKey), keyOrNone(keyfmt.DeviceKey, st.IdentityPubKey))
	case st.Disabled:
		fmt.Fprintf(&b, "locked mode: disabled network-wide — break-glass used, nothing is enforced\n"+
			"  this is permanent: the log can never be re-enabled\n"+
			"  to lock again: argus lock init (new genesis; every device repins)\n"+
			"  current tip (audit): %s\n", keyfmt.Tip.Encode(st.Tip))
	default:
		fmt.Fprintf(&b, "locked mode: enforcing\n  current tip (audit): %s\n  signers: %d\n  devices: %d\n  this node is signer: %v\n  this node authorized: %v\n",
			keyfmt.Tip.Encode(st.Tip), len(st.Signers), st.DeviceCount, st.SignerTrusted, st.Authorized)
	}
	b.WriteString(lockPinLines(st))
	if st.Enabled && !st.Disabled && len(st.Signers) > 0 {
		fmt.Fprintf(&b, "  trust fingerprint: %s\n", signerSetFingerprintOf(st.Signers))
		for _, s := range st.Signers {
			fmt.Fprintf(&b, "    signer: %s\n", keyfmt.SignerKey.Encode(s))
		}
	}
	if st.LocalDisabled {
		b.WriteString("  local-disable: active\n")
	}
	var warn string
	if st.Equivocation {
		warn = "\n⚠ equivocation detected: node beacons diverge — the gateway may be showing inconsistent trust-log views. Compare the tip fingerprint above across your nodes out-of-band (phone/chat) to confirm they match.\n"
	}
	return b.String(), warn
}

// lockPinLines renders the pin block. A quarantined device that still holds a pin is
// superseded: its root was disabled and the network moved on, so it names both.
func lockPinLines(st api.LockStatusResult) string {
	seen := fingerprintOf(st.SeenGenesis)
	// Name the genesis explicitly: a gateway that has seen a relock retains several
	// competing roots, and bare `lock pin` refuses to pick between them.
	fix := "argus lock pin " + keyfmt.Genesis.Encode(st.SeenGenesis)
	switch {
	case st.Quarantined && st.Pinned:
		return fmt.Sprintf("  pin: %s — SUPERSEDED: the network now uses %s\n       run:\n         %s\n",
			fingerprintOf(st.PinGenesis), seen, fix)
	case st.Quarantined:
		return fmt.Sprintf("  pin: none — QUARANTINED (chain seen: %s)\n       run:\n         %s\n", seen, fix)
	case st.Pinned:
		return fmt.Sprintf("  pin: %s (source: %s)\n",
			fingerprintOf(st.PinGenesis), st.PinSource)
	default:
		return "  pin: none\n"
	}
}
