package main

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/shell"
)

func newLockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Manage locked mode (network trust log)",
	}
	cmd.AddCommand(newLockInitCmd(), newLockStatusCmd(), newLockSignCmd(), newLockRevokeCmd(), newLockDisableCmd(), newLockLocalDisableCmd())
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
			head := base64.StdEncoding.EncodeToString(res.Head)
			shell.StdOutF("locked mode enabled\n  genesis: %s\n  signers: %d\n", head, res.SignerCount)
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
			shell.StdOutF("\nTo pin your other nodes and clients, set in their config:\n  lock.genesis: %s\n", head)
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
		Short:         "Show locked-mode status (HEAD fingerprint, signers, this node's roles)",
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

// lockInitOnNode dials the LOCAL node socket and calls lock.init.
func lockInitOnNode(ctx context.Context, cfg *config.Config, p api.LockInitParams) (api.LockInitResult, error) {
	dial, err := gatewayDialer("", "", cfg.Socket) // force local socket
	if err != nil {
		return api.LockInitResult{}, err
	}
	conn, err := dial(ctx)
	if err != nil {
		return api.LockInitResult{}, err
	}
	c := api.NewClient(conn)
	defer c.Close()
	var res api.LockInitResult
	if err := c.Call(api.MethodLockInit, p, &res); err != nil {
		return api.LockInitResult{}, err
	}
	return res, nil
}

func lockStatusOnNode(ctx context.Context, cfg *config.Config) (api.LockStatusResult, error) {
	dial, err := gatewayDialer("", "", cfg.Socket)
	if err != nil {
		return api.LockStatusResult{}, err
	}
	conn, err := dial(ctx)
	if err != nil {
		return api.LockStatusResult{}, err
	}
	c := api.NewClient(conn)
	defer c.Close()
	var st api.LockStatusResult
	if err := c.Call(api.MethodLockStatus, nil, &st); err != nil {
		return api.LockStatusResult{}, err
	}
	return st, nil
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
	return newLockDeviceCmd("revoke", "Revoke a device", api.MethodLockRevoke)
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
			shell.StdOutF("%s ok\n  current HEAD (audit): %s\n", use, base64.StdEncoding.EncodeToString(res.Head))
			return nil
		},
	}
	addClientFlags(cmd.Flags())
	return cmd
}

// lockDeviceOnNode dials the LOCAL node socket and calls the sign/revoke method.
func lockDeviceOnNode(ctx context.Context, cfg *config.Config, method string, device []byte) (api.LockDeviceResult, error) {
	dial, err := gatewayDialer("", "", cfg.Socket) // force local socket
	if err != nil {
		return api.LockDeviceResult{}, err
	}
	conn, err := dial(ctx)
	if err != nil {
		return api.LockDeviceResult{}, err
	}
	c := api.NewClient(conn)
	defer c.Close()
	var res api.LockDeviceResult
	if err := c.Call(method, api.LockDeviceParams{Device: device}, &res); err != nil {
		return api.LockDeviceResult{}, err
	}
	return res, nil
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
			dial, err := gatewayDialer("", "", cfg.Socket) // local node
			if err != nil {
				return fail(cmd, err)
			}
			conn, err := dial(ctx)
			if err != nil {
				return fail(cmd, err)
			}
			c := api.NewClient(conn)
			defer c.Close()
			var res api.LockDisableResult
			if err := c.Call(api.MethodLockDisable, api.LockDisableParams{Secret: secret}, &res); err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("locked mode disabled network-wide\n  current HEAD (audit): %s\n", base64.StdEncoding.EncodeToString(res.Head))
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
			dial, err := gatewayDialer("", "", cfg.Socket) // local node
			if err != nil {
				return fail(cmd, err)
			}
			conn, err := dial(ctx)
			if err != nil {
				return fail(cmd, err)
			}
			c := api.NewClient(conn)
			defer c.Close()
			if err := c.Call(api.MethodLockLocalDisable, nil, nil); err != nil {
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
	shell.StdOutF("locked mode: enabled\n  current HEAD (audit): %s\n  signers: %d\n  devices: %d\n  this node is signer: %v\n  this node authorized: %v\n",
		base64.StdEncoding.EncodeToString(st.Head), len(st.Signers), st.DeviceCount, st.SignerTrusted, st.Authorized)
	if st.Disabled {
		shell.StdOutF("  network-wide disabled: true\n")
	}
	if st.LocalDisabled {
		shell.StdOutF("  local-disable: active\n")
	}
}
