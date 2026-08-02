package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net"
	"os"
	"strings"
	"sync"
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
// nodes.list (empty) and trustlog.sync with the latest chain sent on chain.
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
						return api.TrustLogSyncResult{}, nil
					}
					entries, err := trustlog.UnmarshalChain(cur)
					if err != nil || len(entries) == 0 {
						return api.TrustLogSyncResult{}, nil
					}
					head := trustlog.HashEntry(&entries[len(entries)-1])

					known := make(map[string]bool, len(params.Known))
					for _, k := range params.Known {
						known[string(k)] = true
					}
					if known[string(head)] {
						return api.TrustLogSyncResult{}, nil
					}
					chainEntries, err := trustlog.ChainEntries(cur)
					if err != nil {
						return api.TrustLogSyncResult{}, nil
					}
					stats.mu.Lock()
					stats.chainsServedCnt++
					stats.mu.Unlock()
					return api.TrustLogSyncResult{Entries: chainEntries}, nil
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
			var (
				params any
				result any
			)
			switch method {
			case api.MethodTrustLogSync:
				var p api.TrustLogSyncParams
				_ = json.Unmarshal(raw, &p)
				params = p
				result = &api.TrustLogSyncResult{}
			default:
				params = raw
				result = &struct{}{}
			}
			if err := fn(method, params, result); err != nil {
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

// TestClientSyncTrustChainsOrphanNoRetry covers the case where the gateway
// serves an entry whose ancestor is absent. After the retention fix the client
// no longer retries with nil heads; it issues exactly one sync and returns
// whatever assembles (nothing, if the ancestor was never retained).
func TestClientSyncTrustChainsOrphanNoRetry(t *testing.T) {
	signer, _ := trustlog.GenerateSigner()
	tlog, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	genesis := tlog.Tip()
	_ = tlog.AuthorizeDevice(bytes.Repeat([]byte{0x33}, 32), signer)
	fullChain := trustlog.MarshalChain(tlog.Entries())

	m, fullRaw := clientWithChain(t, fullChain, genesis)
	if len(fullRaw) < 2 {
		t.Fatalf("need ≥2 entries")
	}
	orphan := fullRaw[1] // has Prev pointing to genesis, but genesis not in merged

	calls := 0
	m.peer = fakePeerFunc(func(method string, params, result any) error {
		if method != api.MethodTrustLogSync {
			return nil
		}
		calls++
		result.(*api.TrustLogSyncResult).Entries = [][]byte{orphan}
		return nil
	})

	_, ok := m.syncTrustChains()
	if !ok {
		t.Fatalf("syncTrustChains reported failure")
	}
	if calls != 1 {
		t.Fatalf("orphan entry must not trigger a retry: expected 1 sync, got %d", calls)
	}
}

// TestClientSyncTrustChainsRetainsRejectedBranch covers the loss scenario: the
// client received branch Y (a fork that lost fork-choice to X), recorded its
// head via rememberHead, then did not ingest it. The gateway later serves only
// an extension entry D whose Prev points to the tip of Y. Without retention D
// cannot be placed. After the fix, rememberHead retains Y's raw entries so
// assembly of [genesis, devY, D] succeeds.
func TestClientSyncTrustChainsRetainsRejectedBranch(t *testing.T) {
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	genLog, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesis := genLog.Tip()
	genesisEntries := genLog.Entries()

	logX, _ := trustlog.Load(genesisEntries)
	_ = logX.AuthorizeDevice(bytes.Repeat([]byte{0xAA}, 32), signer)
	chainX := trustlog.MarshalChain(logX.Entries())

	logY, _ := trustlog.Load(genesisEntries)
	_ = logY.AuthorizeDevice(bytes.Repeat([]byte{0xBB}, 32), signer)
	chainY := trustlog.MarshalChain(logY.Entries())

	// D extends Y — the gateway serves only this entry.
	logD, _ := trustlog.Load(logY.Entries())
	_ = logD.AuthorizeDevice(bytes.Repeat([]byte{0xCC}, 32), signer)
	rawD, err := trustlog.ChainEntries(trustlog.MarshalChain(logD.Entries()))
	if err != nil {
		t.Fatalf("ChainEntries D: %v", err)
	}
	dEntry := rawD[len(rawD)-1]

	m, _ := clientWithChain(t, chainX, genesis)
	if _, err := m.trust.Ingest(chainX); err != nil {
		t.Fatalf("Ingest X: %v", err)
	}
	// Simulate receiving-and-rejecting Y: retain its raw entries directly without
	// ingesting them into the trust store (fork-choice loser).
	rawY, err := trustlog.ChainEntries(chainY)
	if err != nil {
		t.Fatalf("ChainEntries Y: %v", err)
	}
	m.mu.Lock()
	if m.retainedEntries == nil {
		m.retainedEntries = trustlog.NewEntryStore()
	}
	reY := m.retainedEntries
	m.mu.Unlock()
	if _, refused := reY.PutAll(rawY); refused > 0 {
		t.Fatalf("PutAll Y refused %d entries", refused)
	}

	m.peer = fakePeerFunc(func(method string, params, result any) error {
		if method == api.MethodTrustLogSync {
			result.(*api.TrustLogSyncResult).Entries = [][]byte{dEntry}
		}
		return nil
	})

	chains, ok := m.syncTrustChains()
	if !ok {
		t.Fatalf("syncTrustChains reported failure")
	}
	found := false
	for _, c := range chains {
		if entries, err := trustlog.ChainEntries(c); err == nil && len(entries) >= 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("extension of rejected branch Y was lost: no assembled chain of length ≥3")
	}
}

func clientForkChainForTest(t *testing.T) []byte {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	tl, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	if err := tl.AuthorizeDevice(bytes.Repeat([]byte{0xBB}, 32), signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	return trustlog.MarshalChain(tl.Entries())
}

func TestClientOffersEveryRetainedHash(t *testing.T) {
	m, chain := clientWithTrustStore(t)

	var got api.TrustLogSyncParams
	m.peer = fakePeerFunc(func(method string, params, result any) error {
		if method == api.MethodTrustLogSync {
			got = params.(api.TrustLogSyncParams)
		}
		return nil
	})
	m.syncTrustChains()

	want, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	if len(got.Known) != len(want) {
		t.Fatalf("offered %d hashes, want %d", len(got.Known), len(want))
	}
}

func TestClientDoesNotRefetchARejectedBranch(t *testing.T) {
	m, _ := clientWithTrustStore(t)
	rejected := clientForkChainForTest(t)

	calls := 0
	var second api.TrustLogSyncParams
	m.peer = fakePeerFunc(func(method string, params, result any) error {
		if method != api.MethodTrustLogSync {
			return nil
		}
		calls++
		if calls == 1 {
			raw, err := trustlog.ChainEntries(rejected)
			if err != nil {
				t.Fatalf("ChainEntries: %v", err)
			}
			result.(*api.TrustLogSyncResult).Entries = raw
			return nil
		}
		second = params.(api.TrustLogSyncParams)
		return nil
	})

	m.syncTrustChains()
	m.syncTrustChains()

	raw, err := trustlog.ChainEntries(rejected)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	offered := map[string]bool{}
	for _, h := range second.Known {
		offered[string(h)] = true
	}
	for _, r := range raw {
		e, uerr := trustlog.UnmarshalEntry(r)
		if uerr != nil {
			t.Fatalf("UnmarshalEntry: %v", uerr)
		}
		if !offered[string(trustlog.HashEntry(&e))] {
			t.Fatalf("second offer omitted a rejected-branch entry")
		}
	}
}

// TestClientSyncTrustChainsIssuesExactlyOneSync is a regression guard: a
// complete, fully-connected chain must resolve in exactly one sync with no
// recovery re-sync.
func TestClientSyncTrustChainsIssuesExactlyOneSync(t *testing.T) {
	signer, _ := trustlog.GenerateSigner()
	tlog, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	genesis := tlog.Tip()
	_ = tlog.AuthorizeDevice(bytes.Repeat([]byte{0x55}, 32), signer)
	fullChain := trustlog.MarshalChain(tlog.Entries())

	m, fullRaw := clientWithChain(t, fullChain, genesis)

	calls := 0
	m.peer = fakePeerFunc(func(method string, params, result any) error {
		if method != api.MethodTrustLogSync {
			return nil
		}
		calls++
		result.(*api.TrustLogSyncResult).Entries = fullRaw
		return nil
	})

	chains, ok := m.syncTrustChains()
	if !ok {
		t.Fatalf("syncTrustChains reported failure")
	}
	if calls != 1 {
		t.Fatalf("complete chain must issue exactly 1 sync, got %d", calls)
	}
	if len(chains) != 1 || !bytes.Equal(chains[0], fullChain) {
		t.Fatalf("assembled chain does not match original")
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

// orphanGateway serves a fixed list of raw entries (orphans) on trustlog.sync,
// simulating a gateway that holds entries whose ancestors were pruned.
func orphanGateway(t *testing.T, entries [][]byte) *api.Peer {
	t.Helper()
	return fakePeerFunc(func(method string, params, result any) error {
		if method == api.MethodTrustLogSync {
			result.(*api.TrustLogSyncResult).Entries = entries
		}
		return nil
	})
}

func TestUnplacedWarningSuppressedWhenUnchanged(t *testing.T) {
	// Orphan entries: a chain with genesis stripped.
	signer, _ := trustlog.GenerateSigner()
	tl, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	dev := bytes.Repeat([]byte{0xCC}, 32)
	_ = tl.AuthorizeDevice(dev, signer)
	full := trustlog.MarshalChain(tl.Entries())
	all, _ := trustlog.UnmarshalChain(full)
	orphans, _ := trustlog.ChainEntries(trustlog.MarshalChain(all[1:])) // 1 orphan (no genesis)

	_, genesis := genesisChainForTest(t)
	srvConn, cliConn := net.Pipe()
	m, err := NewE2EClientWithGenesis(cliConn, genesis)
	if err != nil {
		srvConn.Close()
		cliConn.Close()
		t.Fatalf("NewE2EClientWithGenesis: %v", err)
	}
	defer m.Close()
	defer srvConn.Close()

	captureLog := func(fn func()) string {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)
		fn()
		return buf.String()
	}

	// First call: unplaced count changes 0→1; warning must appear.
	m.peer = orphanGateway(t, orphans)
	out := captureLog(func() { m.syncTrustChains() })
	if !strings.Contains(out, "unplaced") {
		t.Fatal("first call should log the unplaced warning")
	}

	// Second call: same count; warning must NOT repeat.
	m.peer = orphanGateway(t, orphans)
	out = captureLog(func() { m.syncTrustChains() })
	if strings.Contains(out, "unplaced") {
		t.Fatal("second call with identical unplaced count must not log")
	}

	// Sync with no orphans: count returns to 0, memory resets.
	m.peer = orphanGateway(t, nil)
	captureLog(func() { m.syncTrustChains() })

	// Recurrence: same orphan count as the first wave; must warn again.
	m.peer = orphanGateway(t, orphans)
	out = captureLog(func() { m.syncTrustChains() })
	if !strings.Contains(out, "unplaced") {
		t.Fatal("recurrence after zero must log again")
	}
}
