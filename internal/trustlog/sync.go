package trustlog

import (
	"bytes"
	"sync"
)

// SyncStore is a concurrency-safe wrapper over Store for the distribution layer,
// where a background sync goroutine ingests chains relayed by the gateway while
// other goroutines query authorization state. Store itself is single-threaded by
// contract; every access here holds the mutex.
type SyncStore struct {
	mu    sync.Mutex
	store *Store
}

// NewSyncStore pins the out-of-band genesis head. Empty until the first Ingest.
func NewSyncStore(genesisHead []byte) *SyncStore {
	return &SyncStore{store: NewStore(genesisHead)}
}

// Ingest adopts a candidate chain via the pinned Store. changed reports whether
// the verified HEAD advanced (so a caller can persist / act only on real change).
// An identical chain is a no-op (changed=false, err=nil); a rollback/fork/tamper
// or wrong-genesis chain returns an error and leaves state untouched.
func (s *SyncStore) Ingest(chain []byte) (changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.store.Head()
	if err := s.store.Ingest(chain); err != nil {
		return false, err
	}
	return !bytes.Equal(before, s.store.Head()), nil
}

// Bytes returns the current marshaled chain (nil if nothing ingested yet).
func (s *SyncStore) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Bytes()
}

// Head returns the current verified HEAD (nil if empty).
func (s *SyncStore) Head() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Head()
}

// DeviceAuthorized reports whether pub is authorized by the current chain.
func (s *SyncStore) DeviceAuthorized(pub []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.DeviceAuthorized(pub)
}

// SignerTrusted reports whether pub is a trusted signer in the current chain.
func (s *SyncStore) SignerTrusted(pub []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.SignerTrusted(pub)
}
