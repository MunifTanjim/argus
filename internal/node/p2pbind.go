package node

import (
	"sync"

	"github.com/MunifTanjim/argus/internal/clienttoken"
)

// maxBindingsPerToken bounds the keys bound to one token; past it the oldest
// is evicted. maxBindings bounds the store; past it the oldest binding overall
// is evicted. A key with a live direct channel is never evicted, so the caps
// may be exceeded by at most the number of direct channels.
const (
	maxBindingsPerToken = 8
	maxBindings         = 1024
)

// bindingStore maps client Noise static keys to the token their relay channel
// was opened with. A key is admitted only while its token is valid: in the
// gateway's paired-token list, or the node's own master token. One store
// lives per uplink and is cleared when the uplink closes.
type bindingStore struct {
	mu     sync.Mutex
	byKey  map[string]binding
	byHash map[string][]string // keys per token hash, oldest first
	valid  map[string]bool     // paired client token hashes
	pinned map[string]int      // keys with live direct channels, by channel count
	master string              // hash of the node's master token; "" when unknown
	seq    uint64
}

type binding struct {
	hash string
	seq  uint64 // bind order, for total-cap eviction
}

func newBindingStore(masterHash string) *bindingStore {
	return &bindingStore{
		byKey:  map[string]binding{},
		byHash: map[string][]string{},
		valid:  map[string]bool{},
		pinned: map[string]int{},
		master: masterHash,
	}
}

// bind records that key completed a relay handshake on a channel opened with
// tokenHash. Rebinding a key moves it to tokenHash as its newest binding.
func (s *bindingStore) bind(key []byte, tokenHash string) {
	if len(key) == 0 || tokenHash == "" {
		return
	}
	k := string(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byKey[k]; ok {
		s.removeLocked(k)
	}
	if keys := s.byHash[tokenHash]; len(keys) >= maxBindingsPerToken {
		for _, kk := range keys {
			if s.pinned[kk] == 0 {
				s.removeLocked(kk)
				break
			}
		}
	}
	if len(s.byKey) >= maxBindings {
		var oldest string
		var oldestSeq uint64
		for ok, b := range s.byKey {
			if s.pinned[ok] == 0 && (oldest == "" || b.seq < oldestSeq) {
				oldest, oldestSeq = ok, b.seq
			}
		}
		s.removeLocked(oldest)
	}
	s.seq++
	s.byKey[k] = binding{hash: tokenHash, seq: s.seq}
	s.byHash[tokenHash] = append(s.byHash[tokenHash], k)
}

func (s *bindingStore) removeLocked(k string) {
	b, ok := s.byKey[k]
	if !ok {
		return
	}
	delete(s.byKey, k)
	keys := s.byHash[b.hash]
	for i, kk := range keys {
		if kk == k {
			keys = append(keys[:i:i], keys[i+1:]...)
			break
		}
	}
	if len(keys) == 0 {
		delete(s.byHash, b.hash)
	} else {
		s.byHash[b.hash] = keys
	}
}

// pin keeps key's binding from eviction while a direct channel it opened is
// live; unpin releases it.
func (s *bindingStore) pin(key []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinned[string(key)]++
}

func (s *bindingStore) unpin(key []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := string(key)
	if s.pinned[k] <= 1 {
		delete(s.pinned, k)
	} else {
		s.pinned[k]--
	}
}

// setValid replaces the paired client token list and drops every binding
// whose token is no longer valid, returning the dropped keys.
func (s *bindingStore) setValid(hashes []string) (revoked [][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.valid = make(map[string]bool, len(hashes))
	for _, h := range hashes {
		if h != "" {
			s.valid[h] = true
		}
	}
	for h, keys := range s.byHash {
		if s.tokenValidLocked(h) {
			continue
		}
		for _, k := range keys {
			delete(s.byKey, k)
			revoked = append(revoked, []byte(k))
		}
		delete(s.byHash, h)
	}
	return revoked
}

// tokenValid reports whether hash is a paired client token or the master token.
func (s *bindingStore) tokenValid(hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenValidLocked(hash)
}

func (s *bindingStore) tokenValidLocked(hash string) bool {
	return hash != "" && (s.valid[hash] || hash == s.master)
}

// hashes returns every valid token hash: the paired client tokens and the
// master token.
func (s *bindingStore) hashes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.valid)+1)
	for h := range s.valid {
		out = append(out, h)
	}
	if s.master != "" && !s.valid[s.master] {
		out = append(out, s.master)
	}
	return out
}

// admits reports whether key is bound to a valid token.
func (s *bindingStore) admits(key []byte) bool {
	_, ok := s.validHash(key)
	return ok
}

// validHash returns the valid token key is bound to, or false when it is not.
func (s *bindingStore) validHash(key []byte) (hash string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byKey[string(key)]
	if !ok || !s.tokenValidLocked(b.hash) {
		return "", false
	}
	return b.hash, true
}

// clear drops every binding and the token list.
func (s *bindingStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.byKey)
	clear(s.byHash)
	clear(s.valid)
	clear(s.pinned)
}

func (s *bindingStore) countForTest() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byKey)
}

// SetMasterToken records the token this node connects to the gateway with,
// and its hash. The gateway never lists it, so the node vouches for it itself.
func (d *Node) SetMasterToken(tok string) {
	var h string
	if tok != "" {
		h = clienttoken.HashToken(tok)
	}
	d.masterHash.Store(&h)
	d.masterTok.Store(&tok)
}

// masterToken is the gateway token itself, which a plaintext node presents to
// open a direct peer channel.
func (d *Node) masterToken() string {
	if t := d.masterTok.Load(); t != nil {
		return *t
	}
	return ""
}

func (d *Node) masterTokenHash() string {
	if h := d.masterHash.Load(); h != nil {
		return *h
	}
	return ""
}

// bindings returns the current uplink's binding store, or nil without one.
func (d *Node) bindings() *bindingStore {
	if r := d.activeResponder.Load(); r != nil {
		return r.binds
	}
	return nil
}
