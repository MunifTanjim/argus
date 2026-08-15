package node

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

// TestHandleBeaconDeliverCounterGuardAtomicUnderConcurrency verifies that the
// counter guard (accept only a strictly-greater counter) and the store update
// are one atomic section. In the buggy form the guard reads lastCtr under the
// lock, releases it, checks, then re-acquires to write: two concurrent
// deliveries can both read the same lastCtr, both pass, then race their writes
// so the LOWER counter's write lands last — storing an older beacon (and a lower
// counter) than one already accepted. A malicious courier can exploit that to
// overwrite (suppress) a newer beacon whose tip revealed equivocation.
//
// Invariant: after N concurrent deliveries with distinct increasing counters,
// the stored counter and stored beacon must correspond to the HIGHEST counter —
// once it is accepted, every lower counter must fail the strictly-greater guard
// and cannot clobber it, regardless of interleaving.
func TestHandleBeaconDeliverCounterGuardAtomicUnderConcurrency(t *testing.T) {
	pub, priv := genBeaconKeyPair(t)

	const rounds = 500
	const workers = 32
	// Pre-sign and pre-marshal every delivery so the goroutines do no per-call
	// crypto before the lock dance — they pile into the check-then-act window
	// together, maximizing the chance a stale reader writes last.
	params := make([]json.RawMessage, workers+1)
	for c := 1; c <= workers; c++ {
		// Distinct tip per counter so the stored beacon can be matched to the
		// counter that produced it.
		tip := bytes.Repeat([]byte{byte(c)}, 32)
		params[c] = marshalBeacon(t, api.SignBeacon(priv, pub, tip, 1, uint64(c)))
	}

	for r := 0; r < rounds; r++ {
		d, _, _ := setupNodeWithTrust(t)
		addPeerPub(d, pub)

		var wg sync.WaitGroup
		start := make(chan struct{})
		for c := 1; c <= workers; c++ {
			wg.Add(1)
			go func(counter int) {
				defer wg.Done()
				<-start // release all goroutines at once
				_, _ = d.handleBeaconDeliver(context.Background(), params[counter])
			}(c)
		}
		close(start)
		wg.Wait()

		d.peerBeaconMu.Lock()
		gotCtr := d.peerBeaconCtr[string(pub)]
		gotBeacon := d.peerBeacons[string(pub)]
		d.peerBeaconMu.Unlock()

		if gotCtr != uint64(workers) {
			t.Fatalf("round %d: stored counter = %d, want %d (a lower counter clobbered the highest)",
				r, gotCtr, workers)
		}
		wantTip := bytes.Repeat([]byte{byte(workers)}, 32)
		if !bytes.Equal(gotBeacon.Tip, wantTip) || gotBeacon.Counter != uint64(workers) {
			t.Fatalf("round %d: stored beacon counter=%d tip=%x, want counter=%d tip=%x "+
				"(stored beacon does not match the highest accepted counter)",
				r, gotBeacon.Counter, gotBeacon.Tip, workers, wantTip)
		}
	}
}
