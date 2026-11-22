package node

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/invite"
)

// newInviteResponder returns a responder on a signer node with an invite store
// whose clock is now.
func newInviteResponder(t *testing.T, now func() time.Time) (*relayResponder, *invite.Store) {
	t.Helper()
	st, err := invite.OpenStore(filepath.Join(t.TempDir(), "invites.json"), now)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	d, sk := newSignerLockNode(t)
	d.SetIdentityKey(mustKeyPair(t))
	initLockNode(t, d, sk)
	d.SetInviteStore(st)
	return d.newRelayResponder(nil), st
}

var inviteChanSeq atomic.Int64

// inviteConn is one invite channel attempt from a fake client through a fake gateway.
type inviteConn struct {
	chanID   string
	gw       *api.Peer
	fromNode chan api.RelayFrame
	ch       *api.Channel // nil when the handshake did not complete
}

// inviteHandshake sends an IKpsk2 msg1 for inviteID and finishes the handshake
// when msg2 arrives. err is non-nil when no msg2 came back or Finish failed.
func inviteHandshake(t *testing.T, r *relayResponder, clientKP e2e.KeyPair, inviteID, psk []byte) (*inviteConn, error) {
	t.Helper()
	gw, fromNode := gatewayUplink(t, r)
	c := &inviteConn{chanID: fmt.Sprintf("inv-%d", inviteChanSeq.Add(1)), gw: gw, fromNode: fromNode}
	init, msg1, err := e2e.NewInviteInitiator(clientKP, r.static.Public, api.ChannelPrologue(r.d.id, c.chanID), psk, inviteID)
	if err != nil {
		t.Fatalf("NewInviteInitiator: %v", err)
	}
	hf, err := api.MarshalInviteHandshakeFrame(c.chanID, msg1)
	if err != nil {
		t.Fatalf("MarshalInviteHandshakeFrame: %v", err)
	}
	if err := gw.SendRawFrame(api.EncodingJSON, hf); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	select {
	case f := <-fromNode:
		if f.Method != api.MethodE2EInviteHandshake {
			t.Fatalf("handshake reply method = %q", f.Method)
		}
		msg2, err := api.HandshakeFromFrame(f)
		if err != nil {
			t.Fatalf("HandshakeFromFrame: %v", err)
		}
		sess, err := init.Finish(msg2)
		if err != nil {
			return c, fmt.Errorf("finish: %w", err)
		}
		c.ch = api.NewChannel(c.chanID, sess)
		return c, nil
	case <-time.After(300 * time.Millisecond):
		return c, errors.New("no msg2")
	}
}

// call sends one request on the invite channel and waits for its response.
func (c *inviteConn) call(t *testing.T, method string, params any) (json.RawMessage, error) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	id := json.RawMessage("1")
	req, err := c.ch.SealRequestFrame(api.EncodingJSON, &id, method, "", raw)
	if err != nil {
		t.Fatalf("SealRequestFrame: %v", err)
	}
	if err := c.gw.SendRawFrame(api.EncodingJSON, req); err != nil {
		t.Fatalf("send req: %v", err)
	}
	select {
	case f := <-c.fromNode:
		if f.Method != "" {
			t.Fatalf("invite channel got notification %q", f.Method)
		}
		res, rpcErr, err := c.ch.OpenResponse(f)
		if err != nil {
			t.Fatalf("OpenResponse: %v", err)
		}
		if rpcErr != nil {
			return nil, rpcErr
		}
		return res, nil
	case <-time.After(2 * time.Second):
		return nil, errors.New("no response")
	}
}

func runInviteRedeem(t *testing.T, r *relayResponder, clientKP e2e.KeyPair, inviteID, psk []byte, extra [][]byte) (api.LockInviteRedeemResult, error) {
	t.Helper()
	c, err := inviteHandshake(t, r, clientKP, inviteID, psk)
	if err != nil {
		return api.LockInviteRedeemResult{}, err
	}
	raw, err := c.call(t, api.MethodLockInviteRedeem, api.LockInviteRedeemParams{ExtraKeys: extra})
	// The channel closes after its one response; wait so a follow-up handshake
	// for the same invite is not dropped by the per-invite limit.
	waitChanGone(t, r, c.chanID)
	if err != nil {
		return api.LockInviteRedeemResult{}, err
	}
	var res api.LockInviteRedeemResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return res, nil
}

