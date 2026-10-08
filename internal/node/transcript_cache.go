package node

import (
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/transcript"
)

const (
	transcriptCacheMaxIdle = 8
	transcriptCacheIdleTTL = 10 * time.Minute
)

type transcriptKey struct {
	path       string
	isSubagent bool
}

// sharedTranscript is one folded transcript shared by every subscription to
// its file. Refresh is safe for concurrent use.
type sharedTranscript struct {
	mu          sync.Mutex
	st          adapter.StreamingTranscript
	last        []transcript.Entry
	refreshedAt time.Time

	// Guarded by transcriptCache.mu.
	key       transcriptKey
	refs      int
	idleSince time.Time
	idleGen   int
	timer     *time.Timer
}

// Refresh reuses a fold from the last half poll interval, so subscriptions
// that poll the same file do not each re-fold it.
func (s *sharedTranscript) Refresh() ([]transcript.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.refreshedAt.IsZero() && time.Since(s.refreshedAt) < transcriptPollInterval/2 {
		return s.last, nil
	}
	entries, err := s.st.Refresh()
	if err != nil {
		return nil, err
	}
	s.last, s.refreshedAt = entries, time.Now()
	return entries, nil
}

// transcriptCache keeps folded transcripts after their last subscriber leaves,
// so reopening a session reads only the lines appended since. An item without
// subscribers is evicted after idleTTL, or sooner when more than maxIdle such
// items exist (longest idle first).
type transcriptCache struct {
	mu      sync.Mutex
	items   map[transcriptKey]*sharedTranscript
	maxIdle int
	idleTTL time.Duration
}

func newTranscriptCache(maxIdle int, idleTTL time.Duration) *transcriptCache {
	return &transcriptCache{items: map[transcriptKey]*sharedTranscript{}, maxIdle: maxIdle, idleTTL: idleTTL}
}

func (c *transcriptCache) acquire(key transcriptKey, newStream func() adapter.StreamingTranscript) *sharedTranscript {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.items[key]
	if !ok {
		s = &sharedTranscript{st: newStream(), key: key}
		c.items[key] = s
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.refs++
	return s
}

func (c *transcriptCache) release(s *sharedTranscript) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s.refs--
	if s.refs > 0 {
		return
	}
	s.idleSince = time.Now()
	s.idleGen++
	// The generation check drops a timer that fired while a later acquire was
	// waiting for the lock.
	gen := s.idleGen
	s.timer = time.AfterFunc(c.idleTTL, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if s.refs == 0 && s.idleGen == gen {
			c.evict(s)
		}
	})
	c.enforceIdleLimit()
}

func (c *transcriptCache) enforceIdleLimit() {
	for {
		var oldest *sharedTranscript
		idle := 0
		for _, s := range c.items {
			if s.refs > 0 {
				continue
			}
			idle++
			if oldest == nil || s.idleSince.Before(oldest.idleSince) {
				oldest = s
			}
		}
		if idle <= c.maxIdle {
			return
		}
		c.evict(oldest)
	}
}

func (c *transcriptCache) evict(s *sharedTranscript) {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if c.items[s.key] == s {
		delete(c.items, s.key)
	}
}
