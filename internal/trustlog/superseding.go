package trustlog

import "bytes"

// SupersedingGenesis picks, out of chains, the genesis a device pinned to own should
// be told the network has moved to: the longest chain whose root is not own and which
// carries no disable entry, falling back to the first foreign root when every
// candidate is disabled. A gateway retains several competing branches — after a
// relock that includes the dead root the device already holds and any earlier
// abandoned one — so "the first chain that decodes" names the wrong root as often as
// the right one, and the operator compares that fingerprint out-of-band.
//
// Decode only, no verification: the answer is what to show an operator and what they
// will confirm themselves, not what to trust. Returns nil when no foreign root
// decodes.
func SupersedingGenesis(chains [][]byte, own []byte) []byte {
	var fallback, best []byte
	bestLen := 0
	for _, chain := range chains {
		entries, err := UnmarshalChain(chain)
		if err != nil || len(entries) == 0 {
			continue
		}
		genesis := HashEntry(&entries[0])
		if bytes.Equal(genesis, own) {
			continue
		}
		if fallback == nil {
			fallback = genesis
		}
		if ContainsDisable(entries) {
			continue
		}
		if len(entries) > bestLen {
			bestLen, best = len(entries), genesis
		}
	}
	if best != nil {
		return best
	}
	return fallback
}

// ContainsDisable reports whether entries contain a disable entry, without verifying
// that the revealed secret matches a commitment — an unverified chain's own claim to
// be dead is enough to rank it below a live candidate. Blind callers (the gateway)
// use it for the same ranking without learning anything they could not already parse.
func ContainsDisable(entries []Entry) bool {
	for i := range entries {
		if entries[i].Kind == KindDisable {
			return true
		}
	}
	return false
}
