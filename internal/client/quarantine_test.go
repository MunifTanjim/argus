package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

func genesisChainForTest(t *testing.T) (chain []byte, genesis []byte) {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	return trustlog.MarshalChain(log.Entries()), log.Tip()
}

func TestUnpinnedClientQuarantinesWhenNetworkIsLocked(t *testing.T) {
	chain, genesis := genesisChainForTest(t)
	ch := make(chan []byte, 1)
	ch <- chain

	m, err := NewE2EClientWithIdentity(trustGatewayConn(t, ch), mustKP(t), nil, "")
	if err != nil {
		t.Fatalf("NewE2EClientWithIdentity: %v", err)
	}
	defer m.Close()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if !m.Quarantined() {
		t.Fatal("an unpinned client on a network with a trust log must quarantine")
	}
	if got := m.gate.Genesis(); !bytes.Equal(got, genesis) {
		t.Fatalf("gate genesis = %x, want %x", got, genesis)
	}
}

func TestUnpinnedClientStaysOpenWithNoTrustLog(t *testing.T) {
	m, err := NewE2EClientWithIdentity(trustGatewayConn(t, make(chan []byte)), mustKP(t), nil, "")
	if err != nil {
		t.Fatalf("NewE2EClientWithIdentity: %v", err)
	}
	defer m.Close()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if m.Quarantined() {
		t.Fatal("a network with no trust log must not quarantine anyone")
	}
}

func TestUnpinnedClientQuarantinesMidSession(t *testing.T) {
	clientTrustSyncInterval.Store(int64(20 * time.Millisecond))
	t.Cleanup(func() { clientTrustSyncInterval.Store(int64(5 * time.Minute)) })

	chain, _ := genesisChainForTest(t)
	ch := make(chan []byte, 1)

	m, err := NewE2EClientWithIdentity(trustGatewayConn(t, ch), mustKP(t), nil, "")
	if err != nil {
		t.Fatalf("NewE2EClientWithIdentity: %v", err)
	}
	defer m.Close()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if m.Quarantined() {
		t.Fatal("precondition: no chain yet, so no quarantine")
	}

	ch <- chain // the network gets locked under a running client

	deadline := time.Now().Add(2 * time.Second)
	for !m.Quarantined() {
		if time.Now().After(deadline) {
			t.Fatal("client did not quarantine within 2s of the network gaining a trust log")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestUnpinnedClientDropsChannelOnQuarantine verifies the reevaluateChannels
// gate-tripped branch: every open byNode entry must be closed when the network
// gains a trust log under a running client.
func TestUnpinnedClientDropsChannelOnQuarantine(t *testing.T) {
	clientTrustSyncInterval.Store(int64(20 * time.Millisecond))
	t.Cleanup(func() { clientTrustSyncInterval.Store(int64(5 * time.Minute)) })

	chain, _ := genesisChainForTest(t)

	noop := func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return nil, nil, nil
	}
	n1 := &fakeNode{id: "n1", key: mustKP(t), handle: noop}
	gw, clientConn := newFakeMultiGateway(t, n1)
	defer gw.peer.Close()

	m, err := NewE2EClientWithIdentity(clientConn, mustKP(t), nil, "")
	if err != nil {
		t.Fatalf("NewE2EClientWithIdentity: %v", err)
	}
	defer m.Close()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if snap := m.byNodeSnapshot(); len(snap) == 0 {
		t.Fatal("precondition: expected an open channel to n1 before quarantine")
	}

	gw.setChain(chain) // the network gains a trust log mid-session

	deadline := time.Now().Add(2 * time.Second)
	for !m.Quarantined() {
		if time.Now().After(deadline) {
			t.Fatal("client did not quarantine within 2s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if snap := m.byNodeSnapshot(); len(snap) != 0 {
		t.Fatalf("expected empty byNode after quarantine, got %d channel(s)", len(snap))
	}
}

// TestUnpinnedClientAdoptRaceWithTrip is a deterministic test for the TOCTOU fix
// in openChannel. relay.open is blocked until the gate has been tripped and
// reevaluateChannels has swept byNode (finding nothing, because the channel is
// mid-handshake in byChanID). After the handshake completes, the fixed openChannel
// must refuse byNode registration because the gate is already tripped.
func TestUnpinnedClientAdoptRaceWithTrip(t *testing.T) {
	nodeKP := mustKP(t)
	const nodeID = "n1"

	// relayOpened is closed when the gateway's relay.open handler is entered:
	// at that point adoptNode has already passed the openIfEligible gate check.
	// relayRelease is closed when relay.open may return.
	relayOpened := make(chan struct{})
	relayRelease := make(chan struct{})

	gwConn, clientConn := net.Pipe()
	gw := api.NewPeer(gwConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			switch method {
			case api.MethodNodesList:
				return api.NodesListResult{Nodes: []api.NodeDescriptor{}}, nil
			case api.MethodRelayOpen:
				close(relayOpened) // signal: adoptNode passed the gate check
				<-relayRelease     // block: hold until the test has tripped the gate
				return api.RelayOpenResult{ChanID: "c1"}, nil
			case api.MethodTrustLogPull:
				return api.TrustLogPullResult{Chains: [][]byte{}}, nil
			}
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: method}
		},
		OnRelayFrame: func(p *api.Peer, f api.RelayFrame) {
			if f.Method != api.MethodE2EHandshake {
				return
			}
			msg1, err := api.HandshakeFromFrame(f)
			if err != nil {
				return
			}
			_, _, msg2, err := e2e.Respond(nodeKP, api.ChannelPrologue(nodeID, f.Route.ChanID), msg1)
			if err != nil {
				return
			}
			hf, _ := api.MarshalHandshakeFrame(f.Route.ChanID, msg2)
			_ = p.SendRawFrame(hf)
		},
	})
	defer gw.Close()

	m, err := NewE2EClientWithIdentity(clientConn, mustKP(t), nil, "")
	if err != nil {
		t.Fatalf("NewE2EClientWithIdentity: %v", err)
	}
	defer m.Close()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	nd := api.NodeDescriptor{
		ID: nodeID, Label: nodeID + "-box", Online: true,
		IdentityPubKey: base64.StdEncoding.EncodeToString(nodeKP.Public),
	}
	_ = gw.Notify(api.MethodNodeEvent, api.NodeEvent{Type: api.NodeEventAdded, Node: nd})

	// Wait until adoptNode is inside relay.open (past the gate check, channel not
	// yet in byNode). This is the exact race window the fix must close.
	select {
	case <-relayOpened:
	case <-time.After(2 * time.Second):
		t.Fatal("adoptNode did not reach relay.open within 2s")
	}

	// Trip the gate and sweep byNode (empty: channel is mid-handshake in byChanID).
	// Without the fix, the channel would enter byNode after relay.open returns.
	m.gate.Trip([]byte("fake-genesis"))
	m.reevaluateChannels()

	close(relayRelease) // unblock relay.open; handshake proceeds now

	// Wait for adoptNode to finish. The opening map is cleared by openIfEligible's
	// defer regardless of whether openChannel registered the channel or refused it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		m.mu.Lock()
		inFlight := len(m.opening)
		m.mu.Unlock()
		if inFlight == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("adoptNode did not finish within 2s after relay.open unblocked")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The gate check in openChannel must have refused byNode registration.
	if snap := m.byNodeSnapshot(); len(snap) != 0 {
		t.Fatalf("channel survived into byNode after quarantine + adopt race: got %v", snap)
	}
}

// disabledChainForTest builds a genesis carrying one disablement commitment and then
// consumes it, producing the chain every device holds after `argus lock disable`.
func disabledChainForTest(t *testing.T) (chain []byte, genesis []byte) {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	secret, err := trustlog.GenerateDisablementSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, [][]byte{trustlog.DisablementCommitment(secret)})
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	tip := log.Tip()
	if err := log.Disable(secret, signer); err != nil {
		t.Fatalf("disable: %v", err)
	}
	return trustlog.MarshalChain(log.Entries()), tip
}

