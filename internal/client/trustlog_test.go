package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/blake2s"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

type gatewayStats struct {
	mu              sync.Mutex
	lastKnownLen    int
	chainsServedCnt int
	pullsCnt        int
	peer            *api.Peer // the stub gateway's peer, for pushing node.event
}

// emitBeacon pushes a NodeEventBeacon carrying nd, the only way a beacon reaches a
// running client.
func (s *gatewayStats) emitBeacon(nd api.NodeDescriptor) {
	_ = s.peer.Notify(api.MethodNodeEvent, api.NodeEvent{Type: api.NodeEventBeacon, Node: nd})
}

func (s *gatewayStats) lastKnownCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastKnownLen
}

func (s *gatewayStats) chainsServed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chainsServedCnt
}

func (s *gatewayStats) pulls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pullsCnt
}

// trustGatewayConn is one end of a net.Pipe running a minimal gateway that answers
// nodes.list (empty) and trustlog.pull with the latest chain sent on chain.
func trustGatewayConn(t *testing.T, chain <-chan []byte) net.Conn {
	conn, _ := trustGatewayConnWithStats(t, chain)
	return conn
}

// trustGatewayConnWithStats is trustGatewayConn extended to record pull statistics
// and honour the Known fingerprints: branches already known to the caller are
// withheld, matching the real gateway's conditional-diff behaviour.
func trustGatewayConnWithStats(t *testing.T, chain <-chan []byte) (net.Conn, *gatewayStats) {
	t.Helper()
	srvConn, cliConn := net.Pipe()
	var mu sync.Mutex
	var current []byte
	var seenFPs atomic.Value // holds map[[32]byte]bool
	stats := &gatewayStats{}
	{
		peer := api.NewPeer(srvConn, api.PeerOptions{
			Dispatch: func(_ context.Context, method string, raw json.RawMessage) (any, error) {
				switch method {
				case api.MethodNodesList:
					return api.NodesListResult{Nodes: []api.NodeDescriptor{}}, nil
				case api.MethodTrustLogPull:
					var params api.TrustLogPullParams
					_ = json.Unmarshal(raw, &params)

					stats.mu.Lock()
					stats.lastKnownLen = len(params.Known)
					stats.pullsCnt++
					stats.mu.Unlock()

					mu.Lock()
					select {
					case c := <-chain:
						current = c
					default:
					}
					cur := current
					mu.Unlock()

					if cur == nil {
						return api.TrustLogPullResult{}, nil
					}
					fp := blake2s.Sum256(cur)
					var known map[[32]byte]bool
					if v := seenFPs.Load(); v != nil {
						known = v.(map[[32]byte]bool)
					}
					for _, k := range params.Known {
						if len(k) == 32 {
							var kfp [32]byte
							copy(kfp[:], k)
							if known == nil {
								known = map[[32]byte]bool{}
							}
							known[kfp] = true
						}
					}
					seenFPs.Store(known)
					if known[fp] {
						return api.TrustLogPullResult{}, nil
					}
					stats.mu.Lock()
					stats.chainsServedCnt++
					stats.mu.Unlock()
					return api.TrustLogPullResult{Chains: [][]byte{cur}}, nil
				}
				return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: method}
			},
		})
		stats.peer = peer
		t.Cleanup(func() { peer.Close() })
	}
	return cliConn, stats
}

func TestClientPullsAndReSyncsTrustLog(t *testing.T) {
	clientTrustSyncInterval.Store(int64(20 * time.Millisecond))
	t.Cleanup(func() { clientTrustSyncInterval.Store(int64(5 * time.Minute)) })

	// Genesis-only chain first; an authorize appended later.
	signer, _ := trustlog.GenerateSigner()
	log, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	head := log.Tip()
	genChain := trustlog.MarshalChain(log.Entries())
	device := bytes.Repeat([]byte{0x22}, 32)

	chainCh := make(chan []byte, 4)
	chainCh <- genChain
	conn := trustGatewayConn(t, chainCh)

	c, err := NewE2EClientWithGenesis(conn, head)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	waitClient(t, "genesis synced", func() bool { return c.TrustTip() != nil })
	if c.DeviceAuthorized(device) {
		t.Fatal("device not yet authorized")
	}

	// Append an authorize and let the periodic pull pick it up.
	_ = log.AuthorizeDevice(device, signer)
	chainCh <- trustlog.MarshalChain(log.Entries())
	waitClient(t, "device authorized after re-sync", func() bool { return c.DeviceAuthorized(device) })
}

