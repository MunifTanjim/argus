package e2elive

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/session"
)

// waitChannels blocks until the client holds an established E2E channel to every
// named node. NewClient returns before openChannel has completed its handshake,
// and a sessions fan-out only reaches the channels that exist when it runs, so a
// count taken too early can omit a node without saying so — which would let a
// fleet that duplicates one node's agents still look correct.
func waitChannels(t *testing.T, cl *client.ReconnectingE2EClient, ids ...string) {
	t.Helper()
	for _, id := range ids {
		waitFor(t, "e2e channel to "+id, func() bool {
			var r api.AgentsListResult
			return cl.Call(api.MethodAgentsList, api.AgentsListParams{NodeID: id}, &r) == nil
		})
	}
}

// waitSessions rescans every node until want accepts the result. Discovery has no
// ticker, so a plain list can race a freshly started agent. A failing call is
// retried rather than fatal, because one is normal while a node is still
// settling, but the last failure is reported on timeout so that a broken
// transport cannot present itself as a missing session.
func waitSessions(t *testing.T, cl *client.ReconnectingE2EClient, what string, want func([]session.Session) bool) []session.Session {
	t.Helper()
	var last []session.Session
	var lastErr error
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var out []session.Session
		if err := cl.Call(api.MethodSessionsRefresh, nil, &out); err != nil {
			lastErr = err
		} else {
			lastErr, last = nil, out
			if want(out) {
				return out
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (last sessions.refresh error: %v; last result: %+v)", what, lastErr, last)
	return nil
}

// TestAgentIsVisibleOnItsOwnNodeOnly is the container isolation regression test.
// On the process-based harness both nodes shared one tmux server and one process
// table, so a single agent appeared twice.
func TestAgentIsVisibleOnItsOwnNodeOnly(t *testing.T) {
	c := New(t)
	c.StartGateway()
	a := c.AddNode("node-a")
	c.AddNode("node-b")
	c.WaitOnline("node-a", "node-b")

	a.StartAgent("work", "sid-node-a")

	cl := c.NewClient()
	waitChannels(t, cl, "node-a", "node-b")

	found := waitSessions(t, cl, "node-a session to appear", func(ss []session.Session) bool {
		return len(ss) > 0
	})

	if len(found) != 1 {
		t.Fatalf("sessions = %d, want 1: %+v", len(found), found)
	}
	if found[0].NodeID != "node-a" {
		t.Fatalf("session node = %q, want node-a", found[0].NodeID)
	}
	if found[0].AgentSessionID != "sid-node-a" {
		t.Fatalf("agent session id = %q, want sid-node-a", found[0].AgentSessionID)
	}
}

// TestEachNodeReportsItsOwnAgent proves the fleet aggregates rather than
// duplicating one machine.
func TestEachNodeReportsItsOwnAgent(t *testing.T) {
	c := New(t)
	c.StartGateway()
	a := c.AddNode("node-a")
	b := c.AddNode("node-b")
	c.WaitOnline("node-a", "node-b")

	a.StartAgent("alpha", "sid-alpha")
	b.StartAgent("beta", "sid-beta")

	cl := c.NewClient()
	waitChannels(t, cl, "node-a", "node-b")

	found := waitSessions(t, cl, "both sessions to appear", func(ss []session.Session) bool {
		return len(ss) == 2
	})

	byNode := map[string]string{}
	for _, s := range found {
		byNode[s.NodeID] = s.AgentSessionID
	}
	if byNode["node-a"] != "sid-alpha" {
		t.Fatalf("node-a session = %q, want sid-alpha (all: %+v)", byNode["node-a"], found)
	}
	if byNode["node-b"] != "sid-beta" {
		t.Fatalf("node-b session = %q, want sid-beta (all: %+v)", byNode["node-b"], found)
	}
}
