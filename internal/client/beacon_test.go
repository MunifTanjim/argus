package client

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// genBeaconKey returns a fresh Ed25519 beacon keypair for test use.
func genBeaconKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	return pub, priv
}

// TestBuildChainHashSet verifies that buildChainHashSet produces the correct set
// of entry hashes, returns nil for an empty input, and errors on corrupt bytes.
func TestBuildChainHashSet(t *testing.T) {
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	_ = lg.AuthorizeDevice(bytes.Repeat([]byte{0x11}, 32), signer)
	entries := lg.Entries()
	chain := trustlog.MarshalChain(entries)
	chainTip := trustlog.HashEntry(&entries[len(entries)-1])
	genesisHash := trustlog.HashEntry(&entries[0])

	known, err := buildChainHashSet(chain)
	if err != nil {
		t.Fatalf("buildChainHashSet: %v", err)
	}
	if !known[string(chainTip)] {
		t.Error("chain tip must be in hash set")
	}
	if !known[string(genesisHash)] {
		t.Error("genesis hash must be in hash set")
	}

	// Empty input → nil set, no error.
	known, err = buildChainHashSet(nil)
	if err != nil || known != nil {
		t.Fatalf("empty input: want (nil, nil), got (%v, %v)", known, err)
	}

	// Corrupt bytes → error.
	_, err = buildChainHashSet([]byte("garbage"))
	if err == nil {
		t.Fatal("corrupt bytes must return an error")
	}
}

// TestConsistentTips verifies the pure chain-history check function.
func TestConsistentTips(t *testing.T) {
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	_ = lg.AuthorizeDevice(bytes.Repeat([]byte{0x11}, 32), signer)
	entries := lg.Entries()
	chain := trustlog.MarshalChain(entries)
	chainTip := trustlog.HashEntry(&entries[len(entries)-1])
	genesisHash := trustlog.HashEntry(&entries[0])

	known, err := buildChainHashSet(chain)
	if err != nil {
		t.Fatalf("buildChainHashSet: %v", err)
	}

	// Both tips present in the linear history → consistent.
	ok, detail := consistentTips(map[string]api.Beacon{
		"node1": {Tip: chainTip},
		"node2": {Tip: genesisHash},
	}, known)
	if !ok {
		t.Fatalf("expected consistent for tips in history; detail: %s", detail)
	}

	// One tip not in the chain history → inconsistent.
	ok, detail = consistentTips(map[string]api.Beacon{
		"node1": {Tip: chainTip},
		"node2": {Tip: bytes.Repeat([]byte{0xde}, 32)},
	}, known)
	if ok {
		t.Fatal("expected inconsistent for a divergent tip")
	}
	if detail == "" {
		t.Fatal("inconsistency detail must be non-empty")
	}

	// Nil known → consistent (no chain to compare against yet).
	ok, _ = consistentTips(map[string]api.Beacon{
		"n": {Tip: bytes.Repeat([]byte{0xde}, 32)},
	}, nil)
	if !ok {
		t.Fatal("nil known must yield consistent (nothing to compare)")
	}

	// Nil beacon Tip → skipped (node has no chain yet; not an equivocation).
	ok, _ = consistentTips(map[string]api.Beacon{
		"n": {Tip: nil},
	}, known)
	if !ok {
		t.Fatal("nil beacon Tip must be skipped and treated as consistent")
	}
}

// TestCheckBeaconConsistencySkipsOnParseFailure verifies that checkBeaconConsistency
// is a no-op (leaves miss state and flag untouched) when the chain bytes are
// unparseable, rather than falsely flagging or resetting miss streaks.
// Uses checkBeaconConsistencyWithChain to inject corrupt bytes directly.
func TestCheckBeaconConsistencySkipsOnParseFailure(t *testing.T) {
	nodeKey := mustKP(t)
	bPub, bPriv := genBeaconKey(t)
	divergentTip := bytes.Repeat([]byte{0xde}, 32)
	b := api.SignBeacon(bPriv, bPub, divergentTip, 1, 1)
	key := string(nodeKey.Public)

	c := &E2EClient{
		byNode:        map[string]*nodeChan{},
		byChanID:      map[string]*nodeChan{},
		pending:       map[uint64]chan pendingReply{},
		subNode:       map[string]string{},
		termNode:      map[string]string{},
		beacons:       map[string]api.Beacon{key: b},
		beaconCtr:     map[string]uint64{key: b.Counter},
		beaconMiss:    map[string]*beaconMissState{key: {tip: divergentTip, misses: 1}},
		everConnected: map[string]bool{},
	}

	// Pre-condition: miss streak = 1, equivocation not set.
	preMisses := c.beaconMiss[key].misses
	if preMisses != 1 {
		t.Fatalf("pre-condition: miss streak = %d, want 1", preMisses)
	}

	// Call with unparseable chain bytes; must be a no-op.
	c.checkBeaconConsistencyWithChain([]byte("not-valid-chain-bytes"), nil)

	c.mu.Lock()
	postMiss := c.beaconMiss[key]
	flag := c.equivocation
	c.mu.Unlock()

	if flag {
		t.Fatal("equivocation must NOT be set when chain bytes are unparseable")
	}
	if postMiss == nil || postMiss.misses != preMisses {
		t.Fatalf("miss streak must be unchanged on parse failure: got misses=%v, want %d", postMiss, preMisses)
	}
}

