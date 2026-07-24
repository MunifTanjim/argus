package node

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// TestBeaconKnownSetCachedByTip verifies that checkPeerBeaconConsistency caches
// the entry-hash set keyed on the resolved chain tip and reuses it on an unchanged
// tick, then rebuilds it when the tip advances.
func TestBeaconKnownSetCachedByTip(t *testing.T) {
	d, entries, _ := setupNodeWithTrust(t)
	pub, priv := genBeaconKeyPair(t)
	addPeerPub(d, pub)

	// Use a chain-consistent tip so the check does not accumulate misses.
	chainTip := trustlog.HashEntry(&entries[len(entries)-1])
	b := api.SignBeacon(priv, pub, chainTip, len(entries), 1)
	if _, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b)); err != nil {
		t.Fatalf("handleBeaconDeliver: %v", err)
	}

	// Prime: first consistency pass builds and caches the known-set.
	d.checkPeerBeaconConsistency()
	tip1 := append([]byte(nil), d.beaconKnownTip...)
	set1 := d.beaconKnown
	if set1 == nil || len(tip1) == 0 {
		t.Fatal("expected known-set cached after first pass")
	}

	// Unchanged chain: mark the cached map with a sentinel key and verify the
	// second pass does NOT replace it (same map instance ⇒ sentinel still present).
	const sentinel = "\xff_beacon_cache_sentinel"
	d.beaconKnown[sentinel] = true
	d.checkPeerBeaconConsistency()
	if !d.beaconKnown[sentinel] {
		t.Fatal("unchanged-chain tick must reuse the cached known-set, not rebuild it")
	}

	// Changed tip: replace the trust store with a fresh chain whose tip differs.
	// The sentinel must be gone from d.beaconKnown (new map allocated for new tip).
	signer2, _ := trustlog.GenerateSigner()
	lg2, _ := trustlog.NewGenesis([][]byte{signer2.Public}, signer2, nil)
	genesisHash2 := trustlog.HashEntry(&lg2.Entries()[0])
	_ = lg2.AuthorizeDevice(bytes.Repeat([]byte{0x99}, 32), signer2)
	ss2 := trustlog.NewSyncStore(genesisHash2)
	if _, err := ss2.Ingest(trustlog.MarshalChain(lg2.Entries())); err != nil {
		t.Fatalf("Ingest new chain: %v", err)
	}
	d.trust.Store(ss2)

	d.checkPeerBeaconConsistency()
	if d.beaconKnown[sentinel] {
		t.Fatal("changed chain tip must rebuild the known-set, not reuse the stale cache")
	}
}

// genBeaconKeyPair returns a fresh Ed25519 beacon keypair for test use.
func genBeaconKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	return pub, priv
}

// setupNodeWithTrust returns a node with a 2-entry trust chain (genesis +
// authorize-device) and returns the chain entries and bytes.
func setupNodeWithTrust(t *testing.T) (*Node, []trustlog.Entry, []byte) {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	lg, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash := trustlog.HashEntry(&lg.Entries()[0])
	if err := lg.AuthorizeDevice(bytes.Repeat([]byte{0x42}, 32), signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	entries := lg.Entries()
	chain := trustlog.MarshalChain(entries)

	d := newNode(nil)
	ss := trustlog.NewSyncStore(genesisHash)
	if _, err := ss.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	d.trust.Store(ss)
	return d, entries, chain
}

// marshalBeacon encodes a Beacon as a JSON params payload for handleBeaconDeliver.
func marshalBeacon(t *testing.T, b api.Beacon) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("json.Marshal(Beacon): %v", err)
	}
	return raw
}

// addPeerPub registers pub as a roster-known peer beacon public key on d.
func addPeerPub(d *Node, pub ed25519.PublicKey) {
	d.peerBeaconMu.Lock()
	d.peerBeaconPubs[string(pub)] = true
	d.peerBeaconMu.Unlock()
}

