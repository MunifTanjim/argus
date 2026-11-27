package gateway

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
)

const revokeMaster = "master-secret"

func revokeServer(t *testing.T) (*Server, *clienttoken.Store, string) {
	t.Helper()
	store := clienttoken.New(t.TempDir())
	srv := NewServer(New(0), nil, func(tok string) bool { return tok == revokeMaster || store.Authorize(tok) })
	srv.SetClientTokens(store, revokeMaster)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return srv, store, "ws" + strings.TrimPrefix(hs.URL, "http") + "/client"
}

func pairClient(t *testing.T, store *clienttoken.Store, url string) (*api.Peer, string) {
	t.Helper()
	tok, err := clienttoken.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	store.Pend(tok)
	return dialClient(t, url, tok), tok
}

func dialClient(t *testing.T, url, tok string) *api.Peer {
	t.Helper()
	p, err := api.DialWSPeer(context.Background(), url, tok, nil, api.PeerOptions{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestRemoveClosesLiveConnections(t *testing.T) {
	srv, store, url := revokeServer(t)
	_, nodeTap := connectNode(t, srv, "n1")
	dev, tok := pairClient(t, store, url)
	var open api.RelayOpenResult
	if err := dev.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &open); err != nil {
		t.Fatalf("relay.open: %v", err)
	}

	admin := dialClient(t, url, revokeMaster)
	if err := admin.Call(api.MethodClientsRemove, api.ClientRemoveParams{Token: clienttoken.HashToken(tok)}, nil); err != nil {
		t.Fatalf("clients.remove: %v", err)
	}

	select {
	case <-dev.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("revoked client connection stayed open")
	}
	n := nodeTap.await(t, "relay.closed", isNote(api.MethodRelayClosed)).(api.Notification)
	if p, _ := api.Decode[api.RelayCloseParams](n.Params); p.ChanID != open.ChanID {
		t.Fatalf("relay.closed chan = %q, want %q", p.ChanID, open.ChanID)
	}
}

func TestRemoveLeavesOtherTokens(t *testing.T) {
	_, store, url := revokeServer(t)
	devA, tokA := pairClient(t, store, url)
	devB, _ := pairClient(t, store, url)

	admin := dialClient(t, url, revokeMaster)
	if err := admin.Call(api.MethodClientsRemove, api.ClientRemoveParams{Token: clienttoken.HashToken(tokA)}, nil); err != nil {
		t.Fatalf("clients.remove: %v", err)
	}
	select {
	case <-devA.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("removed client stayed connected")
	}
	if err := devB.Call(api.MethodNodesList, nil, nil); err != nil {
		t.Fatalf("other client should keep working: %v", err)
	}
	if err := admin.Call(api.MethodNodesList, nil, nil); err != nil {
		t.Fatalf("admin should keep working: %v", err)
	}
}

func TestRelayOpenRejectsRevokedToken(t *testing.T) {
	srv, store, url := revokeServer(t)
	connectNode(t, srv, "n1")
	dev, tok := pairClient(t, store, url)
	waitTracked(t, srv, clienttoken.HashToken(tok))
	if err := store.Remove(clienttoken.HashToken(tok)); err != nil {
		t.Fatal(err)
	}
	err := dev.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, nil)
	rpcErr, ok := err.(*api.RPCError)
	if !ok || rpcErr.Code != api.CodeInvalidRequest || !strings.Contains(rpcErr.Message, "token revoked") {
		t.Fatalf("want invalid-request token revoked, got %v", err)
	}
}

func TestRemoveBetweenAcceptAndTrackClosesConn(t *testing.T) {
	srv, store, url := revokeServer(t)
	tok, err := clienttoken.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	store.Pend(tok)
	srv.afterClientAccept = func() {
		if err := store.Remove(clienttoken.HashToken(tok)); err != nil {
			t.Error(err)
		}
	}
	dev := dialClient(t, url, tok)
	select {
	case <-dev.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("client removed before tracking stayed connected")
	}
}

// waitTracked waits until the gateway has registered the client connection, so a
// later out-of-band token removal is seen by relay.open rather than the accept path.
func waitTracked(t *testing.T, srv *Server, hash string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.relayMu.Lock()
		n := len(srv.clientConns[hash])
		srv.relayMu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("client connection was never tracked")
}