// TestBeaconCounterGuard verifies stale and equal-counter beacons are dropped.
func TestBeaconCounterGuard(t *testing.T) {
	nodeKey := mustKP(t)
	bPub, bPriv := genBeaconKey(t)
	tip := bytes.Repeat([]byte{0xaa}, 32)

	makeND := func(counter uint64) api.NodeDescriptor {
		b := api.SignBeacon(bPriv, bPub, tip, 1, counter)
		return api.NodeDescriptor{
			IdentityPubKey: base64.StdEncoding.EncodeToString(nodeKey.Public),
			BeaconPubKey:   base64.StdEncoding.EncodeToString(bPub),
			Beacon:         &b,
		}
	}

	m := &E2EClient{beacons: map[string]api.Beacon{}, beaconCtr: map[string]uint64{}}
	key := string(nodeKey.Public)

	m.ingestBeaconFromDescriptor(makeND(5))
	if got := m.beaconCtr[key]; got != 5 {
		t.Fatalf("initial counter: want 5, got %d", got)
	}

	// Stale: counter 3 < 5 → ignored.
	m.ingestBeaconFromDescriptor(makeND(3))
	if got := m.beaconCtr[key]; got != 5 {
		t.Fatalf("stale beacon (3<5) must be ignored; counter now %d", got)
	}

	// Equal: counter 5 == 5 → ignored (not strictly greater).
	m.ingestBeaconFromDescriptor(makeND(5))
	if got := m.beaconCtr[key]; got != 5 {
		t.Fatalf("equal-counter beacon (5==5) must be ignored; counter now %d", got)
	}

	// Fresh: counter 6 > 5 → accepted.
	m.ingestBeaconFromDescriptor(makeND(6))
	if got := m.beaconCtr[key]; got != 6 {
		t.Fatalf("fresh beacon (6>5) must be accepted; counter now %d", got)
	}
}

