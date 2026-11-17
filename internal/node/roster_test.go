package node

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

func rosterEv(typ, id string, online bool) api.NodeEvent {
	return api.NodeEvent{Type: typ, Node: api.NodeDescriptor{ID: id, Online: online}}
}

func newTestRoster() *peerRoster { return newPeerRoster(func() string { return "home" }) }

func TestRosterAppliesEvents(t *testing.T) {
	r := newTestRoster()
	r.apply(rosterEv(api.NodeEventAdded, "a", true))
	if n, ok := r.get("a"); !ok || !n.Online {
		t.Fatalf("get a = %+v, %v", n, ok)
	}
	r.apply(rosterEv(api.NodeEventOffline, "a", false))
	if n, _ := r.get("a"); n.Online {
		t.Fatal("a still online")
	}
	r.apply(rosterEv(api.NodeEventOnline, "a", true))
	if n, _ := r.get("a"); !n.Online {
		t.Fatal("a not online")
	}
	r.apply(rosterEv(api.NodeEventTrustChanged, "a", false))
	if n, _ := r.get("a"); !n.Online {
		t.Fatal("trust-changed altered state")
	}
	r.apply(rosterEv(api.NodeEventRemoved, "a", false))
	if _, ok := r.get("a"); ok {
		t.Fatal("a present after removed")
	}
}

func TestRosterOnlineTakesFreshDescriptor(t *testing.T) {
	r := newTestRoster()
	r.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{ID: "a", Online: true, Label: "old", IdentityPubKey: "k1"}})
	r.apply(rosterEv(api.NodeEventOffline, "a", false))
	// The node restarts within the grace period with a new version.
	r.apply(api.NodeEvent{Type: api.NodeEventOnline, Node: api.NodeDescriptor{
		ID: "a", Label: "new", IdentityPubKey: "k2", Version: "v2",
	}})
	n, _ := r.get("a")
	if !n.Online || n.Label != "new" || n.IdentityPubKey != "k2" || n.Version != "v2" {
		t.Fatalf("a = %+v, want the fresh descriptor, online", n)
	}
}

func TestRosterTrustChangedNotPublished(t *testing.T) {
	r := newTestRoster()
	ch, cancel := r.subscribe()
	defer cancel()
	r.apply(rosterEv(api.NodeEventTrustChanged, "a", false))
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event %+v", ev)
	default:
	}
}

func TestRosterOthersExcludesSelf(t *testing.T) {
	r := newTestRoster()
	r.apply(rosterEv(api.NodeEventAdded, "home", true))
	r.apply(rosterEv(api.NodeEventAdded, "b", true))
	r.apply(rosterEv(api.NodeEventAdded, "c", false))
	got := r.others()
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" || got[1].Online {
		t.Fatalf("others = %+v", got)
	}
}

func TestRosterResetOnUplinkClose(t *testing.T) {
	r := newTestRoster()
	r.apply(rosterEv(api.NodeEventAdded, "a", true))
	r.apply(rosterEv(api.NodeEventAdded, "b", true))
	ch, cancel := r.subscribe()
	defer cancel()
	r.reset()
	if _, ok := r.get("a"); ok {
		t.Fatal("a present after reset")
	}
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		ev := <-ch
		if ev.Type != api.NodeEventRemoved {
			t.Fatalf("type = %s", ev.Type)
		}
		seen[ev.Node.ID] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("seen = %v", seen)
	}
}

func TestRosterSlowSubscriberDoesNotBlock(t *testing.T) {
	r := newTestRoster()
	_, cancel := r.subscribe()
	defer cancel()
	for i := 0; i < rosterSubBuffer*3; i++ {
		r.apply(rosterEv(api.NodeEventAdded, "a", true))
	}
}

func TestNodeGatewayNotifyAppliesNodeEvent(t *testing.T) {
	d := newNode(nil)
	if d.roster == nil {
		t.Fatal("roster nil")
	}
	params, _ := json.Marshal(rosterEv(api.NodeEventAdded, "x", true))
	d.onGatewayNotify(api.Notification{Method: api.MethodNodeEvent, Params: params})
	if _, ok := d.roster.get("x"); !ok {
		t.Fatal("x not in roster")
	}
}

func TestRosterSeenWithin(t *testing.T) {
	r := newTestRoster()
	start := r.started
	if !r.seenWithin("x", time.Hour, start.Add(30*time.Minute)) {
		t.Fatal("a node is seen within the window since the roster started")
	}
	if r.seenWithin("x", time.Hour, start.Add(2*time.Hour)) {
		t.Fatal("a node never seen since start is not seen after the window")
	}
	r.apply(rosterEv(api.NodeEventAdded, "a", true))
	if !r.seenWithin("a", time.Hour, start.Add(2*time.Hour)) {
		t.Fatal("an online node is always seen")
	}
	r.reset()
	if !r.seenWithin("a", time.Hour, time.Now().Add(30*time.Minute)) {
		t.Fatal("a node online until the reset is seen within the window after it")
	}
	if r.seenWithin("a", time.Hour, time.Now().Add(2*time.Hour)) {
		t.Fatal("a node not seen for the window is not seen")
	}
}
