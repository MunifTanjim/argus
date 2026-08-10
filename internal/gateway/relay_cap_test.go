package gateway

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// TestRelayOpenPerClientCap verifies a single client cannot open unbounded relay
// channels (each costs two goroutines + two 64-deep queues). Opens up to the cap
// succeed; one past it is rejected; a different client is unaffected.
func TestRelayOpenPerClientCap(t *testing.T) {
	srv := NewServer(New(time.Second), nil, nil)
	nodePeer, _ := adoptFakeNode(t, srv, "n1", "PUB1")
	defer nodePeer.Close()
	client, _ := connectFakeClient(t, srv)
	defer client.Close()

	for i := 0; i < maxChannelsPerClient; i++ {
		var res api.RelayOpenResult
		if err := client.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &res); err != nil {
			t.Fatalf("open %d (within cap) must succeed: %v", i, err)
		}
	}
	var res api.RelayOpenResult
	if err := client.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &res); err == nil {
		t.Fatal("opening past the per-client cap must be rejected")
	}

	// A different client must still be able to open.
	client2, _ := connectFakeClient(t, srv)
	defer client2.Close()
	var res2 api.RelayOpenResult
	if err := client2.Call(api.MethodRelayOpen, api.RelayOpenParams{NodeID: "n1"}, &res2); err != nil {
		t.Fatalf("a second client must still be able to open: %v", err)
	}
}