// TestHandleBeaconDeliverDropsBadSig verifies that a beacon with an invalid
// (zeroed) signature is silently dropped and does not update any state.
func TestHandleBeaconDeliverDropsBadSig(t *testing.T) {
	d, _, _ := setupNodeWithTrust(t)
	pub, priv := genBeaconKeyPair(t)
	addPeerPub(d, pub)

	tip := bytes.Repeat([]byte{0xaa}, 32)
	b := api.SignBeacon(priv, pub, tip, 1, 1)
	b.Sig = make([]byte, len(b.Sig)) // zero out the sig

	_, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b))
	if err != nil {
		t.Fatalf("handleBeaconDeliver should not return an error for a bad sig: %v", err)
	}
	d.peerBeaconMu.Lock()
	_, stored := d.peerBeacons[string(pub)]
	d.peerBeaconMu.Unlock()
	if stored {
		t.Fatal("beacon with bad sig must not be stored")
	}
}

// TestHandleBeaconDeliverDropsUnknownPub verifies that a beacon whose
// BeaconPub is not in the roster-known set is silently dropped.
func TestHandleBeaconDeliverDropsUnknownPub(t *testing.T) {
	d, _, _ := setupNodeWithTrust(t)
	pub, priv := genBeaconKeyPair(t)
	// Deliberately do NOT register pub in peerBeaconPubs.

	tip := bytes.Repeat([]byte{0xbb}, 32)
	b := api.SignBeacon(priv, pub, tip, 1, 1)

	_, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b))
	if err != nil {
		t.Fatalf("handleBeaconDeliver must not error for unknown pub: %v", err)
	}
	d.peerBeaconMu.Lock()
	_, stored := d.peerBeacons[string(pub)]
	d.peerBeaconMu.Unlock()
	if stored {
		t.Fatal("beacon with unknown BeaconPub must not be stored")
	}
}

// TestHandleBeaconDeliverIgnoresStaleCounter verifies that a beacon with a
// counter ≤ the last accepted counter is ignored (replay and stale defence).
func TestHandleBeaconDeliverIgnoresStaleCounter(t *testing.T) {
	d, _, _ := setupNodeWithTrust(t)
	pub, priv := genBeaconKeyPair(t)
	addPeerPub(d, pub)

	tip := bytes.Repeat([]byte{0xcc}, 32)
	b5 := api.SignBeacon(priv, pub, tip, 1, 5)
	if _, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b5)); err != nil {
		t.Fatalf("counter=5: %v", err)
	}
	d.peerBeaconMu.Lock()
	ctr := d.peerBeaconCtr[string(pub)]
	d.peerBeaconMu.Unlock()
	if ctr != 5 {
		t.Fatalf("expected counter=5, got %d", ctr)
	}

	// Stale: counter 3 < 5 → ignored.
	b3 := api.SignBeacon(priv, pub, tip, 1, 3)
	if _, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b3)); err != nil {
		t.Fatalf("stale counter=3: %v", err)
	}
	d.peerBeaconMu.Lock()
	ctr = d.peerBeaconCtr[string(pub)]
	d.peerBeaconMu.Unlock()
	if ctr != 5 {
		t.Fatalf("stale beacon (3<5) must not update counter; got %d", ctr)
	}

	// Equal: counter 5 == 5 → also ignored (must be strictly greater).
	b5eq := api.SignBeacon(priv, pub, tip, 1, 5)
	if _, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b5eq)); err != nil {
		t.Fatalf("equal counter=5: %v", err)
	}
	d.peerBeaconMu.Lock()
	ctr = d.peerBeaconCtr[string(pub)]
	d.peerBeaconMu.Unlock()
	if ctr != 5 {
		t.Fatalf("equal-counter beacon (5==5) must not update counter; got %d", ctr)
	}
}

// TestHandleBeaconDeliverConsistentTip verifies that a peer beacon with a tip
// present in this node's chain does not set the equivocation flag.
func TestHandleBeaconDeliverConsistentTip(t *testing.T) {
	d, entries, _ := setupNodeWithTrust(t)
	pub, priv := genBeaconKeyPair(t)
	addPeerPub(d, pub)

	chainTip := trustlog.HashEntry(&entries[len(entries)-1])
	b := api.SignBeacon(priv, pub, chainTip, len(entries), 1)
	if _, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b)); err != nil {
		t.Fatalf("handleBeaconDeliver: %v", err)
	}

	d.checkPeerBeaconConsistency()
	d.checkPeerBeaconConsistency()
	if d.Equivocation() {
		t.Fatal("consistent peer beacon must not set the equivocation flag")
	}
}

