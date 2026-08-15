package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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
	cmd.AddCommand(newLockInitCmd(), newLockStatusCmd(), newLockLogCmd(), newLockSignCmd(), newLockRevokeCmd(), newLockAddSignerCmd(), newLockRemoveSignerCmd(), newLockDisableCmd(), newLockLocalDisableCmd(), newLockPinCmd(), newLockUnpinCmd())
	return cmd
}

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

func lockStatusOnNode(ctx context.Context, cfg *config.Config) (api.LockStatusResult, error) {
	return callLocal[api.LockStatusResult](ctx, cfg, api.MethodLockStatus, nil)
}

func lockLogOnNode(ctx context.Context, cfg *config.Config) (api.LockLogResult, error) {
	return callLocal[api.LockLogResult](ctx, cfg, api.MethodLockLog, nil)
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
				kp, ierr := e2e.LoadOrCreateIdentity(config.GetStatePath("client-identity.json"))
				if ierr != nil {
					return fail(cmd, err) // surface the original node-dial error
				}
				shell.StdOutF("locked mode: (no local node)\nthis tui\n  identity: %s\n  → to authorize this tui, run on a signer node:\n      %s\n",
					keyfmt.DeviceKey.Encode(kp.Public), lockSignHint(kp.Public))
				printClientPinStatus(ctx, cfg)
				// Exit 0 only when the socket was not explicitly requested and simply
				// does not exist: that is a machine with no local node (client-only).
				// An explicit --socket flag, or any error other than ENOENT (ECONNREFUSED,
				// timeout, etc.), means the socket is broken or misconfigured.
				if !cmd.Flags().Changed("socket") && errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return fail(cmd, fmt.Errorf("node socket: %v", err))
			}
			printLockStatus(st)
			if hint := authorizeHint(st); hint != "" {
				shell.StdOutF("%s", hint)
			}
			if pinErr := printTUIRole(ctx, cfg, st); pinErr != nil {
				return fail(cmd, pinErr)
			}
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

// gatewayProbeTimeout: a gateway that upgrades then answers nothing would otherwise
// hang `lock status` — the one command run when the gateway is broken.
var gatewayProbeTimeout = 5 * time.Second

func printClientPinStatus(ctx context.Context, cfg *config.Config) error {
	line, err := clientPinLine(ctx, cfg)
	shell.StdOutF("%s", line)
	return err
}

// printTUIRole prints the "this tui" block: the dashboard client's identity, whether
// that identity is authorized in the trust log, its pin line, and a sign hint when it is
// not yet authorized. The node RPC says nothing about the client role, so this is the
// only place it is reported.
func printTUIRole(ctx context.Context, cfg *config.Config, st api.LockStatusResult) error {
	kp, err := e2e.LoadOrCreateIdentity(config.GetStatePath("client-identity.json"))
	if err != nil {
		return printClientPinStatus(ctx, cfg) // identity unreadable: still show the pin line
	}
	shell.StdOutF("this tui\n  identity: %s", keyfmt.DeviceKey.Encode(kp.Public))
	showAuthz := st.Enabled && devicesKnown(st)
	authorized := showAuthz && keyAuthorized(kp.Public, st.Devices)
	if showAuthz {
		shell.StdOutF("   authorized: %s", yesNo(authorized))
	}
	shell.StdOutF("\n")
	pinErr := printClientPinStatus(ctx, cfg)
	if showAuthz && !authorized {
		shell.StdOutF("  → to authorize this tui, run on a signer node:\n      %s\n", lockSignHint(kp.Public))
	}
	return pinErr
}

// devicesKnown reports whether st carries the authorized device set. A daemon that
// predates the Devices field returns a count with no list; membership is unknowable then.
func devicesKnown(st api.LockStatusResult) bool {
	return st.DeviceCount == 0 || len(st.Devices) > 0
}

func keyAuthorized(key []byte, devices [][]byte) bool {
	for _, d := range devices {
		if bytes.Equal(d, key) {
			return true
		}
	}
	return false
}

// clientPinLine is the only surface for the client (TUI) role's quarantine: it has no
// RPC and the node status says nothing about it. A non-nil error means the probe could
// not complete; the caller still prints the line, then exits non-zero.
func clientPinLine(ctx context.Context, cfg *config.Config) (string, error) {
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
	return clientPinStatus(pin, perr, netGenesis, neterr, superseded), neterr
}

