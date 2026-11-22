package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/invite"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

func newInviteNode(t *testing.T) (*Node, *invite.Store) {
	t.Helper()
	st, err := invite.OpenStore(filepath.Join(t.TempDir(), "invites.json"), time.Now)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	d, sk := newSignerLockNode(t)
	initLockNode(t, d, sk)
	d.SetInviteStore(st)
	return d, st
}

func inviteCreate(d *Node, ttl int64) (any, error) {
	raw, _ := json.Marshal(api.LockInviteCreateParams{TTLSeconds: ttl})
	return d.handleLockInviteCreate(context.Background(), raw)
}

// wantRPCError fails unless err is an RPC error with code and, when msg is
// non-empty, exactly that message.
func wantRPCError(t *testing.T, err error, code int, msg string) *api.RPCError {
	t.Helper()
	var rpcErr *api.RPCError
	if !asRPCError(err, &rpcErr) || rpcErr.Code != code || (msg != "" && rpcErr.Message != msg) {
		t.Fatalf("want RPC error %d %q, got %v", code, msg, err)
	}
	return rpcErr
}

func TestInviteCreateRequiresSigner(t *testing.T) {
	st, _ := invite.OpenStore(filepath.Join(t.TempDir(), "invites.json"), time.Now)

	d, _ := newLockNode(t)
	d.SetInviteStore(st)
	_, err := inviteCreate(d, 60)
	wantRPCError(t, err, api.CodeInvalidRequest, api.ErrMsgLockedModeOff)

	other, _ := trustlog.GenerateSigner()
	otherLog, _ := trustlog.NewGenesis([][]byte{other.Public}, other, nil)
	d, _ = newLockNode(t)
	d.SetInviteStore(st)
	myKey, _ := trustlog.GenerateSigner()
	d.SetSignerKey(myKey)
	if err := d.EnableTrustLog(otherLog.Tip(), d.trustPath); err != nil {
		t.Fatalf("EnableTrustLog: %v", err)
	}
	d.syncTrustOnce(&fakePeer{pullChain: trustlog.MarshalChain(otherLog.Entries())})
	_, err = inviteCreate(d, 60)
	wantRPCError(t, err, api.CodeInvalidRequest, api.ErrMsgNotSigner)
}

func TestInviteUnavailableWithoutStore(t *testing.T) {
	d, sk := newSignerLockNode(t)
	initLockNode(t, d, sk)
	_, err := inviteCreate(d, 60)
	wantRPCError(t, err, api.CodeInvalidRequest, api.ErrMsgInvitesOff)
	_, err = d.handleLockInviteList(context.Background(), nil)
	wantRPCError(t, err, api.CodeInvalidRequest, api.ErrMsgInvitesOff)
}

func TestInviteCreateReturnsBlobFields(t *testing.T) {
	d, _ := newInviteNode(t)
	d.id = "node-1"
	d.SetIdentityKey(e2e.KeyPair{Public: make([]byte, 32), Private: make([]byte, 32)})

	before := time.Now()
	res, err := inviteCreate(d, 3600)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	r := res.(api.LockInviteCreateResult)
	if len(r.InviteID) != 16 || len(r.Secret) != 32 {
		t.Fatalf("id/secret lengths: %d/%d", len(r.InviteID), len(r.Secret))
	}
	if r.NodeID != d.id || string(r.IdentityPubKey) != string(d.identity.Public) {
		t.Errorf("node id/identity mismatch")
	}
	if string(r.Genesis) != string(d.pinGenesis) || len(r.Genesis) == 0 {
		t.Errorf("genesis mismatch")
	}
	if got := r.ExpiresAt.Sub(before); got < time.Hour || got > time.Hour+time.Minute {
		t.Errorf("ExpiresAt offset = %v", got)
	}
}

func TestInviteCreateNotPinned(t *testing.T) {
	d, _ := newInviteNode(t)
	d.pinMu.Lock()
	d.pinGenesis = nil
	d.pinMu.Unlock()
	_, err := inviteCreate(d, 60)
	wantRPCError(t, err, api.CodeInvalidRequest, "this node is not pinned; run argus lock pin")
}

func TestInviteListOmitsSecret(t *testing.T) {
	d, _ := newInviteNode(t)
	res, err := inviteCreate(d, 3600)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	secret := res.(api.LockInviteCreateResult).Secret

	lst, err := d.handleLockInviteList(context.Background(), nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	out, _ := json.Marshal(lst)
	if strings.Contains(string(out), base64.StdEncoding.EncodeToString(secret)) {
		t.Fatal("list output contains the secret")
	}
	invs := lst.(api.LockInviteListResult).Invites
	if len(invs) != 1 || invs[0].State != "pending" {
		t.Fatalf("invites = %+v", invs)
	}
}

func TestInviteListExpired(t *testing.T) {
	d, _ := newInviteNode(t)
	// The store's clock, not the wall clock, decides expiry.
	now := time.Now().Add(-time.Hour)
	st, err := invite.OpenStore(filepath.Join(t.TempDir(), "invites.json"), func() time.Time { return now })
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	d.SetInviteStore(st)
	if _, err := inviteCreate(d, 60); err != nil {
		t.Fatalf("create: %v", err)
	}
	state := func() string {
		lst, _ := d.handleLockInviteList(context.Background(), nil)
		invs := lst.(api.LockInviteListResult).Invites
		if len(invs) != 1 {
			t.Fatalf("invites = %+v", invs)
		}
		return invs[0].State
	}
	if got := state(); got != "pending" {
		t.Fatalf("state = %q, want pending", got)
	}
	now = now.Add(time.Minute)
	if got := state(); got != "expired" {
		t.Fatalf("state after ttl = %q, want expired", got)
	}
}

func TestInviteCreateTTLBounds(t *testing.T) {
	d, _ := newInviteNode(t)
	for _, secs := range []int64{0, -1, int64(invite.MaxTTL/time.Second) + 1, math.MaxInt64} {
		if _, err := inviteCreate(d, secs); err == nil {
			t.Errorf("create with ttl_seconds %d succeeded", secs)
		}
	}
	if _, err := inviteCreate(d, int64(invite.MaxTTL/time.Second)); err != nil {
		t.Fatalf("create with max ttl: %v", err)
	}
}

func TestInviteRevoke(t *testing.T) {
	d, _ := newInviteNode(t)
	res, _ := inviteCreate(d, 3600)
	id := res.(api.LockInviteCreateResult).InviteID

	raw, _ := json.Marshal(api.LockInviteRevokeParams{ID: id})
	if _, err := d.handleLockInviteRevoke(context.Background(), raw); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	lst, _ := d.handleLockInviteList(context.Background(), nil)
	if invs := lst.(api.LockInviteListResult).Invites; len(invs) != 1 || invs[0].State != "revoked" {
		t.Fatalf("invites = %+v", invs)
	}

	raw, _ = json.Marshal(api.LockInviteRevokeParams{ID: make([]byte, 16)})
	_, err := d.handleLockInviteRevoke(context.Background(), raw)
	wantRPCError(t, err, api.CodeInvalidRequest, invite.ErrUnknown.Error())
}