// TestHandleBeaconDeliverEquivocationRequiresTwoTicks verifies the N=2
// persistence guard: one unreconciled tick must not flag equivocation, but two
// consecutive unreconciled ticks do.
func TestHandleBeaconDeliverEquivocationRequiresTwoTicks(t *testing.T) {
	d, _, _ := setupNodeWithTrust(t)
	pub, priv := genBeaconKeyPair(t)
	addPeerPub(d, pub)

	divergentTip := bytes.Repeat([]byte{0xde}, 32)
	b := api.SignBeacon(priv, pub, divergentTip, 5, 1)
	if _, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b)); err != nil {
		t.Fatalf("handleBeaconDeliver: %v", err)
	}

	d.checkPeerBeaconConsistency() // tick 1: miss=1, not yet flagged
	if d.Equivocation() {
		t.Fatal("first miss must not set equivocation flag (requires 2 consecutive)")
	}

	d.checkPeerBeaconConsistency() // tick 2: miss=2 → flag
	if !d.Equivocation() {
		t.Fatal("two consecutive unreconciled ticks must set the equivocation flag")
	}
}

// TestHandleBeaconDeliverBenignLagClears verifies that a peer beacon tip that
// reconciles on the second tick (node was behind by one chain entry) does NOT
// trigger the equivocation flag.
func TestHandleBeaconDeliverBenignLagClears(t *testing.T) {
	signer, _ := trustlog.GenerateSigner()
	lg, _ := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	genesisEntry := lg.Entries()[0]
	genesisHash := trustlog.HashEntry(&genesisEntry)
	_ = lg.AuthorizeDevice(bytes.Repeat([]byte{0x55}, 32), signer)
	entries := lg.Entries()
	genesisOnlyChain := trustlog.MarshalChain(entries[:1])
	fullChain := trustlog.MarshalChain(entries)
	fullTip := trustlog.HashEntry(&entries[len(entries)-1])

	d := newNode(nil)
	ss := trustlog.NewSyncStore(genesisHash)
	if _, err := ss.Ingest(genesisOnlyChain); err != nil {
		t.Fatalf("Ingest genesis: %v", err)
	}
	d.trust.Store(ss)

	pub, priv := genBeaconKeyPair(t)
	addPeerPub(d, pub)

	// Peer beacons the full tip (one ahead of us — benign lag).
	b := api.SignBeacon(priv, pub, fullTip, len(entries), 1)
	if _, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b)); err != nil {
		t.Fatalf("handleBeaconDeliver: %v", err)
	}

	d.checkPeerBeaconConsistency() // tick 1: fullTip not in genesis-only chain → miss=1
	if d.Equivocation() {
		t.Fatal("first miss must not set equivocation flag (could be propagation lag)")
	}

	// Node advances its own chain.
	if _, err := ss.Ingest(fullChain); err != nil {
		t.Fatalf("Ingest full chain: %v", err)
	}

	d.checkPeerBeaconConsistency() // tick 2: fullTip now in chain → miss cleared → no flag
	if d.Equivocation() {
		t.Fatal("benign lag (tip reconciles on second tick) must not set equivocation flag")
	}
}

// TestBeaconDeliverRemoteDispatchAllowed verifies that beacon.deliver passes
// through remoteDispatch (the E2E responder path) and is not blocked like
// lock.* methods.
func TestBeaconDeliverRemoteDispatchAllowed(t *testing.T) {
	d := newNode(nil)
	remote := d.remoteDispatch()

	pub, priv := genBeaconKeyPair(t)
	b := api.SignBeacon(priv, pub, nil, 0, 1)
	params, _ := json.Marshal(b)
	_, err := remote(context.Background(), api.MethodBeaconDeliver, params)
	if err != nil {
		rpcErr, ok := err.(*api.RPCError)
		if ok && rpcErr.Code == api.CodeMethodNotFound {
			t.Fatal("beacon.deliver must not be blocked by remoteDispatch (must be reachable over E2E)")
		}
		t.Fatalf("unexpected error from remoteDispatch(beacon.deliver): %v", err)
	}
}