// TestBeaconVerifyGuard verifies that unverifiable and wrong-attributed beacons
// are silently dropped (not flagged as equivocation).
func TestBeaconVerifyGuard(t *testing.T) {
	nodeKey := mustKP(t)
	bPub, bPriv := genBeaconKey(t)
	otherPub, _ := genBeaconKey(t)
	tip := bytes.Repeat([]byte{0xbb}, 32)

	identB64 := base64.StdEncoding.EncodeToString(nodeKey.Public)
	bPubB64 := base64.StdEncoding.EncodeToString(bPub)
	otherPubB64 := base64.StdEncoding.EncodeToString(otherPub)

	m := &E2EClient{beacons: map[string]api.Beacon{}, beaconCtr: map[string]uint64{}}

	// Nil Beacon → no-op.
	m.ingestBeaconFromDescriptor(api.NodeDescriptor{
		IdentityPubKey: identB64, BeaconPubKey: bPubB64, Beacon: nil,
	})
	if len(m.beacons) != 0 {
		t.Fatal("nil Beacon must be ignored")
	}

	// Missing IdentityPubKey → no-op.
	good := api.SignBeacon(bPriv, bPub, tip, 1, 1)
	m.ingestBeaconFromDescriptor(api.NodeDescriptor{
		BeaconPubKey: bPubB64, Beacon: &good,
	})
	if len(m.beacons) != 0 {
		t.Fatal("missing IdentityPubKey must be rejected")
	}

	// Tampered sig (zeroed) → rejected.
	tampered := api.SignBeacon(bPriv, bPub, tip, 1, 2)
	tampered.Sig = make([]byte, len(tampered.Sig)) // zero out the signature
	m.ingestBeaconFromDescriptor(api.NodeDescriptor{
		IdentityPubKey: identB64, BeaconPubKey: bPubB64, Beacon: &tampered,
	})
	if len(m.beacons) != 0 {
		t.Fatal("tampered (zeroed sig) beacon must be rejected by VerifyBeacon")
	}

	// BeaconPub in beacon != roster-announced BeaconPubKey → rejected.
	mismatch := api.SignBeacon(bPriv, bPub, tip, 1, 3)
	// roster says otherPub, but beacon was signed with bPriv (claims bPub)
	m.ingestBeaconFromDescriptor(api.NodeDescriptor{
		IdentityPubKey: identB64,
		BeaconPubKey:   otherPubB64, // roster-announced key doesn't match beacon's BeaconPub
		Beacon:         &mismatch,
	})
	if len(m.beacons) != 0 {
		t.Fatalf("beacon with mismatched BeaconPub must be rejected; beacons=%d", len(m.beacons))
	}

	// Valid beacon → accepted.
	valid := api.SignBeacon(bPriv, bPub, tip, 1, 4)
	m.ingestBeaconFromDescriptor(api.NodeDescriptor{
		IdentityPubKey: identB64, BeaconPubKey: bPubB64, Beacon: &valid,
	})
	if len(m.beacons) != 1 {
		t.Fatalf("valid beacon must be accepted; beacons=%d", len(m.beacons))
	}
}

// TestClientBeaconCrossCheck tests the end-to-end beacon cross-check: beacons are
// collected via the node.event OnNotify path, and the cross-check runs on the
// syncTrustLog tick. Consistent tips → no flag; divergent tip → flag set + logged.
func TestClientBeaconCrossCheck(t *testing.T) {
	// Use a very long sync interval so the background trustSyncLoop does not fire
	// between our explicit syncTrustLog() calls (which would add spurious miss ticks).
	SetTrustSyncIntervalForTest(time.Hour)
	t.Cleanup(func() { SetTrustSyncIntervalForTest(30 * time.Second) })

	// Build a 2-entry chain (genesis + authorize-device).
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	head := lg.Tip()
	_ = lg.AuthorizeDevice(bytes.Repeat([]byte{0x55}, 32), signer)
	entries := lg.Entries()
	chain := trustlog.MarshalChain(entries)
	chainTip := trustlog.HashEntry(&entries[len(entries)-1])

	bPub1, bPriv1 := genBeaconKey(t)
	bPub2, bPriv2 := genBeaconKey(t)

	noop := func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return nil, nil, nil
	}
	n1 := &fakeNode{id: "n1", key: mustKP(t), handle: noop}
	n2 := &fakeNode{id: "n2", key: mustKP(t), handle: noop}
	gw, clientConn := newFakeMultiGateway(t, n1, n2)
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

	// sendBeacon delivers a signed beacon for node n via the gateway's node.event path.
	sendBeacon := func(n *fakeNode, bPub ed25519.PublicKey, bPriv ed25519.PrivateKey, tip []byte, counter uint64) {
		b := api.SignBeacon(bPriv, bPub, tip, len(entries), counter)
		_ = gw.peer.Notify(api.MethodNodeEvent, api.NodeEvent{
			Type: api.NodeEventBeacon,
			Node: api.NodeDescriptor{
				ID:             n.id,
				IdentityPubKey: base64.StdEncoding.EncodeToString(n.key.Public),
				BeaconPubKey:   base64.StdEncoding.EncodeToString(bPub),
				Beacon:         &b,
			},
		})
	}

	t.Run("consistent", func(t *testing.T) {
		// Both nodes beacon the chain tip → same linear history → no flag.
		sendBeacon(n1, bPub1, bPriv1, chainTip, 1)
		sendBeacon(n2, bPub2, bPriv2, chainTip, 1)

		waitClient(t, "both beacons ingested", func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			return len(c.beacons) == 2
		})

		// Trigger a sync: pulls chain + cross-checks the collected beacons.
		c.syncTrustLog()
		if c.Equivocation() {
			t.Fatal("consistent beacons (both on chain tip) must not set the equivocation flag")
		}
	})

	t.Run("divergent", func(t *testing.T) {
		// n2 updates its beacon to a tip not in our chain. Equivocation requires
		// beaconMissThreshold consecutive unreconciled ticks for the same tip.
		divergentTip := bytes.Repeat([]byte{0xde}, 32)
		sendBeacon(n2, bPub2, bPriv2, divergentTip, 2) // counter 2 > 1 (fresh)

		waitClient(t, "divergent beacon ingested", func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			b, ok := c.beacons[string(n2.key.Public)]
			return ok && b.Counter == 2
		})

		c.syncTrustLog() // tick 1: miss=1, not yet flagged (could still be propagation lag)
		if c.Equivocation() {
			t.Fatal("first miss must not set equivocation flag (requires 2 consecutive)")
		}
		c.syncTrustLog() // tick 2: same tip still absent → miss=2, flag set
		if !c.Equivocation() {
			t.Fatal("divergent beacon (same tip, 2 consecutive misses) must set the equivocation flag")
		}
	})

	t.Run("flag-persists-on-stale-replay", func(t *testing.T) {
		// A stale beacon (counter ≤ accepted) must be dropped without affecting the
		// miss streak. Even with the flag manually cleared, the stored divergent beacon
		// causes the next sync to re-set the flag — equivocation evidence is permanent.
		c.mu.Lock()
		c.equivocation = false
		c.mu.Unlock()

		// Replay the divergent tip with a stale counter (2 ≤ 2).
		divergentTip := bytes.Repeat([]byte{0xde}, 32)
		sendBeacon(n2, bPub2, bPriv2, divergentTip, 2) // stale: counter 2 == last accepted 2
		// Counter guard: must not update the stored beacon.
		time.Sleep(40 * time.Millisecond) // let the notification be processed
		c.syncTrustLog()
		// The stored beacon (counter=2, divergentTip) is unchanged; the miss streak
		// already exceeds the threshold → flag is re-set on this sync.
		if !c.Equivocation() {
			t.Fatal("flag must remain set once equivocation is detected (evidence persists)")
		}
	})
}