// netGenesis is the genesis this network offers (nil when none or unchecked), neterr
// why the check failed. superseded means the pinned chain was disabled network-wide,
// making the pin dead rather than protective.
func clientPinStatus(pin trustpin.Pin, perr error, netGenesis []byte, neterr error, superseded bool) string {
	switch {
	case perr != nil:
		return fmt.Sprintf("  pin: unreadable — %v\n       argus refuses to start until this is resolved\n", perr)
	case pin.Genesis != nil && superseded:
		if netGenesis != nil {
			return fmt.Sprintf("  pin: %s — the network now uses a new root (%s)\n       this tui opens no channels; run:\n         %s\n       then restart argus\n",
				fingerprintOf(pin.Genesis), fingerprintOf(netGenesis), "argus lock pin "+trustpin.Encode(netGenesis))
		}
		if neterr != nil {
			// Probe failed: a replacement root may exist but could not be checked.
			// Advising lock init here could mint a competing genesis and fork the fleet.
			return fmt.Sprintf("  pin: %s — the pinned root was replaced, but the gateway could not be reached to find the new one: %v\n       this tui opens no channels; retry once the gateway is reachable\n",
				fingerprintOf(pin.Genesis), neterr)
		}
		return fmt.Sprintf("  pin: %s — the pinned root was disabled network-wide; no new root yet\n       this tui opens no channels\n       a signer must run:\n         argus lock init\n       then on each device:\n         argus lock pin\n",
			fingerprintOf(pin.Genesis))
	case pin.Genesis != nil:
		return fmt.Sprintf("  pin: %s (source: %s)\n", fingerprintOf(pin.Genesis), pin.Source)
	case netGenesis != nil:
		return fmt.Sprintf("  pin: none — locked out: not pinned to the network's root (%s)\n       this tui opens no channels; run: argus lock pin\n", fingerprintOf(netGenesis))
	case neterr != nil:
		return fmt.Sprintf("  pin: none (could not check this network for a trust log: %v)\n", neterr)
	default:
		return "  pin: none\n"
	}
}

// authorizeHint is silent when there is no live root to authorize into: a disabled
// chain authorizes nobody, a quarantined device follows a root the network left —
// both want `argus lock pin`, which the pin line already says.
func authorizeHint(st api.LockStatusResult) string {
	if !st.Enabled || st.Authorized || st.Disabled || st.Quarantined || len(st.IdentityPubKey) == 0 {
		return ""
	}
	return fmt.Sprintf("\n  to authorize this node, run on a signer node:\n    %s\n", lockSignHint(st.IdentityPubKey))
}

func lockSignHint(pub []byte) string {
	return "argus lock sign " + keyfmt.DeviceKey.Encode(pub)
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
			shell.StdOutF("%s", lockLogTrailer(res))
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

// entryHashString spells an entry hash the way revoke-signer --fork-from parses it:
// the genesis as gen:, every later entry as tip:.
func entryHashString(e api.LockLogEntry) string {
	if e.Index == 0 {
		return keyfmt.Genesis.Encode(e.Hash)
	}
	return keyfmt.Tip.Encode(e.Hash)
}

func lockLogTrailer(res api.LockLogResult) string {
	var b strings.Builder
	b.WriteString("\n")
	if len(res.Entries) > 0 {
		if len(res.Entries[0].Hash) > 0 {
			fmt.Fprintf(&b, "genesis: %s\n", keyfmt.Genesis.Encode(res.Entries[0].Hash))
		} else {
			b.WriteString("entry hashes not reported by the running daemon — restart it: argus start\n")
		}
	}
	if len(res.Tip) > 0 {
		fmt.Fprintf(&b, "tip:     %s\n", keyfmt.Tip.Encode(res.Tip))
	}
	fmt.Fprintf(&b, "length:  %d entries\n", len(res.Entries))
	if len(res.Signers) > 0 {
		fmt.Fprintf(&b, "signers: %d  fingerprint: %s\n", len(res.Signers), signerSetFingerprintOf(res.Signers))
	}
	return b.String()
}

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
	if len(e.Hash) > 0 {
		shell.StdOutF("  hash: %s\n", entryHashString(e))
	}
}

func findNode(roster []api.NodeDescriptor, name string) *api.NodeDescriptor {
	for i := range roster {
		if roster[i].ID == name || roster[i].Label == name {
			return &roster[i]
		}
	}
	return nil
}

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

// The node enforces the own-key rule client-side too, so the operator is told
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

type rosterDevice struct {
	pub   []byte
	label string
}

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

// Printing the preview and exiting is
// the default: the signer set becomes permanent and the disablement secrets are
// shown exactly once, so the operator gets to read it before any of that is true.
// It also surfaces the device list, which is otherwise invisible — those identity
// keys come from the gateway's roster, and nothing has verified them.
func initPreview(own []byte, sigPubs [][]byte, devices []rosterDevice, genDisablements int, rerun string) string {
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
	fmt.Fprintf(&b, "\nnothing has been created. re-run with --confirm:\n  %s\n", rerun)
	return b.String()
}

