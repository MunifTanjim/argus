package parser

import (
	"sort"
	"sync"
	"time"
)

// SessionCache caches session metadata keyed by (path, modTime); a changed
// modTime triggers a rescan. Avoids rescanning unchanged files on refresh.
type SessionCache struct {
	mu      sync.Mutex
	entries map[string]cachedSession
}

type cachedSession struct {
	modTime time.Time
	meta    sessionMetadata
}

// NewSessionCache returns an empty cache ready for use.
func NewSessionCache() *SessionCache {
	return &SessionCache{
		entries: make(map[string]cachedSession),
	}
}

// getOrScan returns cached metadata on a modTime match, else rescans.
func (c *SessionCache) getOrScan(path string, modTime time.Time) sessionMetadata {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.entries[path]; ok && cached.modTime.Equal(modTime) {
		return cached.meta
	}

	meta := scanSessionMetadata(path)
	c.entries[path] = cachedSession{modTime: modTime, meta: meta}
	return meta
}

// DiscoverProjectSessions is the cached variant of the standalone function.
func (c *SessionCache) DiscoverProjectSessions(projectDir string) ([]SessionInfo, error) {
	return discoverSessions(projectDir, c.getOrScan)
}

// DiscoverAllProjectSessions is the cached variant of the standalone function.
func (c *SessionCache) DiscoverAllProjectSessions(projectDirs []string) ([]SessionInfo, error) {
	var all []SessionInfo
	for _, dir := range projectDirs {
		sessions, err := c.DiscoverProjectSessions(dir)
		if err != nil {
			continue
		}
		all = append(all, sessions...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].ModTime.After(all[j].ModTime)
	})

	return all, nil
}