// TestSyncRosterUpdatesKnownPubs verifies that syncRoster populates
// peerBeaconPubs from nodes.list, excluding self and nodes without a beacon key.
func TestSyncRosterUpdatesKnownPubs(t *testing.T) {
	d := newNode(nil)
	d.id = "self"

	bPub1, _ := genBeaconKeyPair(t)
	bPub2, _ := genBeaconKeyPair(t)
	bPubSelf, _ := genBeaconKeyPair(t)

	roster := api.NodesListResult{Nodes: []api.NodeDescriptor{
		{ID: "n1", BeaconPubKey: base64.StdEncoding.EncodeToString(bPub1)},
		{ID: "n2", BeaconPubKey: base64.StdEncoding.EncodeToString(bPub2)},
		{ID: "self", BeaconPubKey: base64.StdEncoding.EncodeToString(bPubSelf)}, // self: excluded
		{ID: "n3", BeaconPubKey: ""},                                            // no key: excluded
	}}
	fake := &fakeTrustCaller{
		fn: func(method string, _, out any) error {
			if method != api.MethodNodesList {
				return &api.RPCError{Code: api.CodeMethodNotFound}
			}
			b, _ := json.Marshal(roster)
			return json.Unmarshal(b, out)
		},
	}
	d.syncRoster(fake)

	d.peerBeaconMu.Lock()
	pubs := d.peerBeaconPubs
	d.peerBeaconMu.Unlock()

	if len(pubs) != 2 {
		t.Fatalf("expected 2 peer pubs (n1, n2), got %d", len(pubs))
	}
	if !pubs[string(bPub1)] {
		t.Error("n1's beacon pub must be in peerBeaconPubs")
	}
	if !pubs[string(bPub2)] {
		t.Error("n2's beacon pub must be in peerBeaconPubs")
	}
	if pubs[string(bPubSelf)] {
		t.Error("self's beacon pub must NOT be in peerBeaconPubs")
	}
}