func waitClient(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestClientDoesNotRefetchAKnownBranch(t *testing.T) {
	clientTrustSyncInterval.Store(int64(20 * time.Millisecond))
	t.Cleanup(func() { clientTrustSyncInterval.Store(int64(5 * time.Minute)) })

	signer, _ := trustlog.GenerateSigner()
	log, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	chain := trustlog.MarshalChain(log.Entries())

	ch := make(chan []byte, 1)
	ch <- chain
	conn, stats := trustGatewayConnWithStats(t, ch)

	m, err := NewE2EClientWithIdentity(conn, mustKP(t), log.Tip(), "")
	if err != nil {
		t.Fatalf("NewE2EClientWithIdentity: %v", err)
	}
	defer m.Close()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	m.syncTrustLog() // a second, explicit sync

	if got := stats.lastKnownCount(); got == 0 {
		t.Fatal("the client must send the fingerprints it already holds")
	}
	if got := stats.chainsServed(); got > 1 {
		t.Fatalf("chains served = %d, want the branch sent at most once", got)
	}
}

// descriptorWithBeaconForTest builds an api.NodeDescriptor with a validly-signed
// beacon carrying the given tip, using fresh throwaway keys.
func descriptorWithBeaconForTest(t *testing.T, tip []byte) api.NodeDescriptor {
	t.Helper()
	nodeKey := mustKP(t)
	bPub, bPriv := genBeaconKey(t)
	b := api.SignBeacon(bPriv, bPub, tip, 1, 1)
	return api.NodeDescriptor{
		IdentityPubKey: base64.StdEncoding.EncodeToString(nodeKey.Public),
		BeaconPubKey:   base64.StdEncoding.EncodeToString(bPub),
		Beacon:         &b,
	}
}

func TestBeaconWithUnknownTipTriggersAPull(t *testing.T) {
	clientTrustSyncInterval.Store(int64(10 * time.Minute)) // prove the timer is not what pulled
	t.Cleanup(func() { clientTrustSyncInterval.Store(int64(5 * time.Minute)) })

	signer, _ := trustlog.GenerateSigner()
	log, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	chain := trustlog.MarshalChain(log.Entries())

	ch := make(chan []byte, 1)
	ch <- chain
	conn, stats := trustGatewayConnWithStats(t, ch)
	m, err := NewE2EClientWithIdentity(conn, mustKP(t), log.Tip(), "")
	if err != nil {
		t.Fatalf("NewE2EClientWithIdentity: %v", err)
	}
	defer m.Close()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	before := stats.pulls()

	// A beacon announcing a tip this client has never ingested.
	stats.emitBeacon(descriptorWithBeaconForTest(t, bytes.Repeat([]byte{0xAB}, 32)))

	waitClient(t, "triggered pull issued", func() bool {
		return stats.pulls() > before
	})
}

// TestUnpinnedClientQuarantinesOnBeaconArrival is the client half of the event
// path the 5-minute backstop was justified by. An unpinned client holds no trust
// store, which is exactly the case the pull trigger used to bail on, so a locked
// network left it talking to unverified nodes for a whole backstop.
func TestUnpinnedClientQuarantinesOnBeaconArrival(t *testing.T) {
	clientTrustSyncInterval.Store(int64(10 * time.Minute)) // prove the backstop is not what tripped it
	t.Cleanup(func() { clientTrustSyncInterval.Store(int64(5 * time.Minute)) })

	chain, _ := genesisChainForTest(t)
	ch := make(chan []byte, 1)
	conn, stats := trustGatewayConnWithStats(t, ch)

	m, err := NewE2EClientWithIdentity(conn, mustKP(t), nil, "")
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

	ch <- chain // the network gets locked under a running unpinned client
	stats.emitBeacon(descriptorWithBeaconForTest(t, bytes.Repeat([]byte{0xCD}, 32)))

	waitClient(t, "unpinned client quarantines from the beacon event", m.Quarantined)
}
