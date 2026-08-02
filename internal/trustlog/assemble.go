package trustlog

// ChainEntries splits a marshalled chain into its individually marshalled
// entries, in chain order.
func ChainEntries(chain []byte) ([][]byte, error) {
	entries, err := UnmarshalChain(chain)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(entries))
	for i := range entries {
		out = append(out, MarshalEntry(entries[i]))
	}
	return out, nil
}

// AssembleChainsReport is AssembleChains plus the number of input entries that
// could not be placed in any complete genesis-rooted chain.
//
// unplaced counts distinct, decodable input entries whose hash does not appear
// in any returned chain. Entries that fail to decode are not counted — they are
// garbage, not evidence of a missing ancestor. Duplicates count once.
//
// A non-zero unplaced count with a non-empty heads set sent to the gateway is a
// signal that the gateway withheld ancestors it believed the caller already held
// (because the caller advertised those heads). The caller should clear its
// branch cache and retry once with nil heads so the gateway sends the full
// ancestry.
func AssembleChainsReport(entries [][]byte) (chains [][]byte, unplaced int) {
	byHash := map[string]Entry{}
	var order []string
	for _, raw := range entries {
		e, err := UnmarshalEntry(raw)
		if err != nil {
			continue
		}
		h := string(HashEntry(&e))
		if _, seen := byHash[h]; seen {
			continue
		}
		byHash[h] = e
		order = append(order, h)
	}

	referenced := map[string]bool{}
	for _, e := range byHash {
		if len(e.Prev) > 0 {
			referenced[string(e.Prev)] = true
		}
	}

	placed := map[string]bool{}
	for _, h := range order {
		if referenced[h] {
			continue
		}
		chain, ok := walkToGenesis(byHash, h)
		if !ok {
			continue
		}
		for i := range chain {
			placed[string(HashEntry(&chain[i]))] = true
		}
		chains = append(chains, MarshalChain(chain))
	}
	for h := range byHash {
		if !placed[h] {
			unplaced++
		}
	}
	return chains, unplaced
}

// AssembleChains groups individually marshalled entries into complete
// genesis-rooted chains, each encoded with MarshalChain. Entries that do not
// decode are ignored, and a branch whose head cannot walk Prev back to a
// nil-Prev genesis is skipped: a chain with a hole cannot be verified, so
// returning it half-built would only push the failure downstream.
func AssembleChains(entries [][]byte) [][]byte {
	chains, _ := AssembleChainsReport(entries)
	return chains
}

// walkToGenesis follows Prev from head back to a nil-Prev genesis, returning the
// chain in genesis-first order. ok is false when a link is missing, or when the
// walk exceeds the number of entries held — which means the Prev pointers form a
// cycle rather than a chain.
func walkToGenesis(byHash map[string]Entry, head string) ([]Entry, bool) {
	var reversed []Entry
	cur := head
	for range byHash {
		e, ok := byHash[cur]
		if !ok {
			return nil, false
		}
		reversed = append(reversed, e)
		if len(e.Prev) == 0 {
			out := make([]Entry, 0, len(reversed))
			for i := len(reversed) - 1; i >= 0; i-- {
				out = append(out, reversed[i])
			}
			return out, true
		}
		cur = string(e.Prev)
	}
	return nil, false
}
