package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
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
	cmd.AddCommand(newLockStatusCmd(), newLockLogCmd(), newLockPinCmd(), newLockUnpinCmd(), newLockLocalDisableCmd())
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
