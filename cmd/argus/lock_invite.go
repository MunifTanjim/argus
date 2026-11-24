package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/invite"
	"github.com/MunifTanjim/argus/internal/keyfmt"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

// inviteTTLSeconds converts --ttl to whole seconds, rounding up.
func inviteTTLSeconds(ttl time.Duration) (int64, error) {
	if ttl < time.Second || ttl > invite.MaxTTL {
		return 0, errors.New("--ttl must be from 1s to 720h (30 days)")
	}
	secs := int64(ttl / time.Second)
	if ttl%time.Second != 0 {
		secs++
	}
	return secs, nil
}

// acceptWaitTimeout bounds how long accept waits to see this machine authorized.
const acceptWaitTimeout = 30 * time.Second

func newLockInviteCmd() *cobra.Command {
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:           "invite",
		Short:         "Create an invite that lets a new machine join this locked network",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ttlSeconds, err := inviteTTLSeconds(ttl)
			if err != nil {
				return fail(cmd, err)
			}
			gatewayURL, _ := cmd.Flags().GetString("url")
			if gatewayURL == "" {
				gatewayURL = cfg.Gateway.URL
				if gatewayURL == "" {
					return fail(cmd, errors.New("no gateway URL configured; pass --url wss://host"))
				}
				if isLoopbackURL(gatewayURL) {
					return fail(cmd, fmt.Errorf("gateway.url %s is only reachable from this machine; pass --url with an address the new machine can reach", gatewayURL))
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			res, err := callLocal[api.LockInviteCreateResult](ctx, cfg, api.MethodLockInviteCreate, api.LockInviteCreateParams{TTLSeconds: ttlSeconds})
			if err != nil {
				return fail(cmd, err)
			}
			blob, err := invite.Blob{
				GatewayURL: gatewayURL,
				Token:      cfg.Token,
				Genesis:    res.Genesis,
				IssuerID:   res.NodeID,
				IssuerKey:  res.IdentityPubKey,
				InviteID:   res.InviteID,
				Secret:     res.Secret,
			}.Encode()
			if err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("%s", inviteCreatedText(blob, res.InviteID, res.ExpiresAt))
			return nil
		},
	}
	f := cmd.Flags()
	addSocketFlag(f)
	f.DurationVar(&ttl, "ttl", time.Hour, "how long the invite stays valid")
	f.String("url", "", "gateway URL the new machine connects to (default: this machine's gateway.url)")
	cmd.AddCommand(newLockInviteListCmd(), newLockInviteRevokeCmd(), newLockInviteAcceptCmd())
	return cmd
}

// isLoopbackURL reports whether raw names localhost or a loopback address.
func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func inviteCreatedText(blob string, id []byte, expires time.Time) string {
	return fmt.Sprintf("invite created; on the new machine run `argus lock invite accept <invite>` with:\n\n%s\n\n  id:      %s\n  expires: %s\n",
		blob, hex.EncodeToString(id), expires.Local().Format(time.RFC1123))
}

func newLockInviteListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List invites created on this node",
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
			res, err := callLocal[api.LockInviteListResult](ctx, cfg, api.MethodLockInviteList, nil)
			if err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("%s", inviteListText(res.Invites))
			return nil
		},
	}
	addSocketFlag(cmd.Flags())
	return cmd
}

