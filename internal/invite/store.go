package invite

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

// MaxTTL is the longest an invite can stay valid.
const MaxTTL = 30 * 24 * time.Hour

// retention is how long used, revoked and expired invites stay in the store.
const retention = 7 * 24 * time.Hour

type State string

const (
	StatePending State = "pending"
	StateUsed    State = "used"
	StateRevoked State = "revoked"
)

var (
	ErrUnknown = errors.New("unknown invite")
	ErrExpired = errors.New("invite expired")
	ErrUsed    = errors.New("invite already used")
	ErrRevoked = errors.New("invite revoked")
)

// IsRejection reports whether err is one of the invite's own refusals
// (unknown, revoked, used or expired) rather than a storage failure.
func IsRejection(err error) bool {
	return errors.Is(err, ErrUnknown) || errors.Is(err, ErrRevoked) || errors.Is(err, ErrUsed) || errors.Is(err, ErrExpired)
}

type Invite struct {
	ID        []byte
	Secret    []byte
	CreatedAt time.Time
	ExpiresAt time.Time
	State     State
	UsedAt    time.Time
	Keys      [][]byte
}

func (i Invite) Expired(now time.Time) bool {
	return i.State == StatePending && !now.Before(i.ExpiresAt)
}

func (i Invite) clone() Invite {
	i.ID = slices.Clone(i.ID)
	i.Secret = slices.Clone(i.Secret)
	i.Keys = cloneKeys(i.Keys)
	return i
}

func cloneKeys(keys [][]byte) [][]byte {
	if len(keys) == 0 {
		return nil
	}
	out := make([][]byte, len(keys))
	for n, k := range keys {
		out[n] = slices.Clone(k)
	}
	return out
}

// pruneable reports whether the invite has been finished for longer than the
// retention period. Revoked invites carry no revocation time, so they are
// kept until their expiry plus the retention period.
func (i Invite) pruneable(now time.Time) bool {
	end := i.ExpiresAt
	if i.State == StateUsed {
		end = i.UsedAt
	}
	return now.Sub(end) > retention
}

type Store struct {
	mu      sync.Mutex
	path    string
	now     func() time.Time
	invites []Invite
}

// OpenStore loads the store at path. An absent file is an empty store; a
// corrupt file is an error and is left untouched.
func OpenStore(path string, now func() time.Time) (*Store, error) {
	s := &Store{path: path, now: now}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(data, &s.invites); err != nil {
		return nil, fmt.Errorf("read invites %s: %w", path, err)
	}
	return s, nil
}

// commit persists invites and adopts them only once the write succeeds, so a
// failed save leaves the store as it was.
func (s *Store) commit(invites []Invite) error {
	data, err := json.Marshal(invites)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.path, data); err != nil {
		return err
	}
	s.invites = invites
	return nil
}

func (s *Store) index(id []byte) int {
	return slices.IndexFunc(s.invites, func(i Invite) bool { return string(i.ID) == string(id) })
}

func (s *Store) find(id []byte) *Invite {
	if n := s.index(id); n >= 0 {
		return &s.invites[n]
	}
	return nil
}

// update commits a copy of the invites with invite n changed by fn.
func (s *Store) update(n int, fn func(*Invite)) error {
	next := slices.Clone(s.invites)
	fn(&next[n])
	return s.commit(next)
}

func (s *Store) Create(ttl time.Duration) (Invite, error) {
	if ttl <= 0 || ttl > MaxTTL {
		return Invite{}, errors.New("invite ttl must be from 1s to 30 days")
	}
	inv := Invite{ID: make([]byte, 16), Secret: make([]byte, 32), State: StatePending}
	if _, err := rand.Read(inv.ID); err != nil {
		return Invite{}, err
	}
	if _, err := rand.Read(inv.Secret); err != nil {
		return Invite{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inv.CreatedAt = s.now()
	inv.ExpiresAt = inv.CreatedAt.Add(ttl)
	if err := s.commit(append(s.pruned(inv.CreatedAt), inv)); err != nil {
		return Invite{}, err
	}
	return inv.clone(), nil
}

// pruned returns a copy of the invites without those finished for more than
// the retention period.
func (s *Store) pruned(now time.Time) []Invite {
	return slices.DeleteFunc(slices.Clone(s.invites), func(i Invite) bool { return i.pruneable(now) })
}

// Now is the store's clock, the one expiry is judged by.
func (s *Store) Now() time.Time { return s.now() }

// Secret returns the secret of a known invite in any state.
func (s *Store) Secret(id []byte) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv := s.find(id)
	if inv == nil {
		return nil, false
	}
	return slices.Clone(inv.Secret), true
}

// Consume marks a pending, unexpired invite used and persists it.
func (s *Store) Consume(id []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.index(id)
	if n < 0 {
		return ErrUnknown
	}
	now := s.now()
	switch inv := s.invites[n]; {
	case inv.State == StateRevoked:
		return ErrRevoked
	case inv.State == StateUsed:
		return ErrUsed
	case inv.Expired(now):
		return ErrExpired
	}
	return s.update(n, func(i *Invite) { i.State, i.UsedAt = StateUsed, now })
}

func (s *Store) RecordKeys(id []byte, keys [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.index(id)
	if n < 0 {
		return ErrUnknown
	}
	return s.update(n, func(i *Invite) { i.Keys = cloneKeys(keys) })
}

func (s *Store) Revoke(id []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.index(id)
	if n < 0 {
		return ErrUnknown
	}
	switch s.invites[n].State {
	case StateUsed:
		return ErrUsed
	case StateRevoked:
		return nil
	}
	return s.update(n, func(i *Invite) { i.State = StateRevoked })
}

// List drops invites finished for more than the retention period, then
// returns the rest, newest first.
func (s *Store) List() ([]Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kept := s.pruned(s.now()); len(kept) != len(s.invites) {
		if err := s.commit(kept); err != nil {
			return nil, err
		}
	}
	out := make([]Invite, len(s.invites))
	for n, i := range s.invites {
		out[n] = i.clone()
	}
	slices.SortStableFunc(out, func(a, b Invite) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}
