package e2elive

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

// TestSmokeUnlockedRoundTrip proves the full real-process encrypted path
// composes: a client seals a request, the blind gateway relays it opaquely,
// the real node process decrypts and answers, and the client opens the reply.
func TestSmokeUnlockedRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("real-process e2e; skipped under -short")
	}
	c := New(t)
	c.StartGateway()
	c.AddNode("node-a")
	c.AddNode("node-b")
	c.WaitOnline("node-a", "node-b")

	cl := c.NewClient()

	// Cleartext roster served by the blind gateway.
	var roster api.NodesListResult
	if err := cl.Call(api.MethodNodesList, nil, &roster); err != nil {
		t.Fatalf("nodes.list: %v", err)
	}
	got := map[string]bool{}
	for _, nd := range roster.Nodes {
		got[nd.ID] = true
	}
	if !got["node-a"] || !got["node-b"] {
		t.Fatalf("roster missing nodes: %+v", roster.Nodes)
	}

	// Node-addressed call over the sealed E2E channel: sealed by the client,
	// relayed opaquely by the gateway, decrypted+handled by the real node,
	// and the sealed reply opened by the client. Empty result is fine.
	var agents api.AgentsListResult
	if err := cl.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: "node-a"}, &agents); err != nil {
		t.Fatalf("agents.list over e2e: %v", err)
	}
}
