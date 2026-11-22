package invite

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestStore(t *testing.T) (*Store, *fakeClock, string) {
	t.Helper()
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "sub", "invites.json")
	s, err := OpenStore(path, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	return s, clk, path
}

func TestStoreCreateAndSecret(t *testing.T) {
	s, clk, _ := newTestStore(t)
	inv, err := s.Create(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.ID) != 16 || len(inv.Secret) != 32 {
		t.Fatalf("id %d bytes, secret %d bytes", len(inv.ID), len(inv.Secret))
	}
	if !inv.ExpiresAt.Equal(clk.t.Add(time.Hour)) || !inv.CreatedAt.Equal(clk.t) || inv.State != StatePending {
		t.Fatalf("unexpected invite %+v", inv)
	}
	got, ok := s.Secret(inv.ID)
	if !ok || !bytes.Equal(got, inv.Secret) {
		t.Fatalf("Secret = %x, %v", got, ok)
	}
	got[0] ^= 0xff
	again, _ := s.Secret(inv.ID)
	if !bytes.Equal(again, inv.Secret) {
		t.Fatal("Secret returned aliased bytes")
	}
	if _, err := s.Create(0); err == nil {
		t.Fatal("Create(0) succeeded")
	}
	if _, err := s.Create(MaxTTL + time.Second); err == nil {
		t.Fatal("Create past MaxTTL succeeded")
	}
}

func TestStoreConsumeOnce(t *testing.T) {
	s, clk, path := newTestStore(t)
	inv, _ := s.Create(time.Hour)
	if err := s.Consume(inv.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Consume(inv.ID); !errors.Is(err, ErrUsed) {
		t.Fatalf("second Consume = %v", err)
	}
	s2, err := OpenStore(path, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s2.List()
	if len(list) != 1 || list[0].State != StateUsed || !list[0].UsedAt.Equal(clk.t) {
		t.Fatalf("reopened list = %+v", list)
	}
	if err := s2.Consume(inv.ID); !errors.Is(err, ErrUsed) {
		t.Fatalf("reopened Consume = %v", err)
	}
}

func TestStoreRecordKeys(t *testing.T) {
	s, clk, path := newTestStore(t)
	inv, _ := s.Create(time.Hour)
	key := []byte{1, 2, 3}
	if err := s.RecordKeys(inv.ID, [][]byte{key}); err != nil {
		t.Fatal(err)
	}
	key[0] = 9
	s2, _ := OpenStore(path, clk.now)
	list, _ := s2.List()
	if len(list) != 1 || len(list[0].Keys) != 1 || !bytes.Equal(list[0].Keys[0], []byte{1, 2, 3}) {
		t.Fatalf("list = %+v", list)
	}
	if err := s.RecordKeys(make([]byte, 16), nil); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown RecordKeys = %v", err)
	}
}

func TestStoreConsumeExpired(t *testing.T) {
	s, clk, _ := newTestStore(t)
	inv, _ := s.Create(time.Hour)
	clk.advance(time.Hour)
	if err := s.Consume(inv.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("Consume = %v", err)
	}
}

func TestStoreConsumeRevoked(t *testing.T) {
	s, _, _ := newTestStore(t)
	inv, _ := s.Create(time.Hour)
	if err := s.Revoke(inv.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(inv.ID); err != nil {
		t.Fatalf("second Revoke = %v", err)
	}
	if err := s.Consume(inv.ID); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Consume = %v", err)
	}
	if _, ok := s.Secret(inv.ID); !ok {
		t.Fatal("Secret not found for revoked invite")
	}
}

func TestStoreRevokeUsed(t *testing.T) {
	s, _, _ := newTestStore(t)
	inv, _ := s.Create(time.Hour)
	if err := s.Consume(inv.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(inv.ID); !errors.Is(err, ErrUsed) {
		t.Fatalf("Revoke = %v", err)
	}
}

func TestStoreUnknown(t *testing.T) {
	s, _, _ := newTestStore(t)
	id := make([]byte, 16)
	rand.Read(id)
	if err := s.Consume(id); !errors.Is(err, ErrUnknown) {
		t.Fatalf("Consume = %v", err)
	}
	if err := s.Revoke(id); !errors.Is(err, ErrUnknown) {
		t.Fatalf("Revoke = %v", err)
	}
	if _, ok := s.Secret(id); ok {
		t.Fatal("Secret ok for unknown id")
	}
}

func TestStoreListPrunes(t *testing.T) {
	s, clk, path := newTestStore(t)
	used, _ := s.Create(time.Hour)
	if err := s.Consume(used.ID); err != nil {
		t.Fatal(err)
	}
	clk.advance(7*24*time.Hour + time.Second)
	pending, _ := s.Create(time.Hour)
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !bytes.Equal(list[0].ID, pending.ID) {
		t.Fatalf("list = %+v", list)
	}
	s2, _ := OpenStore(path, clk.now)
	list, _ = s2.List()
	if len(list) != 1 || !bytes.Equal(list[0].ID, pending.ID) {
		t.Fatalf("reopened list = %+v", list)
	}
	if _, ok := s2.Secret(used.ID); ok {
		t.Fatal("pruned invite still known")
	}
}

func TestStoreCreatePrunes(t *testing.T) {
	s, clk, path := newTestStore(t)
	used, _ := s.Create(time.Hour)
	if err := s.Consume(used.ID); err != nil {
		t.Fatal(err)
	}
	clk.advance(7*24*time.Hour + time.Second)
	if _, err := s.Create(time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Secret(used.ID); ok {
		t.Fatal("Create kept an invite past retention")
	}
	s2, _ := OpenStore(path, clk.now)
	if _, ok := s2.Secret(used.ID); ok {
		t.Fatal("Create did not persist the pruning")
	}
}

func TestStoreListNewestFirst(t *testing.T) {
	s, clk, _ := newTestStore(t)
	a, _ := s.Create(time.Hour)
	clk.advance(time.Minute)
	b, _ := s.Create(time.Hour)
	list, _ := s.List()
	if len(list) != 2 || !bytes.Equal(list[0].ID, b.ID) || !bytes.Equal(list[1].ID, a.ID) {
		t.Fatalf("list = %+v", list)
	}
}

func TestStoreCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invites.json")
	garbage := []byte("not json{")
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path, time.Now); err == nil {
		t.Fatal("OpenStore succeeded on corrupt file")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, garbage) {
		t.Fatal("corrupt file was modified")
	}
}

func TestStoreFailedSaveKeepsState(t *testing.T) {
	s, _, path := newTestStore(t)
	pending, _ := s.Create(time.Hour)
	revoked, _ := s.Create(time.Hour)
	if err := s.Revoke(revoked.ID); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if _, err := s.Create(time.Hour); err == nil {
		t.Fatal("Create succeeded in a read-only dir")
	}
	if err := s.Consume(pending.ID); err == nil {
		t.Fatal("Consume succeeded in a read-only dir")
	}
	if err := s.Revoke(pending.ID); err == nil {
		t.Fatal("Revoke succeeded in a read-only dir")
	}
	if err := s.RecordKeys(revoked.ID, [][]byte{{1}}); err == nil {
		t.Fatal("RecordKeys succeeded in a read-only dir")
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]State{string(pending.ID): StatePending, string(revoked.ID): StateRevoked}
	if len(list) != len(want) {
		t.Fatalf("list = %+v, want 2 invites", list)
	}
	for _, inv := range list {
		if inv.State != want[string(inv.ID)] || !inv.UsedAt.IsZero() || inv.Keys != nil {
			t.Errorf("invite %x = %+v after failed saves", inv.ID, inv)
		}
	}
}
