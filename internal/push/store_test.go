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

	if err := s.Upsert("dev-1", tg, ""); err != nil {
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
	if err := s.Upsert("dev-1", Target{Endpoint: "https://old.example/a"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("dev-1", Target{Endpoint: "https://new.example/b"}, ""); err != nil {
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
	if err := s.Upsert("", Target{Endpoint: "https://x"}, ""); err == nil {
		t.Error("Upsert with empty device id = nil, want error")
	}
	if err := s.Upsert("dev-1", Target{}, ""); err == nil {
		t.Error("Upsert with empty endpoint = nil, want error")
	}
}

func TestStoreUpsertCarriesPause(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	tg := Target{Endpoint: "https://a.example/1"}

	// Register-authoritative: a pause supplied at register time is persisted.
	if err := s.Upsert("dev-1", tg, pauseIndefinite); err != nil {
		t.Fatal(err)
	}
	if rec := readRecord(t, dir, storeID("dev-1")); rec.PausedUntil != pauseIndefinite {
		t.Errorf("PausedUntil = %q, want %q", rec.PausedUntil, pauseIndefinite)
	}

	// A later register with an empty pause clears it.
	created := readRecord(t, dir, storeID("dev-1")).CreatedAt
	if err := s.Upsert("dev-1", tg, ""); err != nil {
		t.Fatal(err)
	}
	rec := readRecord(t, dir, storeID("dev-1"))
	if rec.PausedUntil != "" {
		t.Errorf("PausedUntil after re-register = %q, want empty", rec.PausedUntil)
	}
	if rec.CreatedAt != created {
		t.Errorf("CreatedAt = %q, want %q (preserved)", rec.CreatedAt, created)
	}
}

func TestStoreSetPause(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	tg := Target{Endpoint: "https://a.example/1", P256dh: "pk", Auth: "au"}
	mustUpsert(t, s, "dev-1", tg)
	before := readRecord(t, dir, storeID("dev-1"))

	if err := s.SetPause("dev-1", pauseIndefinite); err != nil {
		t.Fatalf("SetPause: %v", err)
	}
	rec := readRecord(t, dir, storeID("dev-1"))
	if rec.PausedUntil != pauseIndefinite {
		t.Errorf("PausedUntil = %q, want %q", rec.PausedUntil, pauseIndefinite)
	}
	if rec.Target != tg {
		t.Errorf("Target = %+v, want %+v (preserved)", rec.Target, tg)
	}
	if rec.CreatedAt != before.CreatedAt || rec.LastSeen != before.LastSeen {
		t.Errorf("timestamps changed: got created=%q seen=%q", rec.CreatedAt, rec.LastSeen)
	}

	// Clearing with an empty string re-enables.
	if err := s.SetPause("dev-1", ""); err != nil {
		t.Fatalf("SetPause clear: %v", err)
	}
	if rec := readRecord(t, dir, storeID("dev-1")); rec.PausedUntil != "" {
		t.Errorf("PausedUntil after clear = %q, want empty", rec.PausedUntil)
	}
}

func TestStoreSetPauseUnknownDevice(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.SetPause("nope", pauseIndefinite); err == nil {
		t.Error("SetPause on unknown device = nil, want error")
	}
}

func TestRecordPaused(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-01T12:00:00Z")
	cases := []struct {
		name        string
		pausedUntil string
		want        bool
	}{
		{"empty is enabled", "", false},
		{"future is paused", "2026-06-01T13:00:00Z", true},
		{"sentinel is paused", pauseIndefinite, true},
		{"past is enabled", "2026-06-01T11:00:00Z", false},
		{"exact boundary resumes", "2026-06-01T12:00:00Z", false},
		{"malformed fails open", "not-a-time", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := Record{PausedUntil: c.pausedUntil}
			if got := rec.Paused(now); got != c.want {
				t.Errorf("Paused(%q) = %v, want %v", c.pausedUntil, got, c.want)
			}
		})
	}
}

func mustUpsert(t *testing.T, s *Store, deviceID string, tg Target) {
	t.Helper()
	if err := s.Upsert(deviceID, tg, ""); err != nil {
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
