package node

import (
	"path/filepath"
	"sync"
	"testing"
)

// TestMakeBeaconPersistedCounterMonotonicUnderConcurrency verifies the invariant
// "the persisted counter is >= every emitted counter". makeBeacon is called from
// several unserialized goroutines (identify response, sync-tick offer, uplink
// offer). The atomic increment hands out ordered counters, but if the subsequent
// persist calls are not serialized, a lower counter's temp+rename can land on
// disk AFTER a higher counter was already emitted to the gateway. On restart the
// node then seeds below an emitted value and re-emits a reused counter bound to a
// possibly different tip — manufactured equivocation evidence against an honest
// node. `go test -race` cannot see this (it is a logical ordering race on the
// persisted file, not a memory race), so we assert the invariant directly.
func TestMakeBeaconPersistedCounterMonotonicUnderConcurrency(t *testing.T) {
	kp, err := LoadOrCreateBeaconKey(filepath.Join(t.TempDir(), "seed-key.json"))
	if err != nil {
		t.Fatalf("LoadOrCreateBeaconKey: %v", err)
	}

	const rounds = 20
	const workers = 32
	for r := 0; r < rounds; r++ {
		keyPath := filepath.Join(t.TempDir(), "beacon-key.json")
		d := newNode(nil)
		d.SetBeaconKey(kp)
		d.SetBeaconCounterPath(keyPath) // no file yet; seeds from 0

		var wg sync.WaitGroup
		var mu sync.Mutex
		var maxEmitted uint64
		errs := make(chan error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				b, err := d.makeBeacon()
				if err != nil {
					errs <- err
					return
				}
				mu.Lock()
				if b.Counter > maxEmitted {
					maxEmitted = b.Counter
				}
				mu.Unlock()
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("round %d: makeBeacon: %v", r, err)
		}

		persisted := LoadBeaconCounter(keyPath)
		if persisted < maxEmitted {
			t.Fatalf("round %d: persisted counter = %d, but max emitted = %d — "+
				"a lower counter's persist landed after a higher emitted one; "+
				"restart would reseed below an emitted counter and reuse it",
				r, persisted, maxEmitted)
		}
	}
}