func wantInviteState(t *testing.T, st *invite.Store, id []byte, want invite.State) {
	t.Helper()
	invs, err := st.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, inv := range invs {
		if string(inv.ID) == string(id) {
			if inv.State != want {
				t.Fatalf("invite state = %q, want %q", inv.State, want)
			}
			return
		}
	}
	t.Fatalf("invite %x not listed", id)
}

func waitChanGone(t *testing.T, r *relayResponder, chanID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for r.lookup(chanID) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("channel %s still open", chanID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestInviteRedeemAuthorizesClientAndExtra(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	inv, err := st.Create(time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	client, extra := mustKeyPair(t), mustKeyPair(t)
	trust := r.d.trust.Load()
	if trust.DeviceAuthorized(client.Public) {
		t.Fatal("client authorized before redeem")
	}

	res, err := runInviteRedeem(t, r, client, inv.ID, inv.Secret, [][]byte{extra.Public})
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if string(res.Tip) != string(trust.Tip()) {
		t.Fatalf("tip = %x, want %x", res.Tip, trust.Tip())
	}
	if !trust.DeviceAuthorized(client.Public) || !trust.DeviceAuthorized(extra.Public) {
		t.Fatal("client and extra key must be authorized")
	}
	wantInviteState(t, st, inv.ID, invite.StateUsed)
	invs, _ := st.List()
	if len(invs[0].Keys) != 2 {
		t.Fatalf("recorded keys = %d, want 2", len(invs[0].Keys))
	}
}

func TestInviteRedeemLogsSuccess(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	var buf syncBuffer
	r.d.SetLogger(debugLogger(&buf))
	inv, err := st.Create(time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	client, extra := mustKeyPair(t), mustKeyPair(t)
	if _, err := runInviteRedeem(t, r, client, inv.ID, inv.Secret, [][]byte{extra.Public}); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "redeemed invite") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no redeem log line in:\n%s", buf.String())
	}
	for _, want := range []string{
		"level=INFO",
		hex.EncodeToString(inv.ID),
		base64.StdEncoding.EncodeToString(client.Public[:8]),
		base64.StdEncoding.EncodeToString(extra.Public[:8]),
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q must contain %q", line, want)
		}
	}
}

func TestInviteRedeemSecondUseFails(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	inv, _ := st.Create(time.Hour)
	if _, err := runInviteRedeem(t, r, mustKeyPair(t), inv.ID, inv.Secret, nil); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	other := mustKeyPair(t)
	_, err := runInviteRedeem(t, r, other, inv.ID, inv.Secret, nil)
	wantRPCError(t, err, api.CodeInvalidRequest, invite.ErrUsed.Error())
	if r.d.trust.Load().DeviceAuthorized(other.Public) {
		t.Fatal("second redeemer must not be authorized")
	}
}

func TestInviteRedeemExpiredRevokedUnknown(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, st *invite.Store, clock *time.Time) (id, psk []byte)
		want  string // empty: no msg2
	}{
		{"expired", func(t *testing.T, st *invite.Store, clock *time.Time) ([]byte, []byte) {
			inv, _ := st.Create(time.Minute)
			*clock = clock.Add(2 * time.Minute)
			return inv.ID, inv.Secret
		}, invite.ErrExpired.Error()},
		{"revoked", func(t *testing.T, st *invite.Store, _ *time.Time) ([]byte, []byte) {
			inv, _ := st.Create(time.Hour)
			if err := st.Revoke(inv.ID); err != nil {
				t.Fatalf("Revoke: %v", err)
			}
			return inv.ID, inv.Secret
		}, invite.ErrRevoked.Error()},
		{"unknown", func(t *testing.T, _ *invite.Store, _ *time.Time) ([]byte, []byte) {
			return make([]byte, 16), make([]byte, 32)
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var clock atomic.Pointer[time.Time]
			start := time.Now()
			clock.Store(&start)
			r, st := newInviteResponder(t, func() time.Time { return *clock.Load() })
			cur := *clock.Load()
			id, psk := tc.setup(t, st, &cur)
			clock.Store(&cur)
			client := mustKeyPair(t)
			_, err := runInviteRedeem(t, r, client, id, psk, nil)
			if tc.want == "" {
				if err == nil || err.Error() != "no msg2" {
					t.Fatalf("err = %v, want no msg2", err)
				}
			} else {
				wantRPCError(t, err, api.CodeInvalidRequest, tc.want)
			}
			if r.d.trust.Load().DeviceAuthorized(client.Public) {
				t.Fatal("client must not be authorized")
			}
		})
	}
}

