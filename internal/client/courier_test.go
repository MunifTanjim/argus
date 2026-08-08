package client

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// TestClientBeaconCourierDeliversToOtherNodes verifies that the client couriers
// a node's signed beacon to every OTHER connected node via beacon.deliver, and
// does not deliver a node's own beacon back to itself.
func TestClientBeaconCourierDeliversToOtherNodes(t *testing.T) {
	var mu sync.Mutex
	// delivered records which beacons each node received (by target nodeID →
	// list of BeaconPub hex strings received).
	delivered := map[string][]string{}

	makeHandler := func(id string) func(string, json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		return func(method string, params json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
			if method != api.MethodBeaconDeliver {
				return nil, nil, nil
			}
			var b api.Beacon
			if err := json.Unmarshal(params, &b); err != nil {
				return nil, nil, nil
			}
			mu.Lock()
			delivered[id] = append(delivered[id], base64.StdEncoding.EncodeToString(b.BeaconPub))
			mu.Unlock()
			return nil, nil, nil
		}
	}

	n1 := &fakeNode{id: "n1", key: mustKP(t), handle: makeHandler("n1")}
	n2 := &fakeNode{id: "n2", key: mustKP(t), handle: makeHandler("n2")}
	gw, clientConn := newFakeMultiGateway(t, n1, n2)
	defer gw.peer.Close()

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Populate beacons for both nodes by simulating node.event notifications.
	bPub1, bPriv1 := genBeaconKey(t)
	bPub2, bPriv2 := genBeaconKey(t)
	tip := bytes.Repeat([]byte{0xab}, 32)

	sendBeaconEvent := func(n *fakeNode, bPub ed25519.PublicKey, bPriv ed25519.PrivateKey, counter uint64) {
		b := api.SignBeacon(bPriv, bPub, tip, 1, counter)
		_ = gw.peer.Notify(api.MethodNodeEvent, api.NodeEvent{
			Type: api.NodeEventBeacon,
			Node: api.NodeDescriptor{
				ID:             n.id,
				IdentityPubKey: base64.StdEncoding.EncodeToString(n.key.Public),
				BeaconPubKey:   base64.StdEncoding.EncodeToString(bPub),
				Beacon:         &b,
			},
		})
	}

	sendBeaconEvent(n1, bPub1, bPriv1, 1)
	sendBeaconEvent(n2, bPub2, bPriv2, 1)

	// Wait for both beacons to be ingested.
	waitClient(t, "both beacons ingested", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.beacons) == 2
	})

	// Trigger delivery (deliverBeacons is called inside syncTrustLog, but we
	// can invoke it directly since we're in the same package).
	c.deliverBeacons()

	// Allow time for the async E2E calls to complete.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		d1 := len(delivered["n1"])
		d2 := len(delivered["n2"])
		mu.Unlock()
		if d1 >= 1 && d2 >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	got1 := delivered["n1"]
	got2 := delivered["n2"]
	mu.Unlock()

	// n1 should receive n2's beacon (and not its own).
	n1PubB64 := base64.StdEncoding.EncodeToString(bPub1)
	n2PubB64 := base64.StdEncoding.EncodeToString(bPub2)

	if len(got1) == 0 {
		t.Fatal("n1 must receive at least one beacon.deliver call (n2's beacon)")
	}
	for _, recv := range got1 {
		if recv == n1PubB64 {
			t.Fatal("n1 must NOT receive its own beacon via beacon.deliver")
		}
	}
	if len(got2) == 0 {
		t.Fatal("n2 must receive at least one beacon.deliver call (n1's beacon)")
	}
	for _, recv := range got2 {
		if recv == n2PubB64 {
			t.Fatal("n2 must NOT receive its own beacon via beacon.deliver")
		}
	}

	// n1 should receive n2's beacon pub, and vice versa.
	found12 := false
	for _, recv := range got1 {
		if recv == n2PubB64 {
			found12 = true
		}
	}
	if !found12 {
		t.Errorf("n1 must receive n2's beacon; got %v", got1)
	}
	found21 := false
	for _, recv := range got2 {
		if recv == n1PubB64 {
			found21 = true
		}
	}
	if !found21 {
		t.Errorf("n2 must receive n1's beacon; got %v", got2)
	}
}

