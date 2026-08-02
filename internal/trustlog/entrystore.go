package trustlog

import "sync"

// maxRetainedEntries is a DoS backstop, not a retention policy: a locked network
// performs a handful of trust-log writes a year, so reaching this ceiling means a
// peer is misbehaving. Nothing is ever evicted — inserts past the ceiling are
// refused and logged by the caller.
const maxRetainedEntries = 1 << 16

// EntryStore holds the network's trust-log entries keyed by entry hash. It is
// intentionally blind: it parses only each entry's own hash and its Prev pointer,
// never a signature, a kind, or any payload.
//
// Lifetime coupling: the retained entry set and the advertised head set must share
// the same lifetime. After a restart both are empty, so the node advertises nothing
// and receives everything from its peers — no partial-sync gap can arise.
type EntryStore struct {
	mu     sync.Mutex
	byHash map[string][]byte // entry hash -> raw marshalled entry
	prev   map[string]string // entry hash -> parent hash ("" for genesis)
	count  int
}

// NewEntryStore returns an empty EntryStore.
func NewEntryStore() *EntryStore {
	return &EntryStore{}
}

// Put stores a single raw entry. It returns false if the entry fails to decode,
// is already held, or the store has reached maxRetainedEntries.
func (s *EntryStore) Put(raw []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putLocked(raw)
}

// PutAll stores each raw entry in entries, returning the count of newly added entries.
func (s *EntryStore) PutAll(entries [][]byte) int {
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

func (s *EntryStore) putLocked(raw []byte) bool {
	e, err := UnmarshalEntry(raw)
	if err != nil {
		return false
	}
	if s.byHash == nil {
		s.byHash = map[string][]byte{}
		s.prev = map[string]string{}
	}
	h := string(HashEntry(&e))
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

// Heads returns the hash of every retained entry that no other retained entry
// names as its Prev.
func (s *EntryStore) Heads() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headsLocked()
}

func (s *EntryStore) headsLocked() [][]byte {
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

// Delta returns every retained entry the caller cannot reach by walking Prev from
// its known heads, parents before children, plus any caller heads this store does
// not hold. Holding a head implies holding its ancestry, so the head list alone is
// enough to compute what the caller is missing.
func (s *EntryStore) Delta(knownHeads [][]byte) (entries [][]byte, want [][]byte) {
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
