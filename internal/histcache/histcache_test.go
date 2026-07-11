package histcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/session"
)

// setup points the cache at a temp dir and clears the in-memory layer.
func setup(t *testing.T) {
	t.Helper()
	config.CacheDir = t.TempDir()
	mu.Lock()
	mem = map[string]diskEntry{}
	mu.Unlock()
}

func sampleEntry(id string) Entry {
	return Entry{
		Session: session.HistorySession{SessionID: id, Title: "hello", TurnCount: 3},
		Cwd:     "/home/x/proj",
	}
}

func TestGetMissThenRoundtrip(t *testing.T) {
	setup(t)
	mt := time.Unix(1000, 0)

	if _, ok := Get("claude", "s1", mt, 42); ok {
		t.Fatal("expected miss on empty cache")
	}

	e := sampleEntry("s1")
	Put("claude", "s1", mt, 42, e)

	got, ok := Get("claude", "s1", mt, 42)
	if !ok {
		t.Fatal("expected hit after Put")
	}
	if got.Session.Title != "hello" || got.Cwd != "/home/x/proj" || got.Session.TurnCount != 3 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestModTimeInvalidation(t *testing.T) {
	setup(t)
	Put("claude", "s1", time.Unix(1000, 0), 42, sampleEntry("s1"))

	if _, ok := Get("claude", "s1", time.Unix(2000, 0), 42); ok {
		t.Fatal("changed modTime must miss")
	}
}

func TestSizeInvalidation(t *testing.T) {
	setup(t)
	Put("claude", "s1", time.Unix(1000, 0), 42, sampleEntry("s1"))

	if _, ok := Get("claude", "s1", time.Unix(1000, 0), 99); ok {
		t.Fatal("changed size must miss")
	}
}

func TestSchemaVersionInvalidation(t *testing.T) {
	setup(t)
	mt := time.Unix(1000, 0)
	Put("claude", "s1", mt, 42, sampleEntry("s1"))

	// Rewrite the disk record with a stale version and clear L1.
	d, ok := readDisk("claude", "s1")
	if !ok {
		t.Fatal("expected disk record")
	}
	d.Version = schemaVersion + 1
	writeJSON(t, diskPath("claude", "s1"), d)
	mu.Lock()
	mem = map[string]diskEntry{}
	mu.Unlock()

	if _, ok := Get("claude", "s1", mt, 42); ok {
		t.Fatal("stale schemaVersion must miss")
	}
}

func TestDiskPersistenceAcrossMemoryClear(t *testing.T) {
	setup(t)
	mt := time.Unix(1000, 0)
	Put("codex", "s1", mt, 42, sampleEntry("s1"))

	// Simulate a fresh process: drop L1, keep disk.
	mu.Lock()
	mem = map[string]diskEntry{}
	mu.Unlock()

	got, ok := Get("codex", "s1", mt, 42)
	if !ok {
		t.Fatal("expected hit from disk after L1 clear")
	}
	if got.Session.SessionID != "s1" {
		t.Fatalf("disk roundtrip mismatch: %+v", got)
	}
}

func TestCorruptDiskFileMisses(t *testing.T) {
	setup(t)
	path := diskPath("claude", "s1")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := Get("claude", "s1", time.Unix(1000, 0), 42); ok {
		t.Fatal("corrupt disk file must miss, not error")
	}
}

func TestPruneRemovesOrphans(t *testing.T) {
	setup(t)
	mt := time.Unix(1000, 0)
	Put("codex", "keep", mt, 42, sampleEntry("keep"))
	Put("codex", "drop", mt, 42, sampleEntry("drop"))

	Prune("codex", map[string]struct{}{"keep": {}})

	if _, ok := Get("codex", "keep", mt, 42); !ok {
		t.Fatal("live entry must survive prune")
	}
	if _, ok := Get("codex", "drop", mt, 42); ok {
		t.Fatal("orphan entry must be pruned")
	}
	if _, err := os.Stat(diskPath("codex", "drop")); !os.IsNotExist(err) {
		t.Fatal("orphan disk file must be removed")
	}
}

func writeJSON(t *testing.T, path string, d diskEntry) {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
