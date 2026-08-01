package gateway

import (
	"sync"

	"github.com/MunifTanjim/argus/internal/trustlog"
)

// maxRetainedEntries bounds the entry store. It is a denial-of-service backstop,
// not a retention policy: a locked network performs a handful of trust-log writes
// a year, so reaching this means a peer is misbehaving. Nothing is ever evicted —
// inserts past the ceiling are refused and logged by the caller.
const maxRetainedEntries = 1 << 16

// entryStore is the gateway's opaque hold of the network's trust log, keyed by
// entry hash. The gateway is blind: it parses only each entry's own hash and its
// Prev pointer, never a signature, a kind, or any payload. That is strictly less
// than the whole-chain store it replaces, which also learned which chains carried
// a disable.
//
// Storing entries rather than chain snapshots is what makes an appended entry
// impossible to lose: an append adds one object and moves a head, where a snapshot
// store created a whole competing branch per write and evicted under a cap.
type entryStore struct {
	mu     sync.Mutex
	byHash map[string][]byte // entry hash -> raw marshalled entry
	prev   map[string]string // entry hash -> parent hash ("" for genesis)
	count  int
}

func (s *entryStore) put(raw []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putLocked(raw)
}

func (s *entryStore) putAll(entries [][]byte) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, raw := range entries {
		if s.putLocked(raw) {
			n++
		}
	}
	return n
}

func (s *entryStore) putLocked(raw []byte) bool {
	e, err := trustlog.UnmarshalEntry(raw)
	if err != nil {
		return false
	}
	if s.byHash == nil {
		s.byHash = map[string][]byte{}
		s.prev = map[string]string{}
	}
	h := string(trustlog.HashEntry(&e))
	if _, exists := s.byHash[h]; exists {
		return false
	}
	if s.count >= maxRetainedEntries {
		return false
	}
	s.byHash[h] = append([]byte(nil), raw...)
	s.prev[h] = string(e.Prev)
	s.count++
	return true
}

// heads returns the hash of every retained entry that no other retained entry
// names as its Prev.
func (s *entryStore) heads() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headsLocked()
}

func (s *entryStore) headsLocked() [][]byte {
	referenced := make(map[string]bool, len(s.prev))
	for _, p := range s.prev {
		if p != "" {
			referenced[p] = true
		}
	}
	out := make([][]byte, 0, len(s.byHash))
	for h := range s.byHash {
		if !referenced[h] {
			out = append(out, []byte(h))
		}
	}
	return out
}

// delta returns every retained entry the caller cannot reach by walking Prev from
// its heads, parents before children, plus the caller heads this store does not
// hold. Holding a head implies holding its ancestry, so the head list alone is
// enough to compute what the caller is missing.
func (s *entryStore) delta(knownHeads [][]byte) (entries [][]byte, want [][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	reachable := map[string]bool{}
	for _, h := range knownHeads {
		key := string(h)
		if _, ok := s.byHash[key]; !ok {
			want = append(want, append([]byte(nil), h...))
			continue
		}
		for cur := key; cur != ""; {
			if reachable[cur] {
				break
			}
			reachable[cur] = true
			cur = s.prev[cur]
		}
	}

	emitted := map[string]bool{}
	var emit func(h string)
	emit = func(h string) {
		if h == "" || emitted[h] || reachable[h] {
			return
		}
		if _, ok := s.byHash[h]; !ok {
			return
		}
		emit(s.prev[h])
		emitted[h] = true
		entries = append(entries, append([]byte(nil), s.byHash[h]...))
	}
	for _, h := range s.headsLocked() {
		emit(string(h))
	}
	return entries, want
}
