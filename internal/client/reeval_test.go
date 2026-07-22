package client

import (
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

func TestReevaluateChannelsDropsRevoked(t *testing.T) {
	// Build a trust chain authorizing both nodeA's and nodeB's identity keys.
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	head := lg.Head()

	nodeA := &fakeNode{id: "nodeA", key: mustKP(t)}
	nodeB := &fakeNode{id: "nodeB", key: mustKP(t)}

	_ = lg.AuthorizeDevice(nodeA.key.Public, signer)
	_ = lg.AuthorizeDevice(nodeB.key.Public, signer)
	chain := trustlog.MarshalChain(lg.Entries())

	noop := func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return nil, nil, nil
	}
	nodeA.handle = noop
	nodeB.handle = noop

	gw, clientConn := newFakeMultiGateway(t, nodeA, nodeB)
	gw.chain = chain
	defer gw.peer.Close()

	c, err := NewE2EClientWithGenesis(clientConn, head)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()

	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Both nodes should have channels after Connect.
	snap := c.byNodeSnapshot()
	if _, ok := snap["nodeA"]; !ok {
		t.Fatal("nodeA should have a channel before revocation")
	}
	if _, ok := snap["nodeB"]; !ok {
		t.Fatal("nodeB should have a channel before revocation")
	}

	// Revoke nodeB directly on the trust store.
	if _, err := c.trust.RevokeDevice(nodeB.key.Public, signer); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}

	c.reevaluateChannels()

	// nodeB must be gone; nodeA retained.
	snap = c.byNodeSnapshot()
	if _, ok := snap["nodeA"]; !ok {
		t.Fatal("nodeA must be retained after nodeB revocation")
	}
	if _, ok := snap["nodeB"]; ok {
		t.Fatal("nodeB must be removed from byNode after revocation")
	}

	// byChanID must also no longer contain the revoked channel.
	c.mu.Lock()
	for _, nc := range c.byChanID {
		if nc.nodeID == "nodeB" {
			c.mu.Unlock()
			t.Fatal("byChanID still contains revoked nodeB channel")
		}
	}
	c.mu.Unlock()
}

func TestReevaluateChannelsDisabledStoreDropsNothing(t *testing.T) {
	// Build a trust chain with a disablement commitment; authorize only nodeA.
	signer, _ := trustlog.GenerateSigner()
	secret, err := trustlog.GenerateDisablementSecret()
	if err != nil {
		t.Fatalf("GenerateDisablementSecret: %v", err)
	}
	commitment := trustlog.DisablementCommitment(secret)
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, [][]byte{commitment})
	head := lg.Head()

	nodeA := &fakeNode{id: "nodeA", key: mustKP(t)}
	nodeB := &fakeNode{id: "nodeB", key: mustKP(t)}

	_ = lg.AuthorizeDevice(nodeA.key.Public, signer)
	chain := trustlog.MarshalChain(lg.Entries())

	noop := func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return nil, nil, nil
	}
	nodeA.handle = noop
	nodeB.handle = noop

	gw, clientConn := newFakeMultiGateway(t, nodeA, nodeB)
	gw.chain = chain
	defer gw.peer.Close()

	c, err2 := NewE2EClientWithGenesis(clientConn, head)
	if err2 != nil {
		t.Fatalf("new: %v", err2)
	}
	defer c.Close()

	// Ingest the chain and disable the store before connecting.
	if _, err := c.trust.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if _, err := c.trust.Disable(secret, signer); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Disabled store connects all nodes.
	snap := c.byNodeSnapshot()
	if _, ok := snap["nodeA"]; !ok {
		t.Fatal("nodeA should have a channel (disabled store connects all)")
	}
	if _, ok := snap["nodeB"]; !ok {
		t.Fatal("nodeB should have a channel (disabled store connects all)")
	}

	// reevaluateChannels on a disabled store must drop nothing.
	c.reevaluateChannels()

	snap = c.byNodeSnapshot()
	if _, ok := snap["nodeA"]; !ok {
		t.Fatal("nodeA must be retained (disabled store drops nothing)")
	}
	if _, ok := snap["nodeB"]; !ok {
		t.Fatal("nodeB must be retained (disabled store drops nothing)")
	}
}
