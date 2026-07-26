package client

import (
	"bytes"
	"testing"
	"time"

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
	t.Cleanup(func() { clientTrustSyncInterval.Store(int64(30 * time.Second)) })

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