func TestInviteRevokedAfterHandshake(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	inv, _ := st.Create(time.Hour)
	client, extra := mustKeyPair(t), mustKeyPair(t)
	c, err := inviteHandshake(t, r, client, inv.ID, inv.Secret)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	params, _ := json.Marshal(api.LockInviteRevokeParams{ID: inv.ID})
	if _, err := r.d.handleLockInviteRevoke(context.Background(), params); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err = c.call(t, api.MethodLockInviteRedeem, api.LockInviteRedeemParams{ExtraKeys: [][]byte{extra.Public}})
	wantRPCError(t, err, api.CodeInvalidRequest, invite.ErrRevoked.Error())
	if trust := r.d.trust.Load(); trust.DeviceAuthorized(client.Public) || trust.DeviceAuthorized(extra.Public) {
		t.Fatal("nothing may be authorized")
	}
	wantInviteState(t, st, inv.ID, invite.StateRevoked)
	if invs, _ := st.List(); len(invs[0].Keys) != 0 {
		t.Fatalf("recorded keys = %x, want none", invs[0].Keys)
	}
}

func TestInviteRedeemWrongSecret(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	inv, _ := st.Create(time.Hour)
	client := mustKeyPair(t)
	c, err := inviteHandshake(t, r, client, inv.ID, make([]byte, 32))
	if err == nil {
		t.Fatal("handshake with wrong psk must not finish")
	}
	// The client cannot seal a valid record; whatever it sends must not decrypt.
	body, _ := json.Marshal("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	id := json.RawMessage("1")
	frame, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": &id, "method": api.MethodLockInviteRedeem,
		"route": api.RouteHeader{ChanID: c.chanID}, "body": json.RawMessage(body),
	})
	if err := c.gw.SendRawFrame(api.EncodingJSON, frame); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case f := <-c.fromNode:
		t.Fatalf("unexpected frame %+v", f)
	case <-time.After(200 * time.Millisecond):
	}
	waitChanGone(t, r, c.chanID)
	wantInviteState(t, st, inv.ID, invite.StatePending)
	if r.d.trust.Load().DeviceAuthorized(client.Public) {
		t.Fatal("client must not be authorized")
	}
}

func TestInviteChannelRejectsOtherMethods(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	inv, _ := st.Create(time.Hour)
	c, err := inviteHandshake(t, r, mustKeyPair(t), inv.ID, inv.Secret)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	_, err = c.call(t, "session.list", struct{}{})
	wantRPCError(t, err, api.CodeMethodNotFound, "")
	waitChanGone(t, r, c.chanID)
	wantInviteState(t, st, inv.ID, invite.StatePending)
}

func TestInviteRedeemTooManyExtraKeys(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	inv, _ := st.Create(time.Hour)
	client := mustKeyPair(t)
	_, err := runInviteRedeem(t, r, client, inv.ID, inv.Secret, [][]byte{mustKeyPair(t).Public, mustKeyPair(t).Public})
	wantRPCError(t, err, api.CodeInvalidRequest, "")
	_, err = runInviteRedeem(t, r, client, inv.ID, inv.Secret, [][]byte{make([]byte, 31)})
	wantRPCError(t, err, api.CodeInvalidRequest, "")
	wantInviteState(t, st, inv.ID, invite.StatePending)
	if r.d.trust.Load().DeviceAuthorized(client.Public) {
		t.Fatal("client must not be authorized")
	}
}

