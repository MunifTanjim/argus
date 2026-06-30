// Package clienttoken manages per-client gateway tokens: one long-lived random
// secret per paired device, each its own file so a device can be revoked without
// rotating the shared gateway token.
//
// A token is "active" once its file exists; a freshly minted token is held
// "pending" in memory during the pairing window and promoted to a file the moment
// the device authenticates.
package clienttoken

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// tokenBytes is the entropy of a minted token; hex-encoded it is twice as wide.
const tokenBytes = 32

// PendTTL bounds how long a minted-but-unconnected token stays accepted.
const PendTTL = 60 * time.Second

// Record is one persisted client token.
type Record struct {
	Token     string `json:"token"`
	CreatedAt string `json:"created_at"`
}

// fileData is the on-disk shape of a token file.
type fileData struct {
	CreatedAt string `json:"created_at"`
}

// pending is an in-flight minted token awaiting its first connection. ch is
// closed once the device authenticates (see Authorize).
type pending struct {
	ch        chan struct{}
	connected bool
}

// Store is the set of client tokens backed by a directory. It is safe for
// concurrent use.
type Store struct {
	dir string
	now func() time.Time

	mu       sync.Mutex
	pendings map[string]*pending
}

// New returns a Store persisting tokens under dir (created lazily on first write).
func New(dir string) *Store {
	return &Store{dir: dir, now: time.Now, pendings: map[string]*pending{}}
}

// GenerateToken returns a fresh cryptographically-random hex token.
func GenerateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("clienttoken: generate: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// validToken guards against path traversal and junk: a valid token is exactly the
// hex encoding of tokenBytes (lowercase 0-9a-f), so it's always a safe filename.
func validToken(tok string) bool {
	if len(tok) != tokenBytes*2 {
		return false
	}
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) path(tok string) string { return filepath.Join(s.dir, tok+".json") }

// Pend registers a minted token as accepted for the pairing window and returns a
// channel closed when a device first authenticates. Auto-dropped after PendTTL;
// callers should also CancelPend on their own timeout.
func (s *Store) Pend(tok string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &pending{ch: make(chan struct{})}
	s.pendings[tok] = p
	time.AfterFunc(PendTTL, func() { s.expire(tok, p) })
	return p.ch
}

// expire drops a still-pending, never-connected token once its window elapses.
func (s *Store) expire(tok string, p *pending) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.pendings[tok]; ok && cur == p && !p.connected {
		delete(s.pendings, tok)
	}
}

// CancelPend removes a pending token that never connected. No-op once promoted.
func (s *Store) CancelPend(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pendings[tok]; ok && !p.connected {
		delete(s.pendings, tok)
	}
}

// Connected reports whether a pending token has been claimed by a device.
func (s *Store) Connected(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pendings[tok]
	return ok && p.connected
}

// Authorize reports whether tok may open a /client connection. A pending token is
// promoted (file persisted, waiter signalled); an active token is accepted if its
// file still exists.
func (s *Store) Authorize(tok string) bool {
	if !validToken(tok) {
		return false
	}
	s.mu.Lock()
	if p, ok := s.pendings[tok]; ok {
		if err := s.persist(tok); err != nil {
			s.mu.Unlock()
			return false // can't durably record it; refuse rather than half-pair
		}
		delete(s.pendings, tok)
		if !p.connected {
			p.connected = true
			close(p.ch)
		}
		s.mu.Unlock()
		return true
	}
	s.mu.Unlock()

	_, err := os.Stat(s.path(tok))
	return err == nil
}

// persist writes the token's file. Caller holds s.mu.
func (s *Store) persist(tok string) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(fileData{CreatedAt: s.now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(tok), b, 0o600)
}

// List returns the persisted tokens, newest first.
func (s *Store) List() ([]Record, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		tok := name[:len(name)-len(".json")]
		var fd fileData
		if b, rerr := os.ReadFile(filepath.Join(s.dir, name)); rerr == nil {
			_ = json.Unmarshal(b, &fd)
		}
		out = append(out, Record{Token: tok, CreatedAt: fd.CreatedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// Remove deletes a client token's file, revoking it for future connections.
func (s *Store) Remove(tok string) error {
	if !validToken(tok) {
		return fmt.Errorf("clienttoken: invalid token")
	}
	if err := os.Remove(s.path(tok)); err != nil {
		return err
	}
	return nil
}
