package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/invite"
)

// fakeInviteIssuer plays the gateway plus the issuing node's invite responder.
type fakeInviteIssuer struct {
	peer      *api.Peer
	key       e2e.KeyPair
	inviteID  []byte
	secret    []byte
	silent    bool          // never answer msg1 (issuer offline behind the gateway)
	openErr   *api.RPCError // relay.open error
	openHang  bool          // never answer relay.open (silent gateway)
	redeemErr *api.RPCError
	hangUp    bool // close the gateway connection instead of answering lock.inviteRedeem
	tip       []byte

	ch          *api.Channel
	gotStatic   chan []byte
	gotRedeemed chan api.LockInviteRedeemParams
}

func newFakeInviteIssuer(t *testing.T) (*fakeInviteIssuer, net.Conn, invite.Blob) {
	t.Helper()
	key, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeInviteIssuer{
		key:         key,
		inviteID:    bytes.Repeat([]byte{0x11}, 16),
		secret:      bytes.Repeat([]byte{0x22}, 32),
		tip:         []byte("tip"),
		gotStatic:   make(chan []byte, 1),
		gotRedeemed: make(chan api.LockInviteRedeemParams, 1),
	}
	gwConn, clientConn := net.Pipe()
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	f.peer = api.NewPeer(gwConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			if method != api.MethodRelayOpen {
				return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: method}
			}
			if f.openHang {
				<-hang
			}
			if f.openErr != nil {
				return nil, f.openErr
			}
			return api.RelayOpenResult{ChanID: "c7"}, nil
		},
		OnRelayFrame: f.onFrame,
	})
	t.Cleanup(func() { f.peer.Close() })
	b := invite.Blob{
		GatewayURL: "wss://gw.example",
		Token:      "tok",
		Genesis:    bytes.Repeat([]byte{0x33}, 32),
		IssuerID:   "issuer",
		IssuerKey:  key.Public,
		InviteID:   f.inviteID,
		Secret:     f.secret,
	}
	return f, clientConn, b
}

func (f *fakeInviteIssuer) onFrame(_ *api.Peer, fr api.RelayFrame) {
	if fr.Method == api.MethodE2EInviteHandshake {
		if f.silent {
			return
		}
		msg1, err := api.HandshakeFromFrame(fr)
		if err != nil {
			return
		}
		lookup := func(payload []byte) ([]byte, bool) {
			return f.secret, bytes.Equal(payload, f.inviteID)
		}
		sess, clientStatic, _, msg2, err := e2e.RespondInvite(f.key, api.ChannelPrologue("issuer", fr.Route.ChanID), msg1, lookup)
		if err != nil {
			return
		}
		f.gotStatic <- clientStatic
		f.ch = api.NewChannel(fr.Route.ChanID, sess)
		hf, _ := api.MarshalInviteHandshakeFrame(fr.Route.ChanID, msg2)
		_ = f.peer.SendRawFrame(api.EncodingJSON, hf)
		return
	}
	if f.ch == nil || fr.Method != api.MethodLockInviteRedeem {
		return
	}
	raw, err := f.ch.OpenParams(fr)
	if err != nil {
		return
	}
	var p api.LockInviteRedeemParams
	_ = json.Unmarshal(raw, &p)
	f.gotRedeemed <- p
	if f.hangUp {
		f.peer.Close()
		return
	}
	var result []byte
	if f.redeemErr == nil {
		result, _ = json.Marshal(api.LockInviteRedeemResult{Tip: f.tip})
	}
	rf, _ := f.ch.SealResponseFrame(api.EncodingJSON, fr.ID, result, f.redeemErr)
	_ = f.peer.SendRawFrame(api.EncodingJSON, rf)
}

func setInviteHandshakeTimeout(t *testing.T, d time.Duration) {
	old := inviteHandshakeTimeout
	inviteHandshakeTimeout = d
	t.Cleanup(func() { inviteHandshakeTimeout = old })
}

