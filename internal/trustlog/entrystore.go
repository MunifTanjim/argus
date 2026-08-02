package trustlog

import "sync"

// maxRetainedEntries is a DoS backstop, not a retention policy: a locked network
// performs a handful of trust-log writes a year, so reaching this ceiling means a
// peer is misbehaving. Nothing is ever evicted — inserts past the ceiling are
// refused; callers log when the refused count is non-zero.
var maxRetainedEntries = 1 << 16

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

// SetMaxRetainedEntriesForTest overrides the entry store ceiling and returns the
// previous value. Test-only; restore with t.Cleanup so a lowered ceiling cannot
// leak into sibling tests.
func SetMaxRetainedEntriesForTest(n int) int {
	prev := maxRetainedEntries
	maxRetainedEntries = n
	return prev
}

// maxOfferedHashes caps how many hashes a sync offer lists. Two orders of
// magnitude above any realistic log, and well below maxRetainedEntries, so
// truncation is unreachable without an attack. Truncating is safe: the protocol
// is set subtraction, so an under-reporting caller receives entries it already
// holds and they dedupe by hash on arrival.
var maxOfferedHashes = 4096

// SetMaxOfferedHashesForTest overrides the offer cap and returns the previous
// value. Test-only; restore it when the test ends.
func SetMaxOfferedHashesForTest(n int) int {
	prev := maxOfferedHashes
	maxOfferedHashes = n
	return prev
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

// Hashes lists the hash of every retained entry, for a sync offer. truncated is
// true when the store holds more than maxOfferedHashes and the list is partial.
func (s *EntryStore) Hashes() (hashes [][]byte, truncated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for h := range s.byHash {
		if len(hashes) >= maxOfferedHashes {
			return hashes, true
		}
		hashes = append(hashes, []byte(h))
	}
	return hashes, false
}

// All returns every retained entry, parents before children.
func (s *EntryStore) All() [][]byte {
	entries, _, _ := s.Delta(nil)
	return entries
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

// Delta returns every retained entry whose hash the caller did not list, parents
// before children, plus the listed hashes this store does not hold. disjoint
// reports that a non-empty known shares no entry with this store — the caller is
// almost certainly following a different trust root.
//
// This is set subtraction, not inference: the gateway does not assume the caller
// holds a listed entry's ancestry. A caller that under-reports simply receives
// entries it already has, which dedupe on arrival.
func (s *EntryStore) Delta(known [][]byte) (entries [][]byte, want [][]byte, disjoint bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	held := make(map[string]bool, len(known))
	shared := 0
	for _, h := range known {
		key := string(h)
		if _, ok := s.byHash[key]; ok {
			held[key] = true
			shared++
			continue
		}
		want = append(want, append([]byte(nil), h...))
	}

	emitted := map[string]bool{}
	var emit func(h string)
	emit = func(h string) {
		if h == "" || emitted[h] {
			return
		}
		if _, ok := s.byHash[h]; !ok {
			return
		}
		// Mark visited before recursing so a prev cycle terminates here rather
		// than overflowing the stack. Parents-before-children still holds: the
		// append is after emit(prev) returns, so ancestors are always emitted first.
		emitted[h] = true
		// Always recurse into prev so ancestors of a held hash are not withheld;
		// skip only the append when held, not the descent.
		emit(s.prev[h])
		if !held[h] {
			entries = append(entries, append([]byte(nil), s.byHash[h]...))
		}
	}
	for _, h := range s.headsLocked() {
		emit(string(h))
	}
	return entries, want, len(known) > 0 && shared == 0
}
