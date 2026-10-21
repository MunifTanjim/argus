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
	"github.com/MunifTanjim/argus/internal/tui"
)

func newLockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Manage locked mode (network trust log)",
	}
	cmd.AddCommand(newLockInitCmd(), newLockStatusCmd(), newLockLogCmd(), newLockSignCmd(), newLockRevokeCmd(), newLockAddSignerCmd(), newLockRemoveSignerCmd(), newLockRevokeSignerCmd(), newLockDisableCmd(), newLockLocalDisableCmd(), newLockPinCmd(), newLockUnpinCmd())
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
	if err := c.CallContext(ctx, method, params, &res); err != nil {
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
				shell.StdOutF("%s", noLocalNodeText(kp.Public, checkClientPin(ctx, cfg)))
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

func probeGateway(ctx context.Context, probe func(context.Context) error) error {
	pctx, cancel := context.WithTimeout(ctx, gatewayProbeTimeout)
	defer cancel()
	err := probe(pctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("the gateway did not answer within %s", gatewayProbeTimeout)
	}
	return err
}

func noLocalNodeText(identity []byte, cp clientPinInfo) string {
	return fmt.Sprintf("locked mode: (no local node)\n\nthis tui\n  identity: %s\n", keyfmt.DeviceKey.Encode(identity)) +
		authorizeTUIHint(identity) + cp.text()
}

func authorizeTUIHint(identity []byte) string {
	return fmt.Sprintf("  → to authorize this tui, run on a signer node:\n      %s\n", lockSignHint(identity))
}

// printTUIRole prints the "this tui" block: the dashboard client's identity, whether
// that identity is authorized in the trust log, its pin line, and a sign hint when it is
// not yet authorized. The node RPC says nothing about the client role, so this is the
// only place it is reported.
func printTUIRole(ctx context.Context, cfg *config.Config, st api.LockStatusResult) error {
	var identity []byte
	if kp, err := e2e.LoadOrCreateIdentity(config.GetStatePath("client-identity.json")); err == nil {
		identity = kp.Public
	}
	cp := checkClientPin(ctx, cfg)
	shell.StdOutF("%s", tuiRoleText(st, identity, cp))
	return cp.neterr
}

func tuiRoleText(st api.LockStatusResult, identity []byte, cp clientPinInfo) string {
	if identity == nil {
		return cp.text()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nthis tui\n  identity: %s", keyfmt.DeviceKey.Encode(identity))
	sign := tuiSignStateOf(st, identity)
	if sign != tui.TUISignUnknown {
		fmt.Fprintf(&b, "   authorized: %s", yesNo(sign == tui.TUISigned))
	}
	b.WriteString("\n")
	b.WriteString(cp.text())
	if sign == tui.TUIUnsigned {
		b.WriteString(authorizeTUIHint(identity))
	}
	return b.String()
}

func tuiSignStateOf(st api.LockStatusResult, identity []byte) tui.TUISignState {
	switch {
	case identity == nil || !st.Enabled || !devicesKnown(st):
		return tui.TUISignUnknown
	case keyAuthorized(identity, st.Devices):
		return tui.TUISigned
	default:
		return tui.TUIUnsigned
	}
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

// netGenesis is the genesis this network offers (nil when none or unchecked), neterr
// why the check failed. superseded means the pinned chain was disabled network-wide,
// making the pin dead rather than protective.
type clientPinInfo struct {
	pin        trustpin.Pin
	perr       error
	netGenesis []byte
	neterr     error
	superseded bool
}

// checkClientPin is the only surface for the client (TUI) role's quarantine: it has no
// RPC and the node status says nothing about it. A non-nil neterr means the probe could
// not complete.
func checkClientPin(ctx context.Context, cfg *config.Config) clientPinInfo {
	cp := clientPinInfo{}
	cp.pin, cp.perr = trustpin.Resolve(cfg.Lock.Genesis, clientPinFile())
	// A pinned client is checked too, not just an unpinned one: when the chain it
	// names has been disabled the pin is dead, and the network genesis is what the
	// operator needs to see to understand why the dashboard went dark.
	cp.superseded = cp.perr == nil && cp.pin.Genesis != nil && clientPinSuperseded(cp.pin.Genesis)
	if cp.perr == nil && (cp.pin.Genesis == nil || cp.superseded) && cfg.Gateway.URL != "" {
		cp.neterr = probeGateway(ctx, func(pctx context.Context) (err error) {
			if cp.superseded {
				cp.netGenesis, err = supersedingGenesisFromNetwork(pctx, cfg, cp.pin.Genesis)
			} else {
				cp.netGenesis, err = quarantiningGenesis(pctx, cfg)
			}
			return err
		})
	}
	return cp
}

func (cp clientPinInfo) state() tui.TUIPinState {
	switch {
	case cp.perr != nil:
		return tui.TUIPinUnreadable
	case cp.pin.Genesis != nil && cp.superseded:
		switch {
		case cp.netGenesis != nil:
			return tui.TUIRootReplaced
		case cp.neterr != nil:
			return tui.TUIRootReplacedUnchecked
		default:
			return tui.TUIRootDisabled
		}
	case cp.pin.Genesis != nil:
		return tui.TUIPinned
	case cp.netGenesis != nil:
		return tui.TUILockedOut
	case cp.neterr != nil:
		return tui.TUICheckFailed
	default:
		return tui.TUIUnpinned
	}
}

func (cp clientPinInfo) text() string {
	switch cp.state() {
	case tui.TUIPinUnreadable:
		return fmt.Sprintf("  pin: unreadable — %v\n       argus refuses to start until this is resolved\n", cp.perr)
	case tui.TUIRootReplaced:
		return fmt.Sprintf("  pin: %s — the network now uses a new root (%s)\n       this tui opens no channels; run:\n         %s\n       then restart argus\n",
			fingerprintOf(cp.pin.Genesis), fingerprintOf(cp.netGenesis), cp.fix())
	case tui.TUIRootReplacedUnchecked:
		// A replacement root may exist but could not be checked. Advising lock init
		// here could mint a competing genesis and fork the fleet.
		return fmt.Sprintf("  pin: %s — the pinned root was replaced, but the gateway could not be reached to find the new one: %v\n       this tui opens no channels; retry once the gateway is reachable\n",
			fingerprintOf(cp.pin.Genesis), cp.neterr)
	case tui.TUIRootDisabled:
		return fmt.Sprintf("  pin: %s — the pinned root was disabled network-wide; no new root yet\n       this tui opens no channels\n       a signer must run:\n         argus lock init\n       then on each device:\n         argus lock pin\n",
			fingerprintOf(cp.pin.Genesis))
	case tui.TUIPinned:
		return fmt.Sprintf("  pin: %s (source: %s)\n", fingerprintOf(cp.pin.Genesis), cp.pin.Source)
	case tui.TUILockedOut:
		return fmt.Sprintf("  pin: none — locked out: not pinned to the network's root (%s)\n       this tui opens no channels; run: argus lock pin\n", fingerprintOf(cp.netGenesis))
	case tui.TUICheckFailed:
		return fmt.Sprintf("  pin: none (could not check this network for a trust log: %v)\n", cp.neterr)
	default:
		return "  pin: none\n"
	}
}

// authorizeHint is silent when there is no live root to authorize into: a disabled
// chain authorizes nobody, a quarantined device follows a root the network left —
// both want `argus lock pin`, which the pin line already says.
func authorizeHint(st api.LockStatusResult) string {
	if nodeStateOf(st) != tui.NodeUnsigned || len(st.IdentityPubKey) == 0 {
		return ""
	}
	return fmt.Sprintf("\n  to authorize this node, run on a signer node:\n    %s\n", lockSignHint(st.IdentityPubKey))
}

func nodeStateOf(st api.LockStatusResult) tui.NodeState {
	if !st.Enabled || st.Disabled || st.Quarantined {
		return tui.NodeNotApplicable
	}
	if !st.Authorized {
		return tui.NodeUnsigned
	}
	if st.SignerTrusted {
		return tui.NodeSigner
	}
	return tui.NodeSigned
}

func lockPinFix(st api.LockStatusResult) string {
	if len(st.SeenGenesis) == 0 {
		return ""
	}
	return "argus lock pin " + keyfmt.Genesis.Encode(st.SeenGenesis)
}

func (cp clientPinInfo) fix() string {
	return "argus lock pin " + trustpin.Encode(cp.netGenesis)
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
			shell.StdOutF("%s", lockLogText(res))
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

func lockLogText(res api.LockLogResult) string {
	var b strings.Builder
	for _, e := range res.Entries {
		lockLogEntryText(&b, e)
	}
	b.WriteString(lockLogTrailer(res))
	return b.String()
}

func lockLogEntryText(b *strings.Builder, e api.LockLogEntry) {
	switch e.Kind {
	case "genesis":
		fmt.Fprintf(b, "[%d] genesis: %d signer(s)\n", e.Index, len(e.Signers))
		for _, s := range e.Signers {
			fmt.Fprintf(b, "  signer: %s\n", keyfmt.SignerKey.Encode(s))
		}
	case "add-signer":
		fmt.Fprintf(b, "[%d] add-signer: %s\n", e.Index, keyfmt.SignerKey.Encode(e.Target))
	case "remove-signer":
		fmt.Fprintf(b, "[%d] remove-signer: %s\n", e.Index, keyfmt.SignerKey.Encode(e.Target))
	case "authorize-device":
		fmt.Fprintf(b, "[%d] authorize-device: %s\n", e.Index, keyfmt.DeviceKey.Encode(e.Target))
	case "revoke-device":
		fmt.Fprintf(b, "[%d] revoke-device: %s\n", e.Index, keyfmt.DeviceKey.Encode(e.Target))
	case "revoke-signer":
		fmt.Fprintf(b, "[%d] revoke-signer: %d revoked, %d co-sign(s)\n", e.Index, len(e.Revoked), e.CoSignCount)
		for _, r := range e.Revoked {
			fmt.Fprintf(b, "  revoked: %s\n", keyfmt.SignerKey.Encode(r))
		}
		for _, r := range e.Replaces {
			fmt.Fprintf(b, "  replaces: %s\n", keyfmt.SignerKey.Encode(r))
		}
	case "disable":
		fmt.Fprintf(b, "[%d] disable\n", e.Index)
	default:
		fmt.Fprintf(b, "[%d] %s\n", e.Index, e.Kind)
	}
	if len(e.Hash) > 0 {
		fmt.Fprintf(b, "  hash: %s\n", entryHashString(e))
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

// gatherRosterDevices also returns the ids of nodes whose identity key does not decode.
func gatherRosterDevices(roster []api.NodeDescriptor) (devices []rosterDevice, unparseable []string) {
	devices = make([]rosterDevice, 0, len(roster))
	for _, nd := range roster {
		if nd.IdentityPubKey == "" {
			continue
		}
		pub, err := base64.StdEncoding.DecodeString(nd.IdentityPubKey)
		if err != nil {
			unparseable = append(unparseable, nd.ID)
			continue
		}
		label := nd.Label
		if label == "" {
			label = nd.ID
		}
		devices = append(devices, rosterDevice{pub: pub, label: label})
	}
	return devices, unparseable
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
	if err := c.CallContext(ctx, api.MethodNodesList, nil, &r); err != nil {
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
			rosterDevices, unparseable := gatherRosterDevices(roster)
			for _, id := range unparseable {
				shell.StdErrF("WARN: node %q has an unparseable identity key; not authorizing it\n", id)
			}
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

type lockDeviceOp struct{ use, short, method, noop string }

var (
	lockSignOp   = lockDeviceOp{"sign", "Authorize a device", api.MethodLockSign, "device already authorized; nothing changed"}
	lockRevokeOp = lockDeviceOp{"revoke-device", "Revoke a device", api.MethodLockRevoke, "device not currently authorized; nothing changed"}
)

func newLockSignCmd() *cobra.Command   { return newLockDeviceCmd(lockSignOp) }
func newLockRevokeCmd() *cobra.Command { return newLockDeviceCmd(lockRevokeOp) }

func newLockDeviceCmd(op lockDeviceOp) *cobra.Command {
	cmd := &cobra.Command{
		Use:           op.use + " <device>",
		Short:         op.short + " (node label/id or devpub: key)",
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

			res, err := lockDeviceOnNode(ctx, cfg, op.method, device)
			if err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("%s\n  current tip (audit): %s\n", op.outcome(res.Changed), keyfmt.Tip.Encode(res.Tip))
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

func (op lockDeviceOp) outcome(changed bool) string {
	if !changed {
		return op.use + ": " + op.noop
	}
	return op.use + " ok"
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

func revokeSignerStartOnNode(ctx context.Context, cfg *config.Config, p api.LockRevokeSignerStartParams) (api.LockRevokeSignerBlobResult, error) {
	return callLocal[api.LockRevokeSignerBlobResult](ctx, cfg, api.MethodLockRevokeSignerStart, p)
}

func revokeSignerCosignOnNode(ctx context.Context, cfg *config.Config, blob []byte) (api.LockRevokeSignerBlobResult, error) {
	return callLocal[api.LockRevokeSignerBlobResult](ctx, cfg, api.MethodLockRevokeSignerCosign, api.LockRevokeSignerCosignParams{Blob: blob})
}

func revokeSignerFinishOnNode(ctx context.Context, cfg *config.Config, blob []byte) (api.LockRevokeSignerFinishResult, error) {
	return callLocal[api.LockRevokeSignerFinishResult](ctx, cfg, api.MethodLockRevokeSignerFinish, api.LockRevokeSignerFinishParams{Blob: blob})
}

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

			if len(args) == 0 {
				return fail(cmd, fmt.Errorf("revoke-signer: specify signer(s) to revoke, or use --cosign / --finish"))
			}
			revoked, err := parseSignerKeys(args)
			if err != nil {
				return fail(cmd, err)
			}
			if len(replacements) == 0 {
				if st, serr := lockStatusOnNode(ctx, cfg); serr == nil && st.Enabled {
					remaining := signerCountAfterRevoke(st.Signers, revoked)
					if remaining < 1 {
						return fail(cmd, fmt.Errorf(
							"revocation would remove all signers and leave the log unrecoverable\n"+
								"  use --replacement sigpub:<hex> to atomically add a successor signer, or\n"+
								"  'argus lock disable <secret>' + reinit to abandon locked mode"))
					}
					if w := lockInitFewSignersWarning(remaining); w != "" {
						shell.StdErrF("%s", w)
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
			shell.StdErrF("\nNote: all entries after the fork point are erased, not only the revoked signer's; honest entries after it must be re-created.\n")
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
func lockStateOf(st api.LockStatusResult) tui.LockState {
	switch {
	case st.Quarantined && st.Pinned:
		return tui.LockOutReplaced
	case st.Quarantined:
		return tui.LockOutUnpinned
	case st.Disabled:
		return tui.LockDisabled
	case !st.Enabled:
		return tui.LockNotEnabled
	default:
		return tui.LockEnforcing
	}
}

func lockHeadline(st api.LockStatusResult) string {
	switch lockStateOf(st) {
	case tui.LockOutReplaced:
		return "locked mode: locked out — this node opens no channels\n" +
			"  the trust root it is pinned to was disabled by break-glass (permanently), and\n" +
			"  the network has since locked again under a new root\n"
	case tui.LockOutUnpinned:
		return "locked mode: locked out — the network is locked, but this node is not pinned to it\n" +
			"  this node opens no channels until you pin it (see pin: below)\n"
	case tui.LockDisabled:
		return "locked mode: disabled network-wide — break-glass used, nothing is enforced\n" +
			"  this is permanent: the log can never be re-enabled\n" +
			"  to lock again: argus lock init (new genesis; every device repins)\n"
	case tui.LockNotEnabled:
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
	switch lockStateOf(st) {
	case tui.LockOutReplaced:
		if len(st.SeenGenesis) == 0 {
			// Gate tripped but no replacement root observed yet. Naming a new root here
			// would mislead; advise the operator to wait for a signer to reinit.
			return fmt.Sprintf("  pin: %s — the pinned root was disabled; no new root yet\n       when a signer runs argus lock init, run here:\n         argus lock pin\n",
				fingerprintOf(st.PinGenesis))
		}
		// Name the genesis explicitly: a gateway that has seen a relock retains several
		// competing roots, and bare `lock pin` refuses to pick between them.
		seen := fingerprintOf(st.SeenGenesis)
		fix := lockPinFix(st)
		return fmt.Sprintf("  pin: %s — the network now uses a new root (%s)\n       run:\n         %s\n",
			fingerprintOf(st.PinGenesis), seen, fix)
	case tui.LockOutUnpinned:
		seen := fingerprintOf(st.SeenGenesis)
		fix := lockPinFix(st)
		return fmt.Sprintf("  pin: none — not pinned to the network's root (%s)\n       run:\n         %s\n", seen, fix)
	default:
		if st.Pinned {
			return fmt.Sprintf("  pin: %s (source: %s)\n",
				fingerprintOf(st.PinGenesis), st.PinSource)
		}
		return "  pin: none\n"
	}
}