func inviteListText(invs []api.LockInviteInfo) string {
	if len(invs) == 0 {
		return "no invites\n"
	}
	var b strings.Builder
	for _, inv := range invs {
		fmt.Fprintf(&b, "%s  %-7s  expires %s", hex.EncodeToString(inv.ID), inv.State, inv.ExpiresAt.Local().Format(time.RFC1123))
		if len(inv.Keys) > 0 {
			keys := make([]string, len(inv.Keys))
			for i, k := range inv.Keys {
				keys[i] = keyfmt.DeviceKey.Encode(k)
			}
			fmt.Fprintf(&b, "  authorized %s", strings.Join(keys, " "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func newLockInviteRevokeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "revoke <id>",
		Short:         "Revoke an unused invite",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := hex.DecodeString(args[0])
			if err != nil || len(id) == 0 {
				return fail(cmd, fmt.Errorf("invalid invite id %q: want the hex id shown by `argus lock invite list`", args[0]))
			}
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if _, err := callLocal[struct{}](ctx, cfg, api.MethodLockInviteRevoke, api.LockInviteRevokeParams{ID: id}); err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("revoked invite %s\n", hex.EncodeToString(id))
			return nil
		},
	}
	addSocketFlag(cmd.Flags())
	return cmd
}

func newLockInviteAcceptCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:           "accept <invite>",
		Short:         "Join this machine to a locked network with an invite",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if noConfig(cmd) {
				return fail(cmd, errors.New("accept writes the config file; drop --no-config"))
			}
			b, err := invite.DecodeBlob(args[0])
			if err != nil {
				return fail(cmd, err)
			}
			for _, w := range acceptEnvWarnings(b) {
				warn(w)
			}
			cfg, cfgPath, err := loadConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			if cfgPath == "" {
				cfgPath = config.GetConfigPath("config.yaml")
			}
			dial, err := gatewayDialer(b.GatewayURL, b.Token, "")
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			out, err := acceptInvite(ctx, acceptEnv{
				cfg:        cfg,
				configPath: cfgPath,
				redeem: func(ctx context.Context, node e2e.KeyPair, extra [][]byte) error {
					return redeemOverGateway(ctx, dial, node, b, extra)
				},
				waitAuthorized: func(ctx context.Context, clientID e2e.KeyPair, nodePub []byte) error {
					return client.WaitDeviceAuthorized(ctx, dial, clientID, b.Genesis, config.GetStatePath("client-trustlog-chain"), nodePub, acceptWaitTimeout)
				},
				progress: func(msg string) { shell.StdErrLn(msg) },
			}, b, force)
			if err != nil {
				return fail(cmd, err)
			}
			shell.StdOutF("%s", out)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite conflicting gateway.url, token and e2ee.enabled in the config")
	return cmd
}

// acceptEnvWarnings names environment variables that override a value accept
// writes to the config file.
func acceptEnvWarnings(b invite.Blob) []string {
	vars := []struct{ name, key, value string }{
		{"ARGUS_GATEWAY_URL", "gateway.url", b.GatewayURL},
		{"ARGUS_TOKEN", "token", b.Token},
	}
	var warnings []string
	for _, v := range vars {
		if v.value == "" { // accept does not write it
			continue
		}
		if env := os.Getenv(v.name); env != "" && env != v.value {
			warnings = append(warnings, fmt.Sprintf("%s is set and overrides the %s accept writes to the config file", v.name, v.key))
		}
	}
	return warnings
}

// acceptEnv is what acceptInvite needs from the outside world; tests fake the
// network steps.
type acceptEnv struct {
	cfg        *config.Config
	configPath string
	redeem     func(ctx context.Context, node e2e.KeyPair, extra [][]byte) error
	// waitAuthorized returns nil once the network's trust log authorizes nodePub.
	waitAuthorized func(ctx context.Context, clientID e2e.KeyPair, nodePub []byte) error
	// progress reports a step that may take a while; nil discards it.
	progress func(msg string)
}

func (env acceptEnv) report(msg string) {
	if env.progress != nil {
		env.progress(msg)
	}
}

// acceptInvite checks every pin before writing anything, so a conflict leaves
// the config and pins untouched.
func acceptInvite(ctx context.Context, env acceptEnv, b invite.Blob, force bool) (string, error) {
	if err := guardConfigPin(env.cfg, b.Genesis); err != nil {
		return "", err
	}
	pins := []*trustpin.File{nodePinFile(), clientPinFile()}
	for _, pf := range pins {
		if err := guardExistingPin(pf, b.Genesis); err != nil {
			return "", err
		}
	}

	values := map[string]any{"gateway.url": b.GatewayURL, "e2ee.enabled": true}
	if b.Token != "" { // a tokenless gateway: leave any existing token alone
		values["token"] = b.Token
	}
	conflicts, err := config.SetValues(env.configPath, values, force)
	if err != nil {
		return "", err
	}
	if len(conflicts) > 0 {
		return "", fmt.Errorf("%s already sets a different %s; re-run with --force to overwrite", env.configPath, strings.Join(conflicts, ", "))
	}

	for _, pf := range pins {
		if err := pf.Save(b.Genesis); err != nil {
			return "", err
		}
	}
	nodeID, err := e2e.LoadOrCreateIdentity(config.GetStatePath("node-identity.json"))
	if err != nil {
		return "", err
	}
	clientID, err := e2e.LoadOrCreateIdentity(config.GetStatePath("client-identity.json"))
	if err != nil {
		return "", err
	}

	env.report("asking the issuing node to authorize this machine...")
	redeemErr := env.redeem(ctx, nodeID, [][]byte{clientID.Public})
	if mayHaveAuthorized(redeemErr) {
		env.report(fmt.Sprintf("waiting for the network's trust log to authorize this machine (up to %s)...", acceptWaitTimeout))
		werr := env.waitAuthorized(ctx, clientID, nodeID.Public)
		switch {
		case werr == nil:
			return "this machine joined the locked network\n  run `argus start` on this machine (restart it if already running)\n", nil
		case redeemErr == nil:
			return "", fmt.Errorf("the invite was redeemed, but this machine is not authorized yet: %w", werr)
		}
	}
	return "", pinnedFailure(redeemFailure(redeemErr), env.configPath)
}

// mayHaveAuthorized reports whether redeem may have authorized this machine
// despite err: an already used invite, or a request whose answer was lost,
// still might have; the trust log tells.
func mayHaveAuthorized(err error) bool {
	if err == nil || errors.Is(err, client.ErrRedeemResponseLost) {
		return true
	}
	var rpcErr *api.RPCError
	return errors.As(err, &rpcErr) && rpcErr.Message == invite.ErrUsed.Error()
}

// redeemOverGateway dials the gateway and redeems the invite, all within
// client.InviteRedeemTimeout.
func redeemOverGateway(ctx context.Context, dial api.Dialer, node e2e.KeyPair, b invite.Blob, extra [][]byte) error {
	ctx, cancel := context.WithTimeout(ctx, client.InviteRedeemTimeout())
	defer cancel()
	conn, err := dial(ctx)
	if err != nil {
		return fmt.Errorf("gateway unreachable: %s: %w", b.GatewayURL, err)
	}
	_, err = client.RedeemInvite(ctx, conn, node, b, extra)
	return err
}

// issuerLockErrors are the issuing node's own locked-mode refusals; they say
// nothing the joiner can act on directly.
var issuerLockErrors = []string{
	api.ErrMsgLockedModeOff,
	api.ErrMsgNotSigner,
	api.ErrMsgNoSignerKey,
	api.ErrMsgInvitesOff,
}

func pinnedFailure(err error, configPath string) error {
	return fmt.Errorf("%w\n  %s now points at the invite's gateway, and this machine is pinned to its network; re-run accept with a new invite, or run `argus lock unpin` first to join a different network", err, configPath)
}

func redeemFailure(err error) error {
	if errors.Is(err, client.ErrRedeemResponseLost) {
		return fmt.Errorf("redeem invite: %w\n  the invite may have been used; re-run accept with the same invite to check", err)
	}
	if errors.Is(err, client.ErrIssuerUnreachable) {
		return fmt.Errorf("redeem invite: %w, or the invite no longer exists\n  make sure the issuing signer node is running", err)
	}
	if errors.Is(err, client.ErrInviteMismatch) {
		return fmt.Errorf("redeem invite: %w", client.ErrInviteMismatch)
	}
	var rpcErr *api.RPCError
	if errors.As(err, &rpcErr) && slices.Contains(issuerLockErrors, rpcErr.Message) {
		return fmt.Errorf("redeem invite: the issuing node can no longer authorize devices (%s); ask for an invite from a current signer node", rpcErr.Message)
	}
	return fmt.Errorf("redeem invite: %w", err)
}
