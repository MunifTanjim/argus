package trustlog

import (
	"bytes"
	"errors"
)

// Store holds a verified trust-log chain, pinned to a genesis head learned
// out-of-band. Ingest adopts a candidate only if it is a same-genesis,
// prefix-preserving, strictly-longer, fully-verified extension of the current
// chain — the rollback/fork/tamper defense for chains relayed by an untrusted
// gateway. A Store is not safe for concurrent use.
type Store struct {
	genesisHead []byte
	log         *Log
}

// NewStore pins the out-of-band genesis head. The store is empty until Ingest.
func NewStore(genesisHead []byte) *Store {
	return &Store{genesisHead: append([]byte(nil), genesisHead...)}
}

// Ingest decodes, verifies, and adopts a candidate chain when it is a valid,
// same-genesis, monotonic extension of the current chain; otherwise it errors
// (or is a no-op if identical). Never adopts a shorter or divergent chain.
func (s *Store) Ingest(chainBytes []byte) error {
	entries, err := UnmarshalChain(chainBytes)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("trustlog: empty chain")
	}
	// Cheap genesis-pin check first — reject a wrong-genesis chain before the
	// expensive full-chain signature verification in Load.
	if !bytes.Equal(hashEntry(&entries[0]), s.genesisHead) {
		return errors.New("trustlog: candidate genesis does not match pinned head")
	}
	cand, err := Load(entries) // verifies signatures, links, signer trust
	if err != nil {
		return err
	}
	if s.log != nil {
		cur := s.log.Entries()
		if len(entries) < len(cur) {
			return errors.New("trustlog: candidate shorter than current (rollback)")
		}
		for i := range cur {
			if !bytes.Equal(hashEntry(&cur[i]), hashEntry(&entries[i])) {
				return errors.New("trustlog: candidate diverges from current chain (fork)")
			}
		}
		if len(entries) == len(cur) {
			return nil // identical chain: no-op
		}
	}
	s.log = cand
	return nil
}

// Bytes serializes the current chain (nil if nothing ingested yet).
func (s *Store) Bytes() []byte {
	if s.log == nil {
		return nil
	}
	return MarshalChain(s.log.Entries())
}

// Head returns the current chain head, or nil if empty.
func (s *Store) Head() []byte {
	if s.log == nil {
		return nil
	}
	return s.log.Head()
}

// Disabled reports whether the log has been disabled by a valid disablement secret.
func (s *Store) Disabled() bool { return s.log != nil && s.log.Disabled() }

// DeviceAuthorized reports whether pub is authorized by the current chain.
func (s *Store) DeviceAuthorized(pub []byte) bool {
	return s.log != nil && s.log.DeviceAuthorized(pub)
}

// SignerTrusted reports whether pub is a trusted signer in the current chain.
func (s *Store) SignerTrusted(pub []byte) bool {
	return s.log != nil && s.log.SignerTrusted(pub)
}

// Signers returns the current trusted signer set (empty if nothing ingested).
func (s *Store) Signers() [][]byte {
	if s.log == nil {
		return [][]byte{}
	}
	return s.log.Signers()
}

// Devices returns the current authorized device set (empty if nothing ingested).
func (s *Store) Devices() [][]byte {
	if s.log == nil {
		return [][]byte{}
	}
	return s.log.Devices()
}
