package trustlog

import (
	"bytes"
	"errors"
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

// Signers returns the current trusted signer set.
func (s *SyncStore) Signers() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Signers()
}

// Devices returns the current authorized device set.
func (s *SyncStore) Devices() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Devices()
}

// Disabled reports whether the log has been disabled by a valid disablement secret.
func (s *SyncStore) Disabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Disabled()
}

// Disable appends a KindDisable entry revealing secret, signed by by. changed
// reports whether the chain actually advanced (it always does on success, since a
// disable is terminal and irreversible). Returns an error if the secret is unknown,
// the store is empty, or the log is already disabled.
func (s *SyncStore) Disable(secret []byte, by SignerKey) (changed bool, err error) {
	return s.appendSigned(
		func(*Store) bool { return false }, // never a no-op: always attempt the disable
		func(l *Log) error { return l.Disable(secret, by) })
}

// appendSigned extends the live chain by one signer-signed entry under s.mu.
// alreadyDone is evaluated under the lock to provide atomic idempotency: if it
// returns true the call is a no-op (changed=false, err=nil). The cur==nil check
// runs before alreadyDone so an empty store still errors. mutate applies the
// authorize/revoke entry; a rejected mutate leaves state intact.
func (s *SyncStore) appendSigned(alreadyDone func(*Store) bool, mutate func(*Log) error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.store.Bytes()
	if cur == nil {
		return false, errors.New("trustlog: no chain to extend")
	}
	if alreadyDone(s.store) {
		return false, nil // idempotent no-op under the lock
	}
	entries, err := UnmarshalChain(cur)
	if err != nil {
		return false, err
	}
	log, err := Load(entries)
	if err != nil {
		return false, err
	}
	if err := mutate(log); err != nil {
		return false, err
	}
	before := s.store.Head()
	if err := s.store.Ingest(MarshalChain(log.Entries())); err != nil {
		return false, err
	}
	return !bytes.Equal(before, s.store.Head()), nil
}

// AuthorizeDevice appends a signer-signed authorization for devicePub, signed by by
// (which must be a currently-trusted signer), and re-adopts the extended chain.
func (s *SyncStore) AuthorizeDevice(devicePub []byte, by SignerKey) (changed bool, err error) {
	return s.appendSigned(
		func(st *Store) bool { return st.DeviceAuthorized(devicePub) },
		func(l *Log) error { return l.AuthorizeDevice(devicePub, by) })
}

// RevokeDevice appends a signer-signed revocation for devicePub, signed by by.
func (s *SyncStore) RevokeDevice(devicePub []byte, by SignerKey) (changed bool, err error) {
	return s.appendSigned(
		func(st *Store) bool { return !st.DeviceAuthorized(devicePub) },
		func(l *Log) error { return l.RevokeDevice(devicePub, by) })
}