// TestClientPrunesBeaconStateOnChannelDrop verifies that reevaluateChannels also
// deletes beacon state (beacons/beaconCtr/beaconMiss) for the dropped node so a
// revoked node's stale cached beacon cannot accumulate misses or false-positive
// the equivocation flag.
func TestClientPrunesBeaconStateOnChannelDrop(t *testing.T) {
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	head := lg.Tip()
	nodeA := &fakeNode{id: "nodeA", key: mustKP(t)}
	nodeB := &fakeNode{id: "nodeB", key: mustKP(t)}
	_ = lg.AuthorizeDevice(nodeA.key.Public, signer)
	_ = lg.AuthorizeDevice(nodeB.key.Public, signer)
	chain := trustlog.MarshalChain(lg.Entries())
	noop := func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) { return nil, nil, nil }
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

	// Seed beacon state for nodeB (simulates a beacon received and a miss tick).
	nodeBKey := string(nodeB.key.Public)
	divergentTip := bytes.Repeat([]byte{0xde}, 32)
	bPub, bPriv := genBeaconKey(t)
	b := api.SignBeacon(bPriv, bPub, divergentTip, 5, 1)
	c.mu.Lock()
	c.beacons[nodeBKey] = b
	c.beaconCtr[nodeBKey] = b.Counter
	c.beaconMiss[nodeBKey] = &beaconMissState{tip: divergentTip, misses: 1}
	c.mu.Unlock()

	// Revoke nodeB from the trust store and drop the channel.
	if _, err := c.trust.RevokeDevice(nodeB.key.Public, signer); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	c.reevaluateChannels()

	c.mu.Lock()
	_, hasBeacon := c.beacons[nodeBKey]
	_, hasCtr := c.beaconCtr[nodeBKey]
	_, hasMiss := c.beaconMiss[nodeBKey]
	c.mu.Unlock()
	if hasBeacon || hasCtr || hasMiss {
		t.Fatal("reevaluateChannels must prune beacon state for the dropped node")
	}
}

