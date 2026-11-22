package e2etest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/invite"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

func TestLockInviteEndToEnd(t *testing.T) {
	ts := startGateway(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signerNodeKP, nodeKP, clientKP := mustCrossKey(t), mustCrossKey(t), mustCrossKey(t)
	signerKey, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}

	// dir is short enough for macOS's 104-byte sockaddr_un limit.
	dir := t.TempDir()
	state := t.TempDir()
	invites, err := invite.OpenStore(filepath.Join(state, "invites"), time.Now)
	if err != nil {
		t.Fatalf("invite store: %v", err)
	}

	sn := node.New()
	sn.SetIdentity("li-signer", "Lock Invite Signer")
	sn.SetVersion("itest")
	sn.SetIdentityKey(signerNodeKP)
	sn.SetE2EE(true)
	sn.SetSignerKey(signerKey)
	sn.SetTrustChainPath(filepath.Join(state, "signer-chain"))
	sn.SetInviteStore(invites)
	go sn.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	socketPath := filepath.Join(dir, "li.sock")
	sockCtx, sockCancel := context.WithCancel(ctx)
	sockDone := make(chan error, 1)
	go func() { sockDone <- sn.Run(sockCtx, socketPath) }()
	t.Cleanup(func() {
		sockCancel()
		<-sockDone
	})
	waitFor(t, "li.sock ready", func() bool {
		_, err := os.Stat(socketPath)
		return err == nil
	})
	waitRostered(t, ctx, ts.URL, "li-signer")

	lc, err := api.Dial(socketPath)
	if err != nil {
		t.Fatalf("dial socket: %v", err)
	}
	defer lc.Close()

	var initRes api.LockInitResult
	if err := lc.Call(api.MethodLockInit, api.LockInitParams{
		Signers:         [][]byte{signerKey.Public},
		Devices:         [][]byte{signerNodeKP.Public},
		GenDisablements: 1,
	}, &initRes); err != nil {
		t.Fatalf("lock.init: %v", err)
	}

	createInvite := func(t *testing.T) invite.Blob {
		t.Helper()
		var res api.LockInviteCreateResult
		if err := lc.Call(api.MethodLockInviteCreate, api.LockInviteCreateParams{TTLSeconds: 3600}, &res); err != nil {
			t.Fatalf("lock.inviteCreate: %v", err)
		}
		s, err := invite.Blob{
			GatewayURL: wsURL(ts.URL, "/client"),
			Genesis:    res.Genesis,
			IssuerID:   res.NodeID,
			IssuerKey:  res.IdentityPubKey,
			InviteID:   res.InviteID,
			Secret:     res.Secret,
		}.Encode()
		if err != nil {
			t.Fatalf("encode blob: %v", err)
		}
		b, err := invite.DecodeBlob(s)
		if err != nil {
			t.Fatalf("decode blob: %v", err)
		}
		return b
	}
	redeem := func(static e2e.KeyPair, b invite.Blob, extra [][]byte) (api.LockInviteRedeemResult, error) {
		conn, err := api.DialWSConn(ctx, b.GatewayURL, b.Token, nil)
		if err != nil {
			t.Fatalf("dial gateway: %v", err)
		}
		return client.RedeemInvite(ctx, conn, static, b, extra)
	}
	wantRPCError := func(t *testing.T, err error, msg string) {
		t.Helper()
		var rpcErr *api.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Message != msg {
			t.Fatalf("err = %v, want RPC error %q", err, msg)
		}
	}

	blob := createInvite(t)
	if !bytes.Equal(blob.Genesis, initRes.Tip) {
		t.Fatalf("blob genesis = %x, want lock.init tip %x", blob.Genesis, initRes.Tip)
	}

	res, err := redeem(nodeKP, blob, [][]byte{clientKP.Public})
	if err != nil {
		t.Fatalf("RedeemInvite: %v", err)
	}
	trust := sn.TrustStore()
	if !trust.DeviceAuthorized(nodeKP.Public) || !trust.DeviceAuthorized(clientKP.Public) {
		t.Fatal("signer trust log must authorize both the node and the client key")
	}
	if !bytes.Equal(res.Tip, trust.Tip()) {
		t.Fatalf("redeem tip = %x, want signer tip %x", res.Tip, trust.Tip())
	}

	t.Run("accept wait sees node authorized", func(t *testing.T) {
		dial := func(ctx context.Context) (net.Conn, error) {
			return api.DialWSConn(ctx, blob.GatewayURL, blob.Token, nil)
		}
		chain := filepath.Join(t.TempDir(), "client-chain")
		if err := client.WaitDeviceAuthorized(ctx, dial, clientKP, blob.Genesis, chain, nodeKP.Public, 30*time.Second); err != nil {
			t.Fatalf("WaitDeviceAuthorized: %v", err)
		}
	})

	t.Run("fresh node reaches signer", func(t *testing.T) {
		// Earlier subtests' channels close asynchronously; let them drain so the
		// baseline cannot drop while the fresh node's channel opens.
		waitFor(t, "signer's earlier channels close", func() bool { return sn.RelayChannelCountForTest() == 0 })
		baseline := sn.RelayChannelCountForTest()
		fn := node.New()
		fn.SetIdentity("li-fresh", "Lock Invite Fresh")
		fn.SetVersion("itest")
		fn.SetIdentityKey(nodeKP)
		fn.SetE2EE(true)
		// Pinned to the invite's genesis with no chain yet, as after accept.
		if err := fn.EnableTrustLog(blob.Genesis, filepath.Join(t.TempDir(), "chain")); err != nil {
			t.Fatalf("EnableTrustLog: %v", err)
		}
		fctx, fcancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			fn.ConnectGateway(fctx, wsURL(ts.URL, "/node"), "", nil)
		}()
		t.Cleanup(func() { fcancel(); <-done })
		waitRostered(t, ctx, ts.URL, "li-fresh")
		waitFor(t, "fresh node rosters the signer", func() bool {
			nd, ok := fn.RosterNodeForTest("li-signer")
			return ok && nd.Online
		})

		raw, err := json.Marshal(api.OrchListParams{All: true})
		if err != nil {
			t.Fatal(err)
		}
		// orch.session.list --all opens a peer channel to every online peer.
		var unreachable []string
		waitFor(t, "fresh node opens a peer channel to the signer", func() bool {
			lctx, lcancel := context.WithTimeout(ctx, 5*time.Second)
			defer lcancel()
			out, err := fn.LocalDispatchForTest()(lctx, api.MethodOrchSessionList, raw)
			if err != nil {
				return false
			}
			unreachable = out.(api.OrchListResult).Unreachable
			return !slices.Contains(unreachable, "li-signer")
		})
		waitFor(t, "signer holds the fresh node's peer channel", func() bool {
			return sn.RelayChannelCountForTest() > baseline
		})
	})

	t.Run("reused invite", func(t *testing.T) {
		tip := sn.TrustStore().Tip()
		_, err := redeem(nodeKP, blob, [][]byte{clientKP.Public})
		wantRPCError(t, err, invite.ErrUsed.Error())
		if !bytes.Equal(sn.TrustStore().Tip(), tip) {
			t.Fatal("signer trust tip changed on a reused invite")
		}
	})

	t.Run("revoked invite", func(t *testing.T) {
		b := createInvite(t)
		if err := lc.Call(api.MethodLockInviteRevoke, api.LockInviteRevokeParams{ID: b.InviteID}, nil); err != nil {
			t.Fatalf("lock.inviteRevoke: %v", err)
		}
		tip := sn.TrustStore().Tip()
		otherKP := mustCrossKey(t)
		_, err := redeem(otherKP, b, nil)
		wantRPCError(t, err, invite.ErrRevoked.Error())
		if !bytes.Equal(sn.TrustStore().Tip(), tip) {
			t.Fatal("signer trust tip changed on a revoked invite")
		}
		if sn.TrustStore().DeviceAuthorized(otherKP.Public) {
			t.Fatal("revoked invite authorized its key")
		}
	})
}