// The client mirrors the node: a pin to a disabled chain protects nothing, so once
// the network serves a different root the dashboard must go dark and say why.
func TestClientQuarantinesWhenItsDisabledChainIsSuperseded(t *testing.T) {
	disabled, ownGenesis := disabledChainForTest(t)
	foreign, foreignGenesis := genesisChainForTest(t)
	ch := make(chan []byte, 1)
	ch <- foreign

	m, err := NewE2EClientWithIdentity(trustGatewayConn(t, ch), mustKP(t), ownGenesis, "")
	if err != nil {
		t.Fatalf("NewE2EClientWithIdentity: %v", err)
	}
	defer m.Close()
	if _, err := m.trust.Ingest(disabled); err != nil {
		t.Fatalf("seed disabled chain: %v", err)
	}
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	m.syncTrustLog()

	if !m.Quarantined() {
		t.Fatal("a superseded client must quarantine")
	}
	if got := m.gate.Genesis(); !bytes.Equal(got, foreignGenesis) {
		t.Fatalf("gate genesis = %x, want %x", got, foreignGenesis)
	}
}

// A client whose own root is still live refuses the foreign chain without going dark
// — that is what its pin is for.
func TestEnforcingClientIgnoresForeignGenesis(t *testing.T) {
	own, ownGenesis := genesisChainForTest(t)
	foreign, _ := genesisChainForTest(t)
	ch := make(chan []byte, 1)
	ch <- foreign

	m, err := NewE2EClientWithIdentity(trustGatewayConn(t, ch), mustKP(t), ownGenesis, "")
	if err != nil {
		t.Fatalf("NewE2EClientWithIdentity: %v", err)
	}
	defer m.Close()
	if _, err := m.trust.Ingest(own); err != nil {
		t.Fatalf("seed own chain: %v", err)
	}
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	m.syncTrustLog()

	if m.Quarantined() {
		t.Fatal("an enforcing client must not quarantine on a foreign chain")
	}
}
