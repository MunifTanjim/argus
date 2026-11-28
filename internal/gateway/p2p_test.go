package gateway

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
)

var p2pCaps = api.NodeCapabilities{P2P: true}

func p2pServer(t *testing.T) (*Server, *clienttoken.Store, string) {
	t.Helper()
	store := clienttoken.New(t.TempDir())
	srv := NewServer(New(0), nil, func(tok string) bool { return tok == revokeMaster || store.Authorize(tok) })
	srv.SetClientTokens(store, revokeMaster)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return srv, store, "ws" + strings.TrimPrefix(hs.URL, "http") + "/client"
}

func tokenListOf(t *testing.T, ev any) []string {
	t.Helper()
	p, err := api.Decode[api.TokenListChangedParams](ev.(api.Notification).Params)
	if err != nil {
		t.Fatal(err)
	}
	return p.Hashes
}

func TestRelayOpenSendsChannelOpened(t *testing.T) {
	srv, store, url := p2pServer(t)
	_, tap := connectNodeCaps(t, srv, "n1", p2pCaps)
	dev, tok := pairClient(t, store, url)
	chanID := openFrom(t, dev, "n1")

	n := tap.await(t, "channel.opened", isNote(api.MethodChannelOpened)).(api.Notification)
	p, _ := api.Decode[api.ChannelOpenedParams](n.Params)
	if p.ChanID != chanID || p.TokenHash != clienttoken.HashToken(tok) || p.NodeID != "" {
		t.Fatalf("channel.opened = %+v", p)
	}
}

func TestChannelOpenedBeforeFirstFrame(t *testing.T) {
	srv, store, url := p2pServer(t)
	_, tap := connectNodeCaps(t, srv, "n1", p2pCaps)
	dev, _ := pairClient(t, store, url)
	chanID := openFrom(t, dev, "n1")
	if err := dev.SendRawFrame(api.EncodingCBOR, relayFrom(t, chanID, "x")); err != nil {
		t.Fatalf("send frame: %v", err)
	}
	ev := tap.await(t, "channel.opened or frame", func(ev any) bool {
		return isFrame(ev) || isNote(api.MethodChannelOpened)(ev)
	})
	if isFrame(ev) {
		t.Fatal("relayed frame arrived before channel.opened")
	}
}

func TestNodeOpenedChannelOpened(t *testing.T) {
	srv := NewServer(New(0), nil, nil)
	srv.SetClientTokens(clienttoken.New(t.TempDir()), revokeMaster)
	a, _, _, bID, _, bTap := func() (*api.Peer, *api.Peer, string, string, *nodeTap, *nodeTap) {
		a, aTap := connectNodeCaps(t, srv, "na", p2pCaps)
		b, bTap := connectNodeCaps(t, srv, "nb", p2pCaps)
		return a, b, "na", "nb", aTap, bTap
	}()
	chanID := openFrom(t, a, bID)

	n := bTap.await(t, "channel.opened", isNote(api.MethodChannelOpened)).(api.Notification)
	p, _ := api.Decode[api.ChannelOpenedParams](n.Params)
	if p.ChanID != chanID || p.NodeID != "na" || p.TokenHash != clienttoken.HashToken(revokeMaster) {
		t.Fatalf("channel.opened = %+v", p)
	}
}

func TestLegacyNodeGetsPeerOpened(t *testing.T) {
	srv, store, url := p2pServer(t)
	_, tap := connectNode(t, srv, "n1")
	a, _ := connectNode(t, srv, "n2")
	dev, _ := pairClient(t, store, url)

	openFrom(t, dev, "n1")
	chanID := openFrom(t, a, "n1")

	n := tap.await(t, "relay.peer_opened", isNote(api.MethodRelayPeerOpened)).(api.Notification)
	p, _ := api.Decode[api.RelayPeerOpenedParams](n.Params)
	if p.ChanID != chanID || p.FromNode != "n2" {
		t.Fatalf("relay.peer_opened = %+v", p)
	}
	for {
		select {
		case ev := <-tap.events:
			if m, ok := ev.(api.Notification); ok && (m.Method == api.MethodChannelOpened || m.Method == api.MethodTokenListChanged) {
				t.Fatalf("legacy node got %s", m.Method)
			}
		default:
			return
		}
	}
}

func TestTokenListOnIdentify(t *testing.T) {
	srv, store, url := p2pServer(t)
	_, tok := pairClient(t, store, url)
	dialClient(t, url, revokeMaster)
	_, tap := connectNodeCaps(t, srv, "n1", p2pCaps)

	hashes := tokenListOf(t, tap.await(t, "token.list.changed", isNote(api.MethodTokenListChanged)))
	if len(hashes) != 1 || hashes[0] != clienttoken.HashToken(tok) {
		t.Fatalf("hashes = %v, want only the paired token", hashes)
	}
}

func TestTokenListOnRemove(t *testing.T) {
	srv, store, url := p2pServer(t)
	_, tok := pairClient(t, store, url)
	_, tap := connectNodeCaps(t, srv, "n1", p2pCaps)
	tap.await(t, "initial list", isNote(api.MethodTokenListChanged))

	admin := dialClient(t, url, revokeMaster)
	if err := admin.Call(api.MethodClientsRemove, api.ClientRemoveParams{Token: clienttoken.HashToken(tok)}, nil); err != nil {
		t.Fatal(err)
	}
	if hashes := tokenListOf(t, tap.await(t, "list after remove", isNote(api.MethodTokenListChanged))); len(hashes) != 0 {
		t.Fatalf("hashes = %v, want empty", hashes)
	}
}

func TestTokenListOnPairPromotion(t *testing.T) {
	srv, store, url := p2pServer(t)
	_, tap := connectNodeCaps(t, srv, "n1", p2pCaps)
	if h := tokenListOf(t, tap.await(t, "initial list", isNote(api.MethodTokenListChanged))); len(h) != 0 {
		t.Fatalf("initial hashes = %v", h)
	}
	_, tok := pairClient(t, store, url)
	hashes := tokenListOf(t, tap.await(t, "list after pairing", isNote(api.MethodTokenListChanged)))
	if len(hashes) != 1 || hashes[0] != clienttoken.HashToken(tok) {
		t.Fatalf("hashes = %v", hashes)
	}
}

func TestServerInfoDeclaresP2P(t *testing.T) {
	s := NewServer(New(0), nil, nil)
	res, err := s.clientSrv.DispatchFunc()(context.Background(), api.MethodServerInfo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := res.(api.ServerInfo).Capabilities; c == nil || !c.P2P {
		t.Fatalf("capabilities = %+v, want P2P", c)
	}
}

func TestTokenListNotMissedWhenIdentifyInterleaves(t *testing.T) {
	srv, store, url := p2pServer(t)
	_, tok := pairClient(t, store, url)
	_, tapA := connectNodeCaps(t, srv, "na", p2pCaps)
	tapA.await(t, "initial list", isNote(api.MethodTokenListChanged))

	if err := store.Remove(clienttoken.HashToken(tok)); err != nil {
		t.Fatal(err)
	}
	_, tapB := connectNodeCaps(t, srv, "nb", p2pCaps) // identifies between the store change and the publish
	tapB.await(t, "identify list", isNote(api.MethodTokenListChanged))
	srv.PublishTokenList()

	if h := tokenListOf(t, tapA.await(t, "list after removal", isNote(api.MethodTokenListChanged))); len(h) != 0 {
		t.Fatalf("hashes = %v, want empty", h)
	}
}
