package trustlog

import "sync"

// maxRetainedEntries is a DoS backstop, not a retention policy: a locked network
// performs a handful of trust-log writes a year, so reaching this ceiling means a
// peer is misbehaving. Nothing is ever evicted — inserts past the ceiling are
// refused; callers log when the refused count is non-zero.
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

// MaxRetainedEntries is the EntryStore ceiling; exposed for tests in other packages.
const MaxRetainedEntries = maxRetainedEntries

// NewEntryStore returns an empty EntryStore.
func NewEntryStore() *EntryStore {
	return &EntryStore{}
}

// SetCountForTest directly sets the entry count. For tests that need a full store.
func (s *EntryStore) SetCountForTest(n int) {
	s.mu.Lock()
	s.count = n
	s.mu.Unlock()
}

// Put stores a single raw entry. stored is true when the entry was newly added.
// refused is true specifically when the store is at maxRetainedEntries; callers
// must log when refused is true. Duplicates and undecodable entries are silently
// skipped (stored=false, refused=false).
func (s *EntryStore) Put(raw []byte) (stored, refused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putLocked(raw)
}

// PutAll stores each raw entry, returning the count of newly added entries and the
// count refused because the store is at maxRetainedEntries. Duplicates and
// undecodable garbage contribute to neither count; callers must log when refused
// is non-zero.
func (s *EntryStore) PutAll(entries [][]byte) (added, refused int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, raw := range entries {
		stored, ref := s.putLocked(raw)
		if stored {
			added++
		}
		if ref {
			refused++
		}
	}
	return added, refused
}

// putLocked returns (stored, refused): stored when the entry was newly added,
// refused when the ceiling was the reason it was not.
func (s *EntryStore) putLocked(raw []byte) (stored, refused bool) {
	e, err := UnmarshalEntry(raw)
	if err != nil {
		return false, false
	}
	if s.byHash == nil {
		s.byHash = map[string][]byte{}
		s.prev = map[string]string{}
	}
	h := string(HashEntry(&e))
	if _, exists := s.byHash[h]; exists {
		return false, false
	}
	if s.count >= maxRetainedEntries {
		return false, true
	}
	s.byHash[h] = append([]byte(nil), raw...)
	s.prev[h] = string(e.Prev)
	s.count++
	return true, false
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
