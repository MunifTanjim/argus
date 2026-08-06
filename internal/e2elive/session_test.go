package e2elive

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/session"
)

// refreshSessions rescans on every node and returns what the client sees.
// Discovery has no ticker, so a plain list can race a freshly started agent. A
// call that errors returns nil, which lets the caller keep polling.
func refreshSessions(cl *client.ReconnectingE2EClient) []session.Session {
	var out []session.Session
	if err := cl.Call(api.MethodSessionsRefresh, nil, &out); err != nil {
		return nil
	}
	return out
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
	var found []session.Session
	waitFor(t, "node-a session to appear", func() bool {
		found = refreshSessions(cl)
		return len(found) > 0
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
	var found []session.Session
	waitFor(t, "both sessions to appear", func() bool {
		found = refreshSessions(cl)
		return len(found) == 2
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
