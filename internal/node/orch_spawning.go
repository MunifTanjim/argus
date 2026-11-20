package node

import (
	"sync"
	"time"
)

// spawningKeys holds the actor keys of children this node spawned that it has
// not seen live yet, with their spawn times. A doomed key was killed before it
// started: it is killed once it shows up live.
type spawningKeys struct {
	mu     sync.Mutex
	at     map[string]time.Time
	doomed map[string]bool
}

func newSpawningKeys() *spawningKeys {
	return &spawningKeys{at: map[string]time.Time{}, doomed: map[string]bool{}}
}

func (s *spawningKeys) doom(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.doomed[key] = true
}

func (s *spawningKeys) add(key string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.at[key] = t
}

func (s *spawningKeys) pending(key string, now time.Time, window time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.at[key]
	return ok && now.Sub(at) < window
}

// settle forgets the keys that are live or whose window ended. It returns the
// expired keys that were not doomed, and the doomed keys that are live now.
func (s *spawningKeys) settle(live map[string]bool, now time.Time, window time.Duration) (expired, doomedLive []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, at := range s.at {
		switch {
		case live[key]:
			if s.doomed[key] {
				doomedLive = append(doomedLive, key)
			}
		case now.Sub(at) >= window:
			if !s.doomed[key] {
				expired = append(expired, key)
			}
		default:
			continue
		}
		delete(s.at, key)
		delete(s.doomed, key)
	}
	return expired, doomedLive
}
