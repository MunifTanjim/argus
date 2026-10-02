package client

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

func noopHandle(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
	return nil, nil, nil
}

func (g *fakeMultiGateway) handshakeSeen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gotHandshake
}

func TestClientUnauthorizedDeviceOpensNoChannels(t *testing.T) {
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	genesis := lg.Tip()

	node := &fakeNode{id: "n1", key: mustKP(t), handle: noopHandle}
	_ = lg.AuthorizeDevice(node.key.Public, signer)

	gw, clientConn := newFakeMultiGateway(t, node)
	gw.chain = trustlog.MarshalChain(lg.Entries())
	defer gw.peer.Close()

	c, err := NewE2EClientWithGenesis(clientConn, genesis)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if len(c.byNodeSnapshot()) != 0 {
		t.Fatal("an unauthorized device must open no channels")
	}
	if gw.handshakeSeen() {
		t.Fatal("an unauthorized device must send no handshake")
	}
}

func TestClientUnauthorizedDeviceOpensLockDisabledNode(t *testing.T) {
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	genesis := lg.Tip()

	node := &fakeNode{id: "n1", key: mustKP(t), handle: noopHandle, lockDisabled: true}
	_ = lg.AuthorizeDevice(node.key.Public, signer)

	gw, clientConn := newFakeMultiGateway(t, node)
	gw.chain = trustlog.MarshalChain(lg.Entries())
	defer gw.peer.Close()

	c, err := NewE2EClientWithGenesis(clientConn, genesis)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if _, ok := c.byNodeSnapshot()["n1"]; !ok {
		t.Fatal("a node with lock enforcement disabled accepts any device, so it must be opened")
	}
}

func TestClientOpensChannelsOnceDeviceIsAuthorized(t *testing.T) {
	SetTrustSyncIntervalForTest(time.Hour)
	t.Cleanup(func() { SetTrustSyncIntervalForTest(5 * time.Minute) })

	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	genesis := lg.Tip()

	node := &fakeNode{id: "n1", key: mustKP(t), handle: noopHandle}
	_ = lg.AuthorizeDevice(node.key.Public, signer)

	gw, clientConn := newFakeMultiGateway(t, node)
	gw.chain = trustlog.MarshalChain(lg.Entries())
	defer gw.peer.Close()

	c, err := NewE2EClientWithGenesis(clientConn, genesis)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if len(c.byNodeSnapshot()) != 0 {
		t.Fatal("precondition: no channels before authorization")
	}

	_ = lg.AuthorizeDevice(c.static.Public, signer)
	gw.setChain(trustlog.MarshalChain(lg.Entries()))
	if err := gw.peer.Notify(api.MethodNodeEvent, api.NodeEvent{Type: api.NodeEventTrustChanged}); err != nil {
		t.Fatalf("notify: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := c.byNodeSnapshot()["n1"]; ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("authorizing this device must open channels to the roster without a reconnect")
}
