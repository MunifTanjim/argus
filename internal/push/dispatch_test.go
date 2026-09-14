package push

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// fakeSender records the targets it was asked to send to and can simulate a gone
// target by endpoint.
type fakeSender struct {
	mu   sync.Mutex
	sent []Target
	gone map[string]bool // endpoint -> return ErrGone
}

func (f *fakeSender) Send(_ context.Context, t Target, _ Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, t)
	if f.gone[t.Endpoint] {
		return fmt.Errorf("%w", ErrGone)
	}
	return nil
}

func TestDispatcherSendAll(t *testing.T) {
	store := NewStore(t.TempDir())
	mustUpsert(t, store, "dev-1", Target{Endpoint: "https://a.example/1"})
	mustUpsert(t, store, "dev-2", Target{Endpoint: "https://b.example/2"})

	sender := &fakeSender{}
	d := NewDispatcher(store, sender, nil)
	d.Send(context.Background(), Notification{Title: "t"})

	if len(sender.sent) != 2 {
		t.Fatalf("sent to %d targets, want 2", len(sender.sent))
	}
}

func TestDispatcherSendSkipsPaused(t *testing.T) {
	store := NewStore(t.TempDir())
	mustUpsert(t, store, "live", Target{Endpoint: "https://live.example/1"})
	mustUpsert(t, store, "paused", Target{Endpoint: "https://paused.example/2"})
	if err := store.SetPause("paused", pauseIndefinite); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{}
	d := NewDispatcher(store, sender, nil)
	d.Send(context.Background(), Notification{})

	if len(sender.sent) != 1 || sender.sent[0].Endpoint != "https://live.example/1" {
		t.Fatalf("sent = %v, want only the live device", sender.sent)
	}
}

func TestDispatcherSendDeliversExpiredPause(t *testing.T) {
	store := NewStore(t.TempDir())
	mustUpsert(t, store, "dev-1", Target{Endpoint: "https://a.example/1"})
	if err := store.SetPause("dev-1", "2000-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{}
	d := NewDispatcher(store, sender, nil)
	d.Send(context.Background(), Notification{})

	if len(sender.sent) != 1 {
		t.Fatalf("expired pause: sent %d, want 1 (delivered)", len(sender.sent))
	}
}

func TestDispatcherSendToBypassesPause(t *testing.T) {
	store := NewStore(t.TempDir())
	mustUpsert(t, store, "paused", Target{Endpoint: "https://paused.example/2"})
	if err := store.SetPause("paused", pauseIndefinite); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{}
	d := NewDispatcher(store, sender, nil)
	if err := d.SendTo(context.Background(), "paused", Notification{}); err != nil {
		t.Fatalf("SendTo: %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("test send to paused device: sent %d, want 1 (bypass)", len(sender.sent))
	}
}

func TestDispatcherSendPrunesGone(t *testing.T) {
	store := NewStore(t.TempDir())
	mustUpsert(t, store, "live", Target{Endpoint: "https://live.example/1"})
	mustUpsert(t, store, "dead", Target{Endpoint: "https://dead.example/2"})

	sender := &fakeSender{gone: map[string]bool{"https://dead.example/2": true}}
	d := NewDispatcher(store, sender, nil)
	d.Send(context.Background(), Notification{})

	recs, _ := store.List()
	if len(recs) != 1 || recs[0].DeviceID != "live" {
		t.Fatalf("after prune, records = %v, want only the live device", recs)
	}
}

func TestDispatcherSendToDevice(t *testing.T) {
	store := NewStore(t.TempDir())
	mustUpsert(t, store, "dev-1", Target{Endpoint: "https://a.example/1"})
	sender := &fakeSender{}
	d := NewDispatcher(store, sender, nil)

	if err := d.SendTo(context.Background(), "dev-1", Notification{}); err != nil {
		t.Fatalf("SendTo: %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d, want 1", len(sender.sent))
	}
}

func TestDispatcherSendToUnknownDevice(t *testing.T) {
	store := NewStore(t.TempDir())
	d := NewDispatcher(store, &fakeSender{}, nil)
	if err := d.SendTo(context.Background(), "nope", Notification{}); err == nil {
		t.Error("SendTo unknown device = nil, want error")
	}
}

func TestDispatcherSendToPrunesGone(t *testing.T) {
	store := NewStore(t.TempDir())
	mustUpsert(t, store, "dead", Target{Endpoint: "https://dead.example/2"})
	sender := &fakeSender{gone: map[string]bool{"https://dead.example/2": true}}
	d := NewDispatcher(store, sender, nil)

	if err := d.SendTo(context.Background(), "dead", Notification{}); err == nil {
		t.Error("SendTo gone target = nil, want error")
	}
	if _, ok, _ := store.Get("dead"); ok {
		t.Error("gone device not pruned")
	}
}
