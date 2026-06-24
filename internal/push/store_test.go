package push

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreUpsertGetRemove(t *testing.T) {
	s := NewStore(t.TempDir())
	tg := Target{Endpoint: "https://up.example/x", P256dh: "pk", Auth: "au"}

	if err := s.Upsert("dev-1", tg); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, ok, err := s.Get("dev-1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got != tg {
		t.Errorf("Get = %+v, want %+v", got, tg)
	}

	if err := s.Remove("dev-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok, _ := s.Get("dev-1"); ok {
		t.Error("Get after Remove: still present")
	}
	// Removing a missing device is not an error.
	if err := s.Remove("dev-1"); err != nil {
		t.Fatalf("Remove(missing): %v", err)
	}
}

func TestStoreReregisterReplacesEndpoint(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Upsert("dev-1", Target{Endpoint: "https://old.example/a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("dev-1", Target{Endpoint: "https://new.example/b"}); err != nil {
		t.Fatal(err)
	}
	recs, _ := s.List()
	if len(recs) != 1 {
		t.Fatalf("re-register left %d records, want 1 (replace)", len(recs))
	}
	if recs[0].Endpoint != "https://new.example/b" {
		t.Errorf("endpoint = %q, want the new one", recs[0].Endpoint)
	}
}

func TestStoreListMultipleDevices(t *testing.T) {
	s := NewStore(t.TempDir())
	mustUpsert(t, s, "dev-1", Target{Endpoint: "https://a.example/1"})
	mustUpsert(t, s, "dev-2", Target{Endpoint: "https://b.example/2"})
	recs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("List len = %d, want 2", len(recs))
	}
}

func TestStoreUpsertPreservesCreatedAt(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	clock := []string{"2026-01-01T00:00:00Z", "2026-06-22T00:00:00Z"}
	i := 0
	s.now = func() time.Time { ts, _ := time.Parse(time.RFC3339, clock[i]); return ts }

	mustUpsert(t, s, "dev-1", Target{Endpoint: "https://a.example/1"})
	i = 1
	mustUpsert(t, s, "dev-1", Target{Endpoint: "https://a.example/2"})

	rec := readRecord(t, dir, storeID("dev-1"))
	if rec.CreatedAt != clock[0] {
		t.Errorf("CreatedAt = %q, want %q (preserved)", rec.CreatedAt, clock[0])
	}
	if rec.LastSeen != clock[1] {
		t.Errorf("LastSeen = %q, want %q (advanced)", rec.LastSeen, clock[1])
	}
}

func TestStoreRejectsInvalid(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Upsert("", Target{Endpoint: "https://x"}); err == nil {
		t.Error("Upsert with empty device id = nil, want error")
	}
	if err := s.Upsert("dev-1", Target{}); err == nil {
		t.Error("Upsert with empty endpoint = nil, want error")
	}
}

func mustUpsert(t *testing.T, s *Store, deviceID string, tg Target) {
	t.Helper()
	if err := s.Upsert(deviceID, tg); err != nil {
		t.Fatalf("Upsert(%s): %v", deviceID, err)
	}
}

func readRecord(t *testing.T, dir, id string) Record {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	return rec
}
