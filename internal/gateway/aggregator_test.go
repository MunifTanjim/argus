package gateway

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

type fakeSource struct {
	id, label     string
	idPubKey      string
	signerPubKey  string
	beaconPubKey  string
	initialBeacon *api.Beacon
	done          chan struct{}
}

func newFakeSource(id, label string) *fakeSource {
	return &fakeSource{id: id, label: label, done: make(chan struct{})}
}

func (f *fakeSource) ID() string                { return f.id }
func (f *fakeSource) Label() string             { return f.label }
func (f *fakeSource) Version() string           { return "" }
func (f *fakeSource) IdentityPubKey() string    { return f.idPubKey }
func (f *fakeSource) SignerPubKey() string      { return f.signerPubKey }
func (f *fakeSource) BeaconPubKey() string      { return f.beaconPubKey }
func (f *fakeSource) LatestBeacon() *api.Beacon { return f.initialBeacon }
func (f *fakeSource) Capabilities() api.NodeCapabilities {
	return api.NodeCapabilities{SpawnSession: true}
}
func (f *fakeSource) Done() <-chan struct{} { return f.done }
func (f *fakeSource) close()                { close(f.done) }

func eventually(t *testing.T, want func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if want() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func recvRoster(t *testing.T, ch <-chan api.NodeEvent) api.NodeEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no roster event received")
		return api.NodeEvent{}
	}
}

func TestAggregatorRosterAndEvents(t *testing.T) {
	a := New(50 * time.Millisecond)
	sub, cancel := a.SubscribeRoster()
	defer cancel()

	src := newFakeSource("n1", "n1-box")
	src.idPubKey = "PUB1"
	a.AddSource(src)

	// added
	select {
	case ev := <-sub:
		if ev.Type != api.NodeEventAdded || ev.Node.ID != "n1" ||
			ev.Node.IdentityPubKey != "PUB1" || !ev.Node.Online {
			t.Fatalf("added event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no added roster event")
	}

	// snapshot
	if r := a.Roster(); len(r) != 1 || r[0].ID != "n1" || r[0].IdentityPubKey != "PUB1" || !r[0].Online {
		t.Fatalf("roster = %+v", r)
	}

	// offline on disconnect
	close(src.done)
	select {
	case ev := <-sub:
		if ev.Type != api.NodeEventOffline || ev.Node.Online {
			t.Fatalf("offline event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no offline roster event")
	}

	// removed after grace
	select {
	case ev := <-sub:
		if ev.Type != api.NodeEventRemoved {
			t.Fatalf("removed event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no removed roster event")
	}
	if r := a.Roster(); len(r) != 0 {
		t.Errorf("roster not empty after removal: %+v", r)
	}
}

func TestSourceLivenessOnlineOfflineRemoved(t *testing.T) {
	a := New(20 * time.Millisecond) // short grace
	sub, cancel := a.SubscribeRoster()
	defer cancel()

	src := newFakeSource("n1", "node-1")
	a.AddSource(src)
	if ev := recvRoster(t, sub); ev.Type != api.NodeEventAdded || ev.Node.ID != "n1" {
		t.Fatalf("want Added n1, got %+v", ev)
	}

	src.close() // simulate disconnect: closes Done()
	if ev := recvRoster(t, sub); ev.Type != api.NodeEventOffline {
		t.Fatalf("want Offline, got %+v", ev)
	}
	if ev := recvRoster(t, sub); ev.Type != api.NodeEventRemoved {
		t.Fatalf("want Removed after grace, got %+v", ev)
	}
}

func TestReconnectBeforeGraceNoRemoved(t *testing.T) {
	a := New(200 * time.Millisecond)
	sub, cancel := a.SubscribeRoster()
	defer cancel()

	src := newFakeSource("home", "home-box")
	a.AddSource(src)
	if ev := recvRoster(t, sub); ev.Type != api.NodeEventAdded {
		t.Fatalf("want Added, got %+v", ev)
	}

	src.close() // disconnect; removal scheduled in 200ms
	if ev := recvRoster(t, sub); ev.Type != api.NodeEventOffline {
		t.Fatalf("want Offline, got %+v", ev)
	}

	// Reconnect well before grace elapses.
	time.Sleep(50 * time.Millisecond)
	a.AddSource(newFakeSource("home", "home-box"))
	if ev := recvRoster(t, sub); ev.Type != api.NodeEventOnline {
		t.Fatalf("want Online on reconnect, got %+v", ev)
	}

	// Past the original grace window, no Removed should arrive.
	select {
	case ev := <-sub:
		t.Fatalf("unexpected roster event after reconnect: %+v", ev)
	case <-time.After(300 * time.Millisecond):
		// good: no Removed
	}
}
