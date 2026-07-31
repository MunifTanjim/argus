package gateway

import (
	"sort"
	"sync"

	"golang.org/x/crypto/blake2s"

	"github.com/MunifTanjim/argus/internal/trustlog"
)

// trustStoreCap is the maximum number of distinct competing branches the gateway
// retains at once. When a new branch would push the count over cap the branch with
// the fewest entries is evicted, so higher-value (longer) branches survive.
const trustStoreCap = 4

// branchEntry is one retained branch inside trustStore.
type branchEntry struct {
	bytes []byte
	count int // number of decoded entries (used for eviction and ordering)
}

// trustStore is the gateway's opaque hold of the network's trust-log chain. The
// gateway is untrusted and blind: it never verifies signatures and only parses
// the entry count via the DoS-capped trustlog.UnmarshalChain decoder.
//
// It retains up to trustStoreCap distinct competing branches so that clients can
// receive all live forks and resolve them locally (fork-choice lives on the client,
// not the gateway). Branches are keyed by the blake2s-256 hash of their raw chain
// bytes — a purely content-addressed, blind fingerprint. Within one key the
// entry-count winner is kept; across keys all branches are held up to the cap,
// and the smallest-count branch is evicted when the cap is exceeded.
type trustStore struct {
	mu       sync.Mutex
	branches map[[32]byte]branchEntry
}

// chainKey returns the blake2s-256 fingerprint of the raw chain bytes. The gateway
// uses this as a branch identity without decoding or verifying any entry internals.
func chainKey(chain []byte) [32]byte {
	return blake2s.Sum256(chain)
}

// offer ingests a raw chain: parse the entry count (DoS-capped; blind — no
// signature check), fingerprint by blake2s, and update or insert the branch.
// When inserting a new branch would exceed trustStoreCap the branch with the
// fewest entries is evicted. Unparseable chains are silently dropped.
// inserted is true and fp holds the branch fingerprint only when a key not
// previously held was stored.
func (t *trustStore) offer(chain []byte) (inserted bool, fp [32]byte) {
	entries, err := trustlog.UnmarshalChain(chain)
	if err != nil {
		return false, fp
	}
	count := len(entries)
	key := chainKey(chain)

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.branches == nil {
		t.branches = make(map[[32]byte]branchEntry)
	}

	if existing, ok := t.branches[key]; ok {
		// Same content fingerprint: update only if this offer has strictly more
		// entries (same bytes ⇒ same count, so this is effectively a no-op for
		// identical re-offers; guards against the degenerate case).
		if count > existing.count {
			t.branches[key] = branchEntry{bytes: append([]byte(nil), chain...), count: count}
		}
		return false, fp
	}

	// New branch: insert, then evict the smallest-count branch if over cap.
	t.branches[key] = branchEntry{bytes: append([]byte(nil), chain...), count: count}
	if len(t.branches) > trustStoreCap {
		var minKey [32]byte
		minCount := -1
		for k, v := range t.branches {
			if minCount < 0 || v.count < minCount {
				minKey = k
				minCount = v.count
			}
		}
		delete(t.branches, minKey)
	}
	return true, key
}

// diff returns the retained branches whose fingerprint is absent from known,
// longest first, together with the fingerprints of every branch held. Callers use
// the fingerprint list to notice a branch the gateway lost (restart, cap eviction)
// and re-offer it.
//
// The comparison is over opaque content hashes: the gateway learns nothing about a
// chain by performing it.
func (t *trustStore) diff(known [][]byte) (chains [][]byte, fingerprints [][]byte) {
	skip := make(map[[32]byte]bool, len(known))
	for _, k := range known {
		if len(k) != 32 {
			continue
		}
		var key [32]byte
		copy(key[:], k)
		skip[key] = true
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	type keyed struct {
		key   [32]byte
		entry branchEntry
	}
	es := make([]keyed, 0, len(t.branches))
	for k, v := range t.branches {
		es = append(es, keyed{key: k, entry: v})
	}
	sort.Slice(es, func(i, j int) bool { return es[i].entry.count > es[j].entry.count })

	fingerprints = make([][]byte, 0, len(es))
	for _, e := range es {
		fp := e.key
		fingerprints = append(fingerprints, append([]byte(nil), fp[:]...))
		if skip[e.key] {
			continue
		}
		chains = append(chains, append([]byte(nil), e.entry.bytes...))
	}
	return chains, fingerprints
}

// fingerprints returns the content hash of every retained branch.
func (t *trustStore) fingerprints() [][]byte {
	_, fps := t.diff(nil)
	return fps
}
