package push

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store persists one push target per device as a JSON file. Keyed by device (not
// endpoint) so re-registering with a new endpoint replaces the old record rather
// than leaving a stale one. Safe for concurrent use.
type Store struct {
	dir string
	now func() time.Time
	mu  sync.Mutex
}

// pauseIndefinite is the sentinel PausedUntil value for "off until the user turns
// it back on". A far-future timestamp keeps the send-time check a single
// comparison. The app mirrors this constant by value.
const pauseIndefinite = "9999-12-31T23:59:59Z"

// Record is the on-disk shape of a device's registration.
type Record struct {
	DeviceID string `json:"device_id"`
	Target
	CreatedAt string `json:"created_at"`
	LastSeen  string `json:"last_seen"`
	// PausedUntil, when set, suppresses broadcast sends to this device until the
	// given RFC3339 time. Empty means enabled; the pauseIndefinite sentinel means
	// off until the user re-enables. Old records without the field decode to "".
	PausedUntil string `json:"paused_until,omitempty"`
}

// Paused reports whether broadcasts to this device are suppressed at now. An
// unparseable PausedUntil fails open (delivers) rather than silently dropping.
func (r Record) Paused(now time.Time) bool {
	if r.PausedUntil == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339, r.PausedUntil)
	if err != nil {
		return false
	}
	return now.Before(until)
}

// NewStore returns a Store persisting under dir (created lazily on first write).
func NewStore(dir string) *Store { return &Store{dir: dir, now: time.Now} }

// storeID derives a filesystem-safe, collision-resistant id from a device id.
func storeID(deviceID string) string {
	sum := sha256.Sum256([]byte(deviceID))
	return hex.EncodeToString(sum[:])
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

// Upsert records (or refreshes) a device's target and pause state. A new record
// gets CreatedAt; an existing one keeps it and bumps LastSeen. pausedUntil is
// authoritative: the app supplies its current preference on every register, so
// a reconnect re-applies (never loses) the pause. Empty means enabled.
func (s *Store) Upsert(deviceID string, t Target, pausedUntil string) error {
	if deviceID == "" {
		return fmt.Errorf("push: empty device id")
	}
	if !t.valid() {
		return fmt.Errorf("push: invalid target")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	now := s.now().UTC().Format(time.RFC3339)
	rec := Record{DeviceID: deviceID, Target: t, CreatedAt: now, LastSeen: now, PausedUntil: pausedUntil}
	if b, err := os.ReadFile(s.path(storeID(deviceID))); err == nil {
		var prev Record
		if json.Unmarshal(b, &prev) == nil && prev.CreatedAt != "" {
			rec.CreatedAt = prev.CreatedAt
		}
	}
	return s.writeRecord(deviceID, rec)
}

func (s *Store) writeRecord(deviceID string, rec Record) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(storeID(deviceID)), b, 0o600)
}

// SetPause updates only the PausedUntil field of an existing device, preserving
// its Target and timestamps. An empty pausedUntil re-enables the device. A
// missing device is an error, so a toggle for an unregistered device is visible.
func (s *Store) SetPause(deviceID, pausedUntil string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path(storeID(deviceID)))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("push: no registration for device")
		}
		return err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return err
	}
	rec.PausedUntil = pausedUntil
	return s.writeRecord(deviceID, rec)
}

// Get returns a device's target, if registered.
func (s *Store) Get(deviceID string) (Target, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path(storeID(deviceID)))
	if err != nil {
		if os.IsNotExist(err) {
			return Target{}, false, nil
		}
		return Target{}, false, err
	}
	var rec Record
	if json.Unmarshal(b, &rec) != nil || !rec.Target.valid() {
		return Target{}, false, nil
	}
	return rec.Target, true, nil
}

// Remove deletes a device's record. A missing record is not an error.
func (s *Store) Remove(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(storeID(deviceID))); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// List returns all persisted device registrations.
func (s *Store) List() ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if rerr != nil {
			continue
		}
		var rec Record
		// Skip malformed records and legacy endpoint-keyed files (no device_id):
		// the latter would double-send alongside the device's current record.
		if json.Unmarshal(b, &rec) != nil || rec.DeviceID == "" || !rec.Target.valid() {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}
