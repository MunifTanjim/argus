// Package histcache is a disk-backed per-session metadata cache shared by the
// agent adapters' history list views. Listing past sessions otherwise re-scans
// every transcript on each open; entries here are keyed by the transcript file's
// mod time and size, so unchanged sessions are never re-scanned and the cache
// survives node restarts.
package histcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/session"
)

// schemaVersion invalidates all entries when the stored shape changes.
const schemaVersion = 1

// Entry is the cached list-card payload for one past session. Cwd is kept for
// project filtering (codex/antigravity group by cwd) though it isn't rendered.
type Entry struct {
	Session session.HistorySession `json:"session"`
	Cwd     string                 `json:"cwd"`
}

// diskEntry is the on-disk record: the entry plus the validity keys.
type diskEntry struct {
	Version int       `json:"version"`
	ModTime time.Time `json:"mod_time"`
	Size    int64     `json:"size"`
	Entry   Entry     `json:"entry"`
}

var (
	mu  sync.Mutex
	mem = map[string]diskEntry{} // L1, key = agent/sessionID
)

// Get returns a cached entry valid for the file at (modTime, size). ok is false
// on any miss: absent, stale, wrong schema, or unreadable/corrupt disk record.
func Get(agent, sessionID string, modTime time.Time, size int64) (Entry, bool) {
	key := memKey(agent, sessionID)

	mu.Lock()
	if d, ok := mem[key]; ok {
		if valid(d, modTime, size) {
			mu.Unlock()
			return d.Entry, true
		}
	}
	mu.Unlock()

	d, ok := readDisk(agent, sessionID)
	if !ok || !valid(d, modTime, size) {
		return Entry{}, false
	}
	mu.Lock()
	mem[key] = d
	mu.Unlock()
	return d.Entry, true
}

// Put writes the entry to memory and disk (atomically). Disk errors are ignored:
// a failed write just means the next read is a miss.
func Put(agent, sessionID string, modTime time.Time, size int64, e Entry) {
	d := diskEntry{Version: schemaVersion, ModTime: modTime, Size: size, Entry: e}

	mu.Lock()
	mem[memKey(agent, sessionID)] = d
	mu.Unlock()

	b, err := json.Marshal(d)
	if err != nil {
		return
	}
	path := diskPath(agent, sessionID)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	writeFileAtomic(path, b)
}

// Prune drops cache entries (memory and disk) for agent whose sessionID is not in
// live. Callers that enumerate every session per list pass their full id set.
func Prune(agent string, live map[string]struct{}) {
	dir := agentDir(agent)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if _, ok := live[id]; ok {
			continue
		}
		os.Remove(filepath.Join(dir, name))
		mu.Lock()
		delete(mem, memKey(agent, id))
		mu.Unlock()
	}
}

func valid(d diskEntry, modTime time.Time, size int64) bool {
	return d.Version == schemaVersion && d.Size == size && d.ModTime.Equal(modTime)
}

func readDisk(agent, sessionID string) (diskEntry, bool) {
	b, err := os.ReadFile(diskPath(agent, sessionID))
	if err != nil {
		return diskEntry{}, false
	}
	var d diskEntry
	if json.Unmarshal(b, &d) != nil {
		return diskEntry{}, false
	}
	return d, true
}

func memKey(agent, sessionID string) string { return agent + "/" + sessionID }

func agentDir(agent string) string {
	return config.GetCachePath(filepath.Join("history", sanitize(agent)))
}

func diskPath(agent, sessionID string) string {
	return filepath.Join(agentDir(agent), sanitize(sessionID)+".json")
}

// sanitize keeps a component to a single safe path segment (ids are UUID/hex, but
// guard against separators leaking into the cache path).
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == os.PathSeparator {
			return '_'
		}
		return r
	}, s)
}

// writeFileAtomic writes data to a sibling temp file then renames it into place,
// so a concurrent reader never sees a half-written cache file.
func writeFileAtomic(path string, data []byte) {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp)
		return
	}
	if os.Rename(tmp, path) != nil {
		os.Remove(tmp)
	}
}