// initConfirmRerun rebuilds the invocation with --confirm, preserving every flag the
// caller set (--gen-disablements, --socket, ...) so the printed command reproduces this
// run exactly rather than silently falling back to flag defaults.
func initConfirmRerun(cmd *cobra.Command, args []string) string {
	parts := []string{"argus lock init --confirm"}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if f.Name == "confirm" {
			return
		}
		if f.Value.Type() == "bool" {
			parts = append(parts, "--"+f.Name)
			return
		}
		parts = append(parts, "--"+f.Name, f.Value.String())
	})
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

// revoke-signer co-signing requires ≥3 signers to out-vote one compromised key.
func lockInitFewSignersWarning(signerCount int) string {
	if signerCount < 3 {
		return "\nNote: fewer than 3 signers — 'lock revoke-signer' needs ≥3 signers to out-vote\none compromised key; with fewer, recovery is 'lock disable' + reinit.\n"
	}
	return ""
}

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

func lockInitOnNode(ctx context.Context, cfg *config.Config, p api.LockInitParams) (api.LockInitResult, error) {
	return callLocal[api.LockInitResult](ctx, cfg, api.MethodLockInit, p)
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

			roster, err := fetchRoster(ctx, cfg)
			if err != nil {
				return fail(cmd, err)
			}
			rosterDevices := gatherRosterDevices(roster)
			if !confirm {
				shell.StdOutF("%s", initPreview(own, sigPubs, rosterDevices, genDisablements, initConfirmRerun(cmd, args)))
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

			res, err := lockInitOnNode(ctx, cfg, api.LockInitParams{Signers: sigPubs, Devices: devices, GenDisablements: genDisablements})
			if err != nil {
				return fail(cmd, err)
			}

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
	return newLockDeviceCmd("sign", "Authorize a device", api.MethodLockSign, "device already authorized; nothing changed")
}
func newLockRevokeCmd() *cobra.Command {
	return newLockDeviceCmd("revoke-device", "Revoke a device", api.MethodLockRevoke, "device not currently authorized; nothing changed")
}

func newLockDeviceCmd(use, short, method, noopMsg string) *cobra.Command {
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
			if !res.Changed {
				shell.StdOutF("%s: %s\n  current tip (audit): %s\n", use, noopMsg, keyfmt.Tip.Encode(res.Tip))
				return nil
			}
			shell.StdOutF("%s ok\n  current tip (audit): %s\n", use, keyfmt.Tip.Encode(res.Tip))
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

func lockDeviceOnNode(ctx context.Context, cfg *config.Config, method string, device []byte) (api.LockDeviceResult, error) {
	return callLocal[api.LockDeviceResult](ctx, cfg, method, api.LockDeviceParams{Device: device})
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

// lockStatusLines is pure so the wording is testable. Sections are unconditional: a
// fact that does not exist prints as none, so absence is never ambiguous.
func lockStatusLines(st api.LockStatusResult) (string, string) {
	var b strings.Builder
	b.WriteString(lockHeadline(st))
	for _, section := range []string{
		chainSection(st),
		thisNodeSection(st),
		signersSection(st),
		devicesSection(st),
	} {
		b.WriteString("\n")
		b.WriteString(section)
	}
	b.WriteString("\n")
	b.WriteString(lockPinLines(st))
	if st.LocalDisabled {
		b.WriteString("  local-disable: active\n")
	}
	return b.String(), ""
}

// "disabled" is reserved for a network whose break-glass secret was spent, never one
// simply never locked. Supersession outranks every headline: what matters first is
// that THIS device serves nobody, not the history of the root it still holds.
func lockHeadline(st api.LockStatusResult) string {
	switch {
	case st.Quarantined && st.Pinned:
		return "locked mode: locked out — this node opens no channels\n" +
			"  the trust root it is pinned to was disabled by break-glass (permanently), and\n" +
			"  the network has since locked again under a new root\n"
	case st.Quarantined:
		return "locked mode: locked out — the network is locked, but this node is not pinned to it\n" +
			"  this node opens no channels until you pin it (see pin: below)\n"
	case st.Disabled:
		return "locked mode: disabled network-wide — break-glass used, nothing is enforced\n" +
			"  this is permanent: the log can never be re-enabled\n" +
			"  to lock again: argus lock init (new genesis; every device repins)\n"
	case !st.Enabled:
		return "locked mode: not enabled\n"
	default:
		return "locked mode: enforcing\n"
	}
}

func chainSection(st api.LockStatusResult) string {
	if !st.Enabled {
		return "chain: none — this node holds no trust log\n"
	}
	var b strings.Builder
	b.WriteString("chain\n")
	// Enabled implies pinned — the store is genesis-pinned at construction — so an
	// absent genesis here is a bug, not a state. Say so rather than print "gen:".
	if len(st.PinGenesis) > 0 {
		fmt.Fprintf(&b, "  genesis: %s\n           %s\n",
			keyfmt.Genesis.Encode(st.PinGenesis), fingerprintOf(st.PinGenesis))
	} else {
		b.WriteString("  genesis: (not reported)\n")
	}
	if len(st.Tip) == 0 {
		b.WriteString("  tip:     none — no chain synced yet\n")
	} else {
		fmt.Fprintf(&b, "  tip:     %s\n           %s\n",
			keyfmt.Tip.Encode(st.Tip), fingerprintOf(st.Tip))
	}
	if st.Length == 0 && st.DeviceCount > 0 {
		b.WriteString("  length:  not reported by the running daemon\n")
	} else {
		fmt.Fprintf(&b, "  length:  %d entries\n", st.Length)
	}
	return b.String()
}

func thisNodeSection(st api.LockStatusResult) string {
	var b strings.Builder
	b.WriteString("this node\n")
	fmt.Fprintf(&b, "  identity: %s", keyOrNone(keyfmt.DeviceKey, st.IdentityPubKey))
	if st.Enabled {
		fmt.Fprintf(&b, "   authorized: %s", yesNo(st.Authorized))
	}
	fmt.Fprintf(&b, "\n  signer:   %s", keyOrNone(keyfmt.SignerKey, st.SignerPubKey))
	if st.Enabled {
		fmt.Fprintf(&b, "   trusted: %s", yesNo(st.SignerTrusted))
	}
	b.WriteString("\n")
	return b.String()
}

func signersSection(st api.LockStatusResult) string {
	if len(st.Signers) == 0 {
		return "signers: none\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "signers (%d)\n", len(st.Signers))
	fmt.Fprintf(&b, "  fingerprint: %s\n", signerSetFingerprintOf(st.Signers))
	for _, s := range st.Signers {
		fmt.Fprintf(&b, "  %s\n", keyfmt.SignerKey.Encode(s))
	}
	return b.String()
}

func devicesSection(st api.LockStatusResult) string {
	if st.DeviceCount == 0 {
		return "devices: none\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "devices (%d)\n", st.DeviceCount)
	if len(st.Devices) == 0 {
		b.WriteString("  list not reported by the running daemon — restart it: argus start\n")
		return b.String()
	}
	for _, dev := range st.Devices {
		fmt.Fprintf(&b, "  %s", keyfmt.DeviceKey.Encode(dev))
		if bytes.Equal(dev, st.IdentityPubKey) {
			b.WriteString("  ← this node")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// A quarantined device that still holds a pin is superseded: its root was disabled and
// the network moved on, so it names both.
func lockPinLines(st api.LockStatusResult) string {
	switch {
	case st.Quarantined && st.Pinned:
		if len(st.SeenGenesis) == 0 {
			// Gate tripped but no replacement root observed yet. Naming a new root here
			// would mislead; advise the operator to wait for a signer to reinit.
			return fmt.Sprintf("  pin: %s — the pinned root was disabled; no new root yet\n       when a signer runs argus lock init, run here:\n         argus lock pin\n",
				fingerprintOf(st.PinGenesis))
		}
		// Name the genesis explicitly: a gateway that has seen a relock retains several
		// competing roots, and bare `lock pin` refuses to pick between them.
		seen := fingerprintOf(st.SeenGenesis)
		fix := "argus lock pin " + keyfmt.Genesis.Encode(st.SeenGenesis)
		return fmt.Sprintf("  pin: %s — the network now uses a new root (%s)\n       run:\n         %s\n",
			fingerprintOf(st.PinGenesis), seen, fix)
	case st.Quarantined:
		seen := fingerprintOf(st.SeenGenesis)
		fix := "argus lock pin " + keyfmt.Genesis.Encode(st.SeenGenesis)
		return fmt.Sprintf("  pin: none — not pinned to the network's root (%s)\n       run:\n         %s\n", seen, fix)
	case st.Pinned:
		return fmt.Sprintf("  pin: %s (source: %s)\n",
			fingerprintOf(st.PinGenesis), st.PinSource)
	default:
		return "  pin: none\n"
	}
}