// TestClientPrunesBeaconOnOfflineRemovedEvent verifies that an offline or
// removed node.event notification deletes the node's beacon state from
// beacons/beaconCtr/beaconMiss so the stale tip cannot accumulate misses.
func TestClientPrunesBeaconOnOfflineRemovedEvent(t *testing.T) {
	for _, evType := range []string{api.NodeEventOffline, api.NodeEventRemoved} {
		evType := evType
		t.Run(evType, func(t *testing.T) {
			c := &E2EClient{
				byNode:     map[string]*nodeChan{},
				byChanID:   map[string]*nodeChan{},
				pending:    map[uint64]chan pendingReply{},
				subNode:    map[string]string{},
				termNode:   map[string]string{},
				beacons:    map[string]api.Beacon{},
				beaconCtr:  map[string]uint64{},
				beaconMiss: map[string]*beaconMissState{},
			}

			nodeKey := mustKP(t)
			nodeKeyStr := string(nodeKey.Public)
			identB64 := base64.StdEncoding.EncodeToString(nodeKey.Public)
			divergentTip := bytes.Repeat([]byte{0xde}, 32)
			bPub, bPriv := genBeaconKey(t)
			b := api.SignBeacon(bPriv, bPub, divergentTip, 5, 1)

			// Pre-populate beacon state for the node.
			c.mu.Lock()
			c.beacons[nodeKeyStr] = b
			c.beaconCtr[nodeKeyStr] = b.Counter
			c.beaconMiss[nodeKeyStr] = &beaconMissState{tip: divergentTip, misses: 1}
			c.mu.Unlock()

			evJSON, _ := json.Marshal(api.NodeEvent{
				Type: evType,
				Node: api.NodeDescriptor{ID: "n1", IdentityPubKey: identB64},
			})
			c.onPeerNotify(api.Notification{Method: api.MethodNodeEvent, Params: evJSON})

			c.mu.Lock()
			_, hasBeacon := c.beacons[nodeKeyStr]
			_, hasCtr := c.beaconCtr[nodeKeyStr]
			_, hasMiss := c.beaconMiss[nodeKeyStr]
			c.mu.Unlock()
			if hasBeacon || hasCtr || hasMiss {
				t.Fatalf("%s event must prune beacon state for the node", evType)
			}
		})
	}
}

// TestClientCheckBeaconConsistencySkipsNonConnected verifies the belt-and-
// suspenders guard in checkBeaconConsistency: a beacon for a node that WAS
// connected (had an open channel) but is no longer in byNode (went offline or
// was de-rostered) is skipped and does not accumulate misses, so a legitimate
// revoke-signer fork that orphans an offline node's cached tip cannot
// permanently false-positive the equivocation flag. Nodes that report beacons
// but were NEVER connected are still checked (not skipped).
func TestClientCheckBeaconConsistencySkipsNonConnected(t *testing.T) {
	SetTrustSyncIntervalForTest(time.Hour)
	t.Cleanup(func() { SetTrustSyncIntervalForTest(30 * time.Second) })

	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	noop := func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) { return nil, nil, nil }
	n1 := &fakeNode{id: "n1", key: mustKP(t), handle: noop}
	// Authorize n1's Noise identity key so Connect() will open a channel to n1
	// and populate everConnected for n1.
	_ = lg.AuthorizeDevice(n1.key.Public, signer)
	entries := lg.Entries()
	chain := trustlog.MarshalChain(entries)
	genesisHash := trustlog.HashEntry(&entries[0])

	gw, clientConn := newFakeMultiGateway(t, n1)
	gw.chain = chain
	defer gw.peer.Close()

	c, err := NewE2EClientWithGenesis(clientConn, genesisHash)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Verify n1 was actually connected (everConnected is set for n1's key).
	c.mu.Lock()
	wasEverConnected := c.everConnected[string(n1.key.Public)]
	c.mu.Unlock()
	if !wasEverConnected {
		t.Fatal("n1 must be in everConnected after Connect (its key is authorized)")
	}

	// Inject a divergent beacon for n1, then simulate offline by removing it
	// from byNode WITHOUT pruning the beacon cache (mimics a disconnect between
	// a reevaluateChannels call and the next checkBeaconConsistency tick).
	divergentTip := bytes.Repeat([]byte{0xde}, 32)
	bPub, bPriv := genBeaconKey(t)
	b := api.SignBeacon(bPriv, bPub, divergentTip, 5, 1)
	n1KeyStr := string(n1.key.Public)
	c.mu.Lock()
	c.beacons[n1KeyStr] = b
	c.beaconCtr[n1KeyStr] = b.Counter
	delete(c.byNode, "n1") // simulate disconnect without beacon prune
	c.mu.Unlock()

	// Run more consistency checks than the threshold: n1 is everConnected but
	// currently offline, so its stale divergent beacon must be skipped entirely.
	for i := 0; i < beaconMissThreshold+2; i++ {
		c.checkBeaconConsistency()
	}

	if c.Equivocation() {
		t.Fatal("checkBeaconConsistency must NOT set equivocation for an offline (ever-connected) node's stale cached beacon")
	}
}