func TestRedeemInviteHappyPath(t *testing.T) {
	f, conn, b := newFakeInviteIssuer(t)
	nodeKP, _ := e2e.GenerateKeyPair()
	extra := []byte("client-identity-public-key-32byt")

	res, err := RedeemInvite(context.Background(), conn, nodeKP, b, [][]byte{extra})
	if err != nil {
		t.Fatalf("RedeemInvite: %v", err)
	}
	if !bytes.Equal(res.Tip, f.tip) {
		t.Errorf("Tip = %q, want %q", res.Tip, f.tip)
	}
	if got := <-f.gotStatic; !bytes.Equal(got, nodeKP.Public) {
		t.Errorf("issuer saw initiator static %x, want the node identity %x", got, nodeKP.Public)
	}
	if p := <-f.gotRedeemed; len(p.ExtraKeys) != 1 || !bytes.Equal(p.ExtraKeys[0], extra) {
		t.Errorf("ExtraKeys = %x, want [%x]", p.ExtraKeys, extra)
	}
}

func TestRedeemInviteIssuerOffline(t *testing.T) {
	setInviteHandshakeTimeout(t, 100*time.Millisecond)

	f, conn, b := newFakeInviteIssuer(t)
	f.silent = true
	nodeKP, _ := e2e.GenerateKeyPair()

	start := time.Now()
	_, err := RedeemInvite(context.Background(), conn, nodeKP, b, nil)
	if !errors.Is(err, ErrIssuerUnreachable) {
		t.Fatalf("err = %v, want ErrIssuerUnreachable", err)
	}
	if errors.Is(err, ErrRedeemResponseLost) {
		t.Errorf("err = %v: a failure before the request went out is not a lost response", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s; the handshake timeout should bound it", d)
	}
}

func TestRedeemInviteResponseLost(t *testing.T) {
	f, conn, b := newFakeInviteIssuer(t)
	f.hangUp = true
	nodeKP, _ := e2e.GenerateKeyPair()

	_, err := RedeemInvite(context.Background(), conn, nodeKP, b, nil)
	if !errors.Is(err, ErrRedeemResponseLost) {
		t.Fatalf("err = %v, want ErrRedeemResponseLost", err)
	}
	if !strings.Contains(err.Error(), "gateway connection closed") {
		t.Errorf("err = %v, want it to keep the cause", err)
	}
}

func TestRedeemInviteUnknownNodeIsUnreachable(t *testing.T) {
	f, conn, b := newFakeInviteIssuer(t)
	f.openErr = &api.RPCError{Code: api.CodeInvalidRequest, Message: api.ErrMsgUnknownNode + ": issuer"}
	nodeKP, _ := e2e.GenerateKeyPair()

	_, err := RedeemInvite(context.Background(), conn, nodeKP, b, nil)
	if !errors.Is(err, ErrIssuerUnreachable) {
		t.Fatalf("err = %v, want ErrIssuerUnreachable", err)
	}
}

func TestRedeemInviteReturnsIssuerError(t *testing.T) {
	f, conn, b := newFakeInviteIssuer(t)
	f.redeemErr = &api.RPCError{Code: api.CodeInvalidRequest, Message: invite.ErrUsed.Error()}
	nodeKP, _ := e2e.GenerateKeyPair()

	_, err := RedeemInvite(context.Background(), conn, nodeKP, b, nil)
	var rpcErr *api.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Message != invite.ErrUsed.Error() {
		t.Fatalf("err = %v, want the issuer's RPC error", err)
	}
	if errors.Is(err, ErrRedeemResponseLost) {
		t.Errorf("err = %v: an issuer answer is not a lost response", err)
	}
}

func TestRedeemInviteRelayOpenHangs(t *testing.T) {
	setInviteHandshakeTimeout(t, 100*time.Millisecond)

	f, conn, b := newFakeInviteIssuer(t)
	f.openHang = true
	nodeKP, _ := e2e.GenerateKeyPair()

	start := time.Now()
	_, err := RedeemInvite(context.Background(), conn, nodeKP, b, nil)
	if !errors.Is(err, ErrIssuerUnreachable) {
		t.Fatalf("err = %v, want ErrIssuerUnreachable", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s; the handshake timeout should bound relay.open", d)
	}
}
