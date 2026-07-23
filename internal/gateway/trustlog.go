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
func (t *trustStore) offer(chain []byte) {
	entries, err := trustlog.UnmarshalChain(chain)
	if err != nil {
		return
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
		return
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
}

// all returns copies of all retained branch bytes, ordered by descending entry
// count (longest branch first). Returns nil when no chains have been offered yet.
func (t *trustStore) all() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.branches) == 0 {
		return nil
	}
	es := make([]branchEntry, 0, len(t.branches))
	for _, v := range t.branches {
		es = append(es, v)
	}
	sort.Slice(es, func(i, j int) bool { return es[i].count > es[j].count })
	out := make([][]byte, len(es))
	for i, e := range es {
		out[i] = append([]byte(nil), e.bytes...)
	}
	return out
}
