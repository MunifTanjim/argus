package trustpin

import "sync"

// Gate is the fail-closed state of a device that has seen a trust-log chain but
// holds no pin to check it against. A tripped gate rejects all E2E traffic in
// both directions: an unpinned device cannot distinguish the network's real
// chain from one a hostile gateway fabricated, so it must refuse rather than
// guess. The zero value is usable and un-tripped.
type Gate struct {
	mu      sync.RWMutex
	tripped bool
	genesis []byte
}

// Trip fails the device closed and records the observed genesis. Idempotent: the
// first observed genesis is kept so status output stays stable across ticks.
func (g *Gate) Trip(genesis []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.genesis == nil {
		g.genesis = append([]byte(nil), genesis...)
		g.tripped = true
	}
}

func (g *Gate) Tripped() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.tripped
}

// Genesis returns a copy of the first observed genesis, or nil if never tripped.
func (g *Gate) Genesis() []byte {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.genesis == nil {
		return nil
	}
	return append([]byte(nil), g.genesis...)
}

// Clear releases the gate once a pin has been adopted.
func (g *Gate) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tripped = false
	g.genesis = nil
}