// TestSyncRosterPrunesDerosteredPeerState verifies that when a peer is removed
// from the roster, syncRoster drops its peerBeacons/peerBeaconCtr/peerBeaconMiss
// entries so a stale miss streak cannot false-positive the equivocation flag.
func TestSyncRosterPrunesDerosteredPeerState(t *testing.T) {
	d, _, _ := setupNodeWithTrust(t)
	d.id = "self"

	bPub1, bPriv1 := genBeaconKeyPair(t)
	bPub2, bPriv2 := genBeaconKeyPair(t)

	divergentTip := bytes.Repeat([]byte{0xde}, 32)

	// Add both peers to the roster and deliver beacons from each.
	addPeerPub(d, bPub1)
	addPeerPub(d, bPub2)

	b1 := api.SignBeacon(bPriv1, bPub1, divergentTip, 5, 1)
	b2 := api.SignBeacon(bPriv2, bPub2, divergentTip, 5, 1)
	if _, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b1)); err != nil {
		t.Fatalf("deliver b1: %v", err)
	}
	if _, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b2)); err != nil {
		t.Fatalf("deliver b2: %v", err)
	}

	// Accumulate one miss tick for both peers (divergent tip not in chain).
	d.checkPeerBeaconConsistency()
	if d.Equivocation() {
		t.Fatal("single miss must not set equivocation flag")
	}

	// Verify miss state is recorded for both keys.
	d.peerBeaconMu.Lock()
	ms1 := d.peerBeaconMiss[string(bPub1)]
	ms2 := d.peerBeaconMiss[string(bPub2)]
	d.peerBeaconMu.Unlock()
	if ms1 == nil || ms1.misses != 1 {
		t.Fatalf("expected miss=1 for peer1, got %v", ms1)
	}
	if ms2 == nil || ms2.misses != 1 {
		t.Fatalf("expected miss=1 for peer2, got %v", ms2)
	}

	// De-roster peer2 (only n1 remains). syncRoster must prune peer2's state.
	roster := api.NodesListResult{Nodes: []api.NodeDescriptor{
		{ID: "n1", BeaconPubKey: base64.StdEncoding.EncodeToString(bPub1)},
	}}
	fake := &fakeTrustCaller{
		fn: func(method string, _, out any) error {
			b, _ := json.Marshal(roster)
			return json.Unmarshal(b, out)
		},
	}
	d.syncRoster(fake)

	d.peerBeaconMu.Lock()
	_, hasPeer2Beacon := d.peerBeacons[string(bPub2)]
	_, hasPeer2Ctr := d.peerBeaconCtr[string(bPub2)]
	_, hasPeer2Miss := d.peerBeaconMiss[string(bPub2)]
	// peer1 state must still be present.
	_, hasPeer1Beacon := d.peerBeacons[string(bPub1)]
	d.peerBeaconMu.Unlock()

	if hasPeer2Beacon || hasPeer2Ctr || hasPeer2Miss {
		t.Fatal("de-rostered peer's beacon state must be pruned by syncRoster")
	}
	if !hasPeer1Beacon {
		t.Fatal("remaining peer's beacon must not be pruned")
	}

	// A second consistency check must NOT set the equivocation flag for peer2
	// (its state was pruned). Peer1 still has miss=1; one more tick would flag
	// equivocation for peer1, but peer2 is gone.
	d.checkPeerBeaconConsistency() // peer1: miss=2 → equivocation flagged for peer1
	// The test validates pruning — peer2's stale state does not contribute.
	// (Equivocation may be set for peer1 at this point, which is correct behaviour.)
	d.peerBeaconMu.Lock()
	_, hasPeer2MissAfter := d.peerBeaconMiss[string(bPub2)]
	d.peerBeaconMu.Unlock()
	if hasPeer2MissAfter {
		t.Fatal("de-rostered peer2 must not re-appear in peerBeaconMiss after syncRoster")
	}
}

// TestCheckPeerBeaconConsistencySkipsDerosteredPeer verifies the belt-and-
// suspenders guard in checkPeerBeaconConsistency: a peer key not present in
// the current peerBeaconPubs (the roster attribution set) is skipped and does
// not accumulate misses. This closes the window between syncRoster calls
// (which run every 10 ticks) where a freshly de-rostered peer's stale beacon
// state could otherwise false-positive the equivocation flag.
func TestCheckPeerBeaconConsistencySkipsDerosteredPeer(t *testing.T) {
	d, _, _ := setupNodeWithTrust(t)
	pub, priv := genBeaconKeyPair(t)
	addPeerPub(d, pub)

	divergentTip := bytes.Repeat([]byte{0xde}, 32)
	b := api.SignBeacon(priv, pub, divergentTip, 5, 1)
	if _, err := d.handleBeaconDeliver(context.Background(), marshalBeacon(t, b)); err != nil {
		t.Fatalf("handleBeaconDeliver: %v", err)
	}

	// Tick 1: miss=1, not yet flagged.
	d.checkPeerBeaconConsistency()
	if d.Equivocation() {
		t.Fatal("single miss must not set equivocation flag")
	}

	// De-roster the peer from peerBeaconPubs (simulating syncRoster removing it)
	// but deliberately keep the stale beacon+miss state to test the guard.
	d.peerBeaconMu.Lock()
	delete(d.peerBeaconPubs, string(pub))
	d.peerBeaconMu.Unlock()

	// Tick 2: peer key is no longer in peerBeaconPubs — must be skipped.
	d.checkPeerBeaconConsistency()
	if d.Equivocation() {
		t.Fatal("checkPeerBeaconConsistency must NOT set equivocation for a de-rostered peer " +
			"(key not in peerBeaconPubs)")
	}
}

// fakeTrustCaller is a test double for the trustCaller interface.
type fakeTrustCaller struct {
	fn func(method string, params, out any) error
}

func (f *fakeTrustCaller) Call(method string, params, out any) error {
	return f.fn(method, params, out)
}