// TestEquivocationRequiresPersistence verifies that a single unreconciled tick does
// not flag equivocation (benign propagation lag reconciles on the next pull), but two
// consecutive unreconciled ticks for the same foreign tip do flag it.
func TestEquivocationRequiresPersistence(t *testing.T) {
	// Use a very long sync interval so the background trustSyncLoop does not fire
	// between our explicit syncTrustLog() calls (which would add spurious miss ticks).
	SetTrustSyncIntervalForTest(time.Hour)
	t.Cleanup(func() { SetTrustSyncIntervalForTest(30 * time.Second) })

	// Two-entry chain: genesis + authorize-device.
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	_ = lg.AuthorizeDevice(bytes.Repeat([]byte{0x55}, 32), signer)
	entries := lg.Entries()
	genesisOnlyChain := trustlog.MarshalChain(entries[:1])
	fullChain := trustlog.MarshalChain(entries)
	genesisHash := trustlog.HashEntry(&entries[0])
	fullTip := trustlog.HashEntry(&entries[len(entries)-1])
	foreignTip := bytes.Repeat([]byte{0xfe}, 32)

	bPub, bPriv := genBeaconKey(t)

	noop := func(_ string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return nil, nil, nil
	}
	n1 := &fakeNode{id: "n1", key: mustKP(t), handle: noop}
	gw, clientConn := newFakeMultiGateway(t, n1)
	gw.chain = genesisOnlyChain
	defer gw.peer.Close()

	c, err := NewE2EClientWithGenesis(clientConn, genesisHash)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	sendBeacon := func(tip []byte, counter uint64) {
		b := api.SignBeacon(bPriv, bPub, tip, len(entries), counter)
		_ = gw.peer.Notify(api.MethodNodeEvent, api.NodeEvent{
			Type: api.NodeEventBeacon,
			Node: api.NodeDescriptor{
				ID:             n1.id,
				IdentityPubKey: base64.StdEncoding.EncodeToString(n1.key.Public),
				BeaconPubKey:   base64.StdEncoding.EncodeToString(bPub),
				Beacon:         &b,
			},
		})
	}

	t.Run("benign-lag-clears", func(t *testing.T) {
		// The node beacons fullTip — a tip that exists in the full chain but is NOT
		// yet in the client's genesis-only chain (client is behind by one pull).
		sendBeacon(fullTip, 1)
		waitClient(t, "beacon ingested", func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			b, ok := c.beacons[string(n1.key.Public)]
			return ok && b.Counter == 1
		})

		c.syncTrustLog() // tick 1: client has genesis only → tip absent → miss=1
		if c.Equivocation() {
			t.Fatal("single miss must not set equivocation flag (could be propagation lag)")
		}

		// Advance gateway to serve the full chain (node was legitimately ahead).
		gw.chain = fullChain
		c.syncTrustLog() // tick 2: client pulls full chain → tip reconciles → miss reset
		if c.Equivocation() {
			t.Fatal("benign propagation lag must never set the equivocation flag after reconciliation")
		}
	})

	t.Run("genuine-divergence-requires-two-ticks", func(t *testing.T) {
		// Establish a fresh beacon with a foreign tip (never in any chain).
		// Counter 2 > 1 advances the stored beacon and resets the miss streak.
		c.mu.Lock()
		c.equivocation = false
		c.mu.Unlock()

		sendBeacon(foreignTip, 2)
		waitClient(t, "foreign beacon ingested", func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			b, ok := c.beacons[string(n1.key.Public)]
			return ok && b.Counter == 2
		})

		c.syncTrustLog() // tick 1: foreign tip absent → miss=1, not yet flagged
		if c.Equivocation() {
			t.Fatal("first miss of foreign tip must not flag equivocation (need 2 consecutive)")
		}

		c.syncTrustLog() // tick 2: same foreign tip still absent → miss=2, flag set
		if !c.Equivocation() {
			t.Fatal("two consecutive misses for the same foreign tip must set the equivocation flag")
		}
	})
}
