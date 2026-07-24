package node

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// beaconMissThreshold is the number of consecutive unreconciled peer-beacon
// checks required before setting the node-side equivocation flag. Mirrors the
// client's beaconMissThreshold.
const beaconMissThreshold = 2

// beaconMissState tracks consecutive unreconciled ticks for a single peer's beacon tip.
type beaconMissState struct {
	tip    []byte
	misses int
}

// handleBeaconDeliver ingests a signed HEAD beacon delivered by the client
// courier. Guards (in order):
//
//  1. api.VerifyBeacon — Ed25519 sig must verify against b.BeaconPub.
//  2. Attribution — b.BeaconPub must match a roster-known peer's beacon_pubkey
//     (populated by syncRoster); silently drops beacons from unknown keys.
//  3. Counter guard — b.Counter must be strictly greater than the last accepted
//     counter for this key; ignores stale and replayed beacons.
//  4. Stores the accepted beacon; deletes any prior miss streak (new beacon
//     supersedes it). The consistency check runs separately on each trust-sync
//     tick via checkPeerBeaconConsistency.
//
// A malicious client can replay (counter-caught) or withhold but cannot forge
// (VerifyBeacon + roster attribution).
func (d *Node) handleBeaconDeliver(_ context.Context, params json.RawMessage) (any, error) {
	b, err := api.Decode[api.Beacon](params)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
	}

	// (a) Signature check.
	if !api.VerifyBeacon(b) {
		return nil, nil // invalid sig: silently drop
	}

	// (b) Attribution + (c) counter guard + accept — one atomic section. The read
	// of the last accepted counter, the strictly-greater check, and the store
	// update must not be split across separate locked regions: two concurrent
	// deliveries could otherwise both read the same last counter, both pass the
	// guard, then race their writes so a LOWER counter's write lands last —
	// overwriting a newer beacon (a courier could use that to suppress a beacon
	// whose tip revealed equivocation). VerifyBeacon (crypto) already ran above,
	// outside the lock.
	key := string(b.BeaconPub)
	d.peerBeaconMu.Lock()
	defer d.peerBeaconMu.Unlock()
	if !d.peerBeaconPubs[key] {
		return nil, nil // unknown beacon pub: drop
	}
	if b.Counter <= d.peerBeaconCtr[key] {
		return nil, nil // stale or replayed: ignore
	}
	d.peerBeaconCtr[key] = b.Counter
	d.peerBeacons[key] = b
	delete(d.peerBeaconMiss, key) // new beacon supersedes any miss streak

	return nil, nil
}

// checkPeerBeaconConsistency cross-checks all stored peer beacons against this
// node's own resolved trust-log chain. Called on each syncTrustOnce tick so
// the N=2 persistence guard accumulates across ticks (matching the client's
// checkBeaconConsistency pattern) rather than across deliveries.
//
// A peer beacon tip present in the node's linear chain history reconciles and
// clears any miss streak. A tip absent for beaconMissThreshold consecutive
// ticks sets d.equivocation permanently and logs a warning.
//
// No-op if the node has no trust store or an empty chain.
func (d *Node) checkPeerBeaconConsistency() {
	st := d.trust.Load()
	if st == nil {
		return
	}
	chainBytes, tip := st.BytesAndTip()
	if len(chainBytes) == 0 {
		return
	}
	d.peerBeaconMu.Lock()
	defer d.peerBeaconMu.Unlock()
	if len(d.peerBeacons) == 0 {
		return // no peer beacons yet: skip the chain parse/hash entirely
	}
	known := d.beaconKnown
	if known == nil || !bytes.Equal(tip, d.beaconKnownTip) {
		entries, err := trustlog.UnmarshalChain(chainBytes)
		if err != nil || len(entries) == 0 {
			return // parse failure: be lenient rather than false-positive
		}
		known = make(map[string]bool, len(entries))
		for i := range entries {
			known[string(trustlog.HashEntry(&entries[i]))] = true
		}
		d.beaconKnown = known
		d.beaconKnownTip = tip
	}
	var toFlag []string
	for key, b := range d.peerBeacons {
		// Belt-and-suspenders: skip peers no longer in the current roster attribution
		// set. syncRoster prunes beacon state every 10 ticks; this guard closes the
		// window between those calls so a de-rostered peer never accumulates misses.
		if !d.peerBeaconPubs[key] {
			continue
		}
		if len(b.Tip) == 0 {
			delete(d.peerBeaconMiss, key) // no tip yet: clear any prior miss
			continue
		}
		if known[string(b.Tip)] {
			delete(d.peerBeaconMiss, key) // tip reconciled: reset miss streak
			continue
		}
		// Tip not in resolved chain: track per-peer consecutive misses.
		ms := d.peerBeaconMiss[key]
		if ms == nil || !bytes.Equal(ms.tip, b.Tip) {
			ms = &beaconMissState{tip: append([]byte(nil), b.Tip...), misses: 1}
			d.peerBeaconMiss[key] = ms
		} else {
			ms.misses++
		}
		if ms.misses >= beaconMissThreshold {
			toFlag = append(toFlag, fmt.Sprintf("peer=%x tip=%x", []byte(key), b.Tip))
		}
	}
	if len(toFlag) > 0 {
		d.log.Warn("node: equivocation detected — peer beacon diverges from resolved chain",
			"peers", strings.Join(toFlag, "; "))
		d.equivocation.Store(true)
	}
}

// syncRoster fetches the gateway roster and updates peerBeaconPubs with the
// beacon_pubkey values of all OTHER roster nodes. Called on each sync tick so
// newly joining nodes are recognized for beacon attribution.
func (d *Node) syncRoster(peer trustCaller) {
	var result api.NodesListResult
	if err := peer.Call(api.MethodNodesList, nil, &result); err != nil {
		return
	}
	pubs := make(map[string]bool, len(result.Nodes))
	for _, nd := range result.Nodes {
		if nd.BeaconPubKey == "" || nd.ID == d.id {
			continue // skip self and nodes without a beacon key
		}
		raw, err := base64.StdEncoding.DecodeString(nd.BeaconPubKey)
		if err != nil {
			continue
		}
		pubs[string(raw)] = true
	}
	d.peerBeaconMu.Lock()
	d.peerBeaconPubs = pubs
	// Prune beacon state for peers no longer present in the roster so a
	// de-rostered node's stale miss streak cannot false-positive the flag.
	for key := range d.peerBeacons {
		if !pubs[key] {
			delete(d.peerBeacons, key)
			delete(d.peerBeaconCtr, key)
			delete(d.peerBeaconMiss, key)
		}
	}
	d.peerBeaconMu.Unlock()
}