// TestClientBeaconCourierDoesNotDeliverSelfBeacon verifies that with only one
// node connected, deliverBeacons makes no beacon.deliver calls (nothing to
// courier — only one node and its beacon should not be sent to itself).
func TestClientBeaconCourierDoesNotDeliverSelfBeacon(t *testing.T) {
	var mu sync.Mutex
	var calls int

	n1 := &fakeNode{id: "n1", key: mustKP(t), handle: func(method string, _ json.RawMessage) (json.RawMessage, *api.RPCError, *fakeNote) {
		if method == api.MethodBeaconDeliver {
			mu.Lock()
			calls++
			mu.Unlock()
		}
		return nil, nil, nil
	}}
	gw, clientConn := newFakeMultiGateway(t, n1)
	defer gw.peer.Close()

	c, err := NewE2EClient(clientConn)
	if err != nil {
		t.Fatalf("NewE2EClient: %v", err)
	}
	defer c.Close()
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Populate n1's beacon.
	bPub, bPriv := genBeaconKey(t)
	tip := bytes.Repeat([]byte{0x11}, 32)
	b := api.SignBeacon(bPriv, bPub, tip, 1, 1)
	_ = gw.peer.Notify(api.MethodNodeEvent, api.NodeEvent{
		Type: api.NodeEventBeacon,
		Node: api.NodeDescriptor{
			ID:             n1.id,
			IdentityPubKey: base64.StdEncoding.EncodeToString(n1.key.Public),
			BeaconPubKey:   base64.StdEncoding.EncodeToString(bPub),
			Beacon:         &b,
		},
	})

	waitClient(t, "n1 beacon ingested", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.beacons) == 1
	})

	c.deliverBeacons()
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 0 {
		t.Fatalf("single-node setup must not call beacon.deliver (no other nodes); got %d calls", got)
	}
}

// TestBeaconArrivalCouriersWithoutWaitingForTheTick guards the one node↔node
// beacon path there is: the courier must run when a beacon arrives. Left to the
// trust tick, a peer's tip reaches the other nodes a whole 5-minute backstop late
// and their equivocation cross-check stalls behind it.
func TestBeaconArrivalCouriersWithoutWaitingForTheTick(t *testing.T) {
	clientTrustSyncInterval.Store(int64(10 * time.Minute)) // prove the tick is not what couriered
	t.Cleanup(func() { clientTrustSyncInterval.Store(int64(5 * time.Minute)) })

	m, gw := newCourierTestClient(t, 2)
	defer m.Close()
	before := gw.beaconDeliveries()

	gw.bumpBeaconCounter(t)
	gw.emitBeaconEvent(0)

	waitClient(t, "beacon couriered on arrival", func() bool {
		return gw.beaconDeliveries() > before
	})
}

// TestSuppressedBeaconArrivalIsDeferredNotDropped is the client half of the
// coalescing fix: a second beacon landing inside the rate-limit window must still
// be couriered when the window closes, not discarded until the backstop.
func TestSuppressedBeaconArrivalIsDeferredNotDropped(t *testing.T) {
	clientTrustSyncInterval.Store(int64(10 * time.Minute))
	setTriggerIntervalForTest(200 * time.Millisecond)
	t.Cleanup(func() {
		clientTrustSyncInterval.Store(int64(5 * time.Minute))
		setTriggerIntervalForTest(5 * time.Second)
	})

	m, gw := newCourierTestClient(t, 2)
	defer m.Close()

	gw.bumpBeaconCounter(t)
	gw.emitBeaconEvent(0)
	waitClient(t, "first arrival couriered", func() bool { return gw.beaconDeliveries() > 0 })
	first := gw.beaconDeliveries()

	// Second change, inside the window. No further event follows it.
	gw.bumpBeaconCounter(t)
	gw.emitBeaconEvent(0)

	waitClient(t, "deferred courier run fires on its own", func() bool {
		return gw.beaconDeliveries() > first
	})
}

// TestBeaconFloodCoalescesToOneDeferredRun keeps the rate limit meaningful now
// that suppression defers: a flood of arrivals must collapse into a single owed
// run rather than queue one per beacon.
func TestBeaconFloodCoalescesToOneDeferredRun(t *testing.T) {
	clientTrustSyncInterval.Store(int64(10 * time.Minute))
	setTriggerIntervalForTest(100 * time.Millisecond)
	t.Cleanup(func() {
		clientTrustSyncInterval.Store(int64(5 * time.Minute))
		setTriggerIntervalForTest(5 * time.Second)
	})

	m, gw := newCourierTestClient(t, 2)
	defer m.Close()

	m.deliverBeacons() // steady state, so every delivery below belongs to the flood
	before := gw.beaconDeliveries()

	// Requested synchronously so the whole flood lands inside one window; each
	// counter bump makes the run that follows it observable as a delivery.
	for i := 0; i < 30; i++ {
		gw.bumpBeaconCounter(t)
		m.ingestBeaconFromDescriptor(gw.descriptor(0))
		m.requestBeaconTrigger(false)
	}
	waitClient(t, "at least one courier run", func() bool { return gw.beaconDeliveries() > before })
	time.Sleep(400 * time.Millisecond) // four more windows: a queue would drain here

	// One node pair, so each run delivers at most one beacon: an immediate run plus
	// the single coalesced deferred one.
	if got := gw.beaconDeliveries() - before; got > 2 {
		t.Fatalf("deliveries = %d; 30 arrivals must coalesce into one deferred run", got)
	}
}
