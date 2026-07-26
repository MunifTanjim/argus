package trustpin

import "sync/atomic"

// Gate is the fail-closed state of a device that has seen a trust-log chain but
// holds no pin to check it against. A tripped gate rejects all E2E traffic in
// both directions: an unpinned device cannot distinguish the network's real
// chain from one a hostile gateway fabricated, so it must refuse rather than
// guess. The zero value is usable and un-tripped.
type Gate struct {
	genesis atomic.Pointer[[]byte]
}

// Trip fails the device closed and records the observed genesis. Idempotent: the
// first observed genesis is kept so status output stays stable across ticks.
func (g *Gate) Trip(genesis []byte) {
	cp := make([]byte, len(genesis))
	copy(cp, genesis)
	g.genesis.CompareAndSwap(nil, &cp)
}

func (g *Gate) Tripped() bool { return g.genesis.Load() != nil }

// Genesis returns a copy of the first observed genesis, or nil if never tripped.
// A tripped gate always reports non-nil (empty when Trip was handed no genesis),
// so callers can never infer "open" from a nil read.
func (g *Gate) Genesis() []byte {
	p := g.genesis.Load()
	if p == nil {
		return nil
	}
	out := make([]byte, len(*p))
	copy(out, *p)
	return out
}

// Clear releases the gate once a pin has been adopted.
func (g *Gate) Clear() {
	g.genesis.Store(nil)
}
