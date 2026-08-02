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

// trustGatewayConnWithStats is trustGatewayConn extended to record sync statistics
// and honour the Heads set: branches whose head the caller already holds are withheld,
// matching the real gateway's conditional-diff behaviour.
func trustGatewayConnWithStats(t *testing.T, chain <-chan []byte) (net.Conn, *gatewayStats) {
	t.Helper()
	srvConn, cliConn := net.Pipe()
	var mu sync.Mutex
	var current []byte
	var seenHeads atomic.Value // holds map[[32]byte]bool
	stats := &gatewayStats{}
	{
		peer := api.NewPeer(srvConn, api.PeerOptions{
			Dispatch: func(_ context.Context, method string, raw json.RawMessage) (any, error) {
				switch method {
				case api.MethodNodesList:
					return api.NodesListResult{Nodes: []api.NodeDescriptor{}}, nil
				case api.MethodTrustLogSync:
					var params api.TrustLogSyncParams
					_ = json.Unmarshal(raw, &params)

					stats.mu.Lock()
					stats.lastKnownLen = len(params.Heads)
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
						return api.TrustLogSyncResult{}, nil
					}
					entries, err := trustlog.UnmarshalChain(cur)
					if err != nil || len(entries) == 0 {
						return api.TrustLogSyncResult{}, nil
					}
					var head [32]byte
					copy(head[:], trustlog.HashEntry(&entries[len(entries)-1]))

					var known map[[32]byte]bool
					if v := seenHeads.Load(); v != nil {
						known = v.(map[[32]byte]bool)
					}
					for _, k := range params.Heads {
						if len(k) == 32 {
							var kh [32]byte
							copy(kh[:], k)
							if known == nil {
								known = map[[32]byte]bool{}
							}
							known[kh] = true
						}
					}
					seenHeads.Store(known)
					if known[head] {
						return api.TrustLogSyncResult{}, nil
					}
					raw, err := trustlog.ChainEntries(cur)
					if err != nil {
						return api.TrustLogSyncResult{}, nil
					}
					stats.mu.Lock()
					stats.chainsServedCnt++
					stats.mu.Unlock()
					return api.TrustLogSyncResult{Entries: raw}, nil
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

func clientWithTrustStore(t *testing.T) (*E2EClient, []byte) {
	t.Helper()
	chain, genesis := genesisChainForTest(t)
	srvConn, cliConn := net.Pipe()
	m, err := NewE2EClientWithGenesis(cliConn, genesis)
	if err != nil {
		srvConn.Close()
		cliConn.Close()
		t.Fatalf("NewE2EClientWithGenesis: %v", err)
	}
	origPeer := m.peer
	t.Cleanup(func() {
		m.Close()
		origPeer.Close()
		srvConn.Close()
	})
	if _, err := m.trust.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	return m, chain
}

func fakePeerFunc(fn func(method string, params, result any) error) *api.Peer {
	srvConn, cliConn := net.Pipe()
	api.NewPeer(srvConn, api.PeerOptions{
		Dispatch: func(_ context.Context, method string, raw json.RawMessage) (any, error) {
			var result any
			switch method {
			case api.MethodTrustLogSync:
				result = &api.TrustLogSyncResult{}
			default:
				result = &struct{}{}
			}
			if err := fn(method, raw, result); err != nil {
				return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
			}
			return result, nil
		},
	})
	return api.NewPeer(cliConn, api.PeerOptions{})
}

func TestClientSyncTrustChainsAssemblesEntries(t *testing.T) {
	m, chain := clientWithTrustStore(t)

	raw, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}

	pushed := false
	m.peer = fakePeerFunc(func(method string, params, result any) error {
		switch method {
		case api.MethodTrustLogSync:
			res := result.(*api.TrustLogSyncResult)
			res.Entries = raw
			res.Want = [][]byte{{1, 2, 3}} // the client must ignore this
		case api.MethodTrustLogPush:
			pushed = true
		}
		return nil
	})

	chains, ok := m.syncTrustChains()
	if !ok {
		t.Fatalf("syncTrustChains reported failure")
	}
	if len(chains) != 1 || !bytes.Equal(chains[0], chain) {
		t.Fatalf("assembled %d chains, want the original", len(chains))
	}
	if pushed {
		t.Fatalf("the client must never push trust state")
	}
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

// clientWithChain builds a client pinned to the given genesis chain and returns
// it alongside the full raw entries. The original peer is replaced on return so
// callers can substitute a fake; the teardown closes both connections.
func clientWithChain(t *testing.T, fullChain []byte, genesis []byte) (*E2EClient, [][]byte) {
	t.Helper()
	fullRaw, err := trustlog.ChainEntries(fullChain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	srvConn, cliConn := net.Pipe()
	m, err := NewE2EClientWithGenesis(cliConn, genesis)
	if err != nil {
		srvConn.Close()
		cliConn.Close()
		t.Fatalf("NewE2EClientWithGenesis: %v", err)
	}
	origPeer := m.peer
	t.Cleanup(func() {
		m.Close()
		origPeer.Close()
		srvConn.Close()
	})
	return m, fullRaw
}

// TestClientSyncTrustChainsRetiesOnUnplacedEntries covers the case where the
// gateway withholds ancestors because the client advertised heads for branches
// whose entries it already discarded. On the first sync the gateway returns only
// an orphaned tip; the client must clear its branch cache and retry once with nil
// heads so the gateway sends the full ancestry.
func TestClientSyncTrustChainsRetiesOnUnplacedEntries(t *testing.T) {
	// Build a two-entry chain: genesis + authorize.
	signer, _ := trustlog.GenerateSigner()
	tlog, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	genesis := tlog.Tip()
	_ = tlog.AuthorizeDevice(bytes.Repeat([]byte{0x33}, 32), signer)
	fullChain := trustlog.MarshalChain(tlog.Entries())

	m, fullRaw := clientWithChain(t, fullChain, genesis)
	if len(fullRaw) < 2 {
		t.Fatalf("need ≥2 entries")
	}
	orphan := fullRaw[1] // has Prev pointing to genesis, but genesis missing

	calls := 0
	var lastHeads [][]byte
	m.peer = fakePeerFunc(func(method string, params, result any) error {
		if method != api.MethodTrustLogSync {
			return nil
		}
		calls++
		var p api.TrustLogSyncParams
		if raw, ok := params.(json.RawMessage); ok {
			_ = json.Unmarshal(raw, &p)
		}
		lastHeads = p.Heads
		res := result.(*api.TrustLogSyncResult)
		if calls == 1 {
			res.Entries = [][]byte{orphan}
		} else {
			res.Entries = fullRaw
		}
		return nil
	})

	// Plant a fake head so knownHeads() is non-empty.
	m.mu.Lock()
	m.seenBranches[[32]byte{0: 1}] = true
	m.mu.Unlock()

	chains, ok := m.syncTrustChains()
	if !ok {
		t.Fatalf("syncTrustChains reported failure")
	}
	if calls != 2 {
		t.Fatalf("expected 2 syncs (initial + retry), got %d", calls)
	}
	if len(lastHeads) != 0 {
		t.Fatalf("retry must carry empty Heads, got %d heads", len(lastHeads))
	}
	if len(chains) != 1 || !bytes.Equal(chains[0], fullChain) {
		t.Fatalf("assembled chain does not match original after retry")
	}
}

// TestClientSyncTrustChainsNoRetryOnHappyPath asserts that when all returned
// entries assemble cleanly, exactly one sync is issued — the retry must not fire.
func TestClientSyncTrustChainsNoRetryOnHappyPath(t *testing.T) {
	signer, _ := trustlog.GenerateSigner()
	tlog, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	genesis := tlog.Tip()
	_ = tlog.AuthorizeDevice(bytes.Repeat([]byte{0x44}, 32), signer)
	fullChain := trustlog.MarshalChain(tlog.Entries())

	m, fullRaw := clientWithChain(t, fullChain, genesis)

	calls := 0
	m.peer = fakePeerFunc(func(method string, params, result any) error {
		if method != api.MethodTrustLogSync {
			return nil
		}
		calls++
		res := result.(*api.TrustLogSyncResult)
		res.Entries = fullRaw
		return nil
	})

	// Plant a fake head so knownHeads() is non-empty.
	m.mu.Lock()
	m.seenBranches[[32]byte{0: 1}] = true
	m.mu.Unlock()

	chains, ok := m.syncTrustChains()
	if !ok {
		t.Fatalf("syncTrustChains reported failure")
	}
	if calls != 1 {
		t.Fatalf("happy path must issue exactly 1 sync, got %d", calls)
	}
	if len(chains) != 1 || !bytes.Equal(chains[0], fullChain) {
		t.Fatalf("assembled chain does not match original")
	}
}