func TestInviteChannelGetsNoRegistryStream(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	inv, _ := st.Create(time.Hour)
	c, err := inviteHandshake(t, r, mustKeyPair(t), inv.ID, inv.Secret)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if cs := r.lookup(c.chanID); cs == nil || cs.stopStream != nil {
		t.Fatalf("invite channel = %+v, want open with no stream", cs)
	}
	r.d.reg.ReconcileSessions("claude", discovered("%1"))
	r.d.notifyProjectsChanged()
	select {
	case f := <-c.fromNode:
		t.Fatalf("invite channel got frame %q", f.Method)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestInviteChannelCap(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	for i := range maxInviteChannels {
		inv, _ := st.Create(time.Hour)
		if _, err := inviteHandshake(t, r, mustKeyPair(t), inv.ID, inv.Secret); err != nil {
			t.Fatalf("handshake %d: %v", i, err)
		}
	}
	inv, _ := st.Create(time.Hour)
	if _, err := inviteHandshake(t, r, mustKeyPair(t), inv.ID, inv.Secret); err == nil || err.Error() != "no msg2" {
		t.Fatalf("handshake past cap: err = %v, want no msg2", err)
	}
}

func TestInviteChannelReplacesUnproven(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	inv, _ := st.Create(time.Hour)
	// Knowing the ID is enough to open a channel; it must not block the redeem.
	squatter, err := inviteHandshake(t, r, mustKeyPair(t), inv.ID, make([]byte, 32))
	if err == nil {
		t.Fatal("handshake with a wrong secret finished")
	}
	if r.lookup(squatter.chanID) == nil {
		t.Fatal("issuer kept no channel for the squatter")
	}
	if _, err := runInviteRedeem(t, r, mustKeyPair(t), inv.ID, inv.Secret, nil); err != nil {
		t.Fatalf("redeem while an unproven channel is open: %v", err)
	}
	if r.lookup(squatter.chanID) != nil {
		t.Fatal("unproven channel not replaced")
	}
	wantInviteState(t, st, inv.ID, invite.StateUsed)
}

func TestInviteChannelBusyDropsNewcomer(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	inv, _ := st.Create(time.Hour)
	first, err := inviteHandshake(t, r, mustKeyPair(t), inv.ID, inv.Secret)
	if err != nil {
		t.Fatalf("first handshake: %v", err)
	}
	r.lookup(first.chanID).inviteUsed.Store(true) // its request is being served
	if _, err := inviteHandshake(t, r, mustKeyPair(t), inv.ID, inv.Secret); err == nil || err.Error() != "no msg2" {
		t.Fatalf("handshake while the first redeems: err = %v, want no msg2", err)
	}
	if r.lookup(first.chanID) == nil {
		t.Fatal("busy channel closed by a newcomer")
	}
}

func TestInviteNonPendingDoesNotConsumeCap(t *testing.T) {
	r, st := newInviteResponder(t, time.Now)
	var used []invite.Invite
	for range 2 {
		inv, _ := st.Create(time.Hour)
		if _, err := runInviteRedeem(t, r, mustKeyPair(t), inv.ID, inv.Secret, nil); err != nil {
			t.Fatalf("redeem: %v", err)
		}
		used = append(used, inv)
	}
	// A channel for a used invite does not take a pending-invite slot.
	if _, err := inviteHandshake(t, r, mustKeyPair(t), used[1].ID, used[1].Secret); err != nil {
		t.Fatalf("used-invite handshake: %v", err)
	}
	for i := range maxInviteChannels {
		inv, _ := st.Create(time.Hour)
		if _, err := inviteHandshake(t, r, mustKeyPair(t), inv.ID, inv.Secret); err != nil {
			t.Fatalf("pending handshake %d: %v", i, err)
		}
	}
	_, err := runInviteRedeem(t, r, mustKeyPair(t), used[0].ID, used[0].Secret, nil)
	wantRPCError(t, err, api.CodeInvalidRequest, invite.ErrUsed.Error())
}

func TestInviteChannelIdleClose(t *testing.T) {
	prev := inviteIdleTimeout
	inviteIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { inviteIdleTimeout = prev })
	r, st := newInviteResponder(t, time.Now)
	inv, _ := st.Create(time.Hour)
	c, err := inviteHandshake(t, r, mustKeyPair(t), inv.ID, inv.Secret)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	waitChanGone(t, r, c.chanID)
	wantInviteState(t, st, inv.ID, invite.StatePending)
}
