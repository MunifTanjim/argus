package e2elive

import (
	"reflect"
	"sort"
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

// TestAgentIsVisibleOnItsOwnNodeOnlyPlaintext mirrors
// TestAgentIsVisibleOnItsOwnNodeOnly for the plaintext cipher mode.
func TestAgentIsVisibleOnItsOwnNodeOnlyPlaintext(t *testing.T) {
	c := NewPlaintext(t)
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

// TestEachNodeReportsItsOwnAgentPlaintext mirrors
// TestEachNodeReportsItsOwnAgent for the plaintext cipher mode.
func TestEachNodeReportsItsOwnAgentPlaintext(t *testing.T) {
	c := NewPlaintext(t)
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

// TestNodeRestartsAndRejoinsPlaintext verifies that after a node restart in
// plaintext mode the gateway re-lists it online and the client can reach its
// sessions over a freshly established relay channel. A container restart ends
// the live agent session (the tmux server is gone), so the test re-plants the
// agent after restart and confirms the client sees it through the new channel.
func TestNodeRestartsAndRejoinsPlaintext(t *testing.T) {
	c := NewPlaintext(t)
	c.StartGateway()
	a := c.AddNode("node-a")
	c.WaitOnline("node-a")

	a.StartAgent("work", "sid-node-a")

	cl := c.NewClient()
	waitChannels(t, cl, "node-a")

	waitSessions(t, cl, "initial session", func(ss []session.Session) bool {
		return len(ss) > 0
	})

	c.RestartNode("node-a")
	c.WaitOnline("node-a")

	// After the restart a new relay channel must be established.
	waitChannels(t, cl, "node-a")

	// The old agent session died with the container; re-plant it.
	a.StartAgent("work", "sid-node-a")

	found := waitSessions(t, cl, "session after restart", func(ss []session.Session) bool {
		return len(ss) > 0
	})

	if len(found) != 1 {
		t.Fatalf("sessions after restart = %d, want 1: %+v", len(found), found)
	}
	if found[0].NodeID != "node-a" {
		t.Fatalf("session node after restart = %q, want node-a", found[0].NodeID)
	}
}

// sessionEntry is the subset of a session used for parity comparison.
type sessionEntry struct {
	NodeID         string
	AgentSessionID string
}

// sessionEntries extracts and sorts the parity-comparable fields from a session list.
func sessionEntries(sessions []session.Session) []sessionEntry {
	entries := make([]sessionEntry, len(sessions))
	for i, s := range sessions {
		entries[i] = sessionEntry{NodeID: s.NodeID, AgentSessionID: s.AgentSessionID}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].NodeID < entries[j].NodeID
	})
	return entries
}

// runSessionScenario starts a two-node cluster under c, plants one agent per
// node, and returns the merged session list seen by a client.
func runSessionScenario(t *testing.T, c *Cluster) []sessionEntry {
	t.Helper()
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
	return sessionEntries(found)
}

// TestLiveParityPlainVsE2EE runs the same session fan-out scenario in both
// cipher modes and asserts the client-visible merged session list is structurally
// identical — same nodes, same agent session IDs. This guards against the
// plaintext relay path dropping or duplicating sessions compared with E2EE.
func TestLiveParityPlainVsE2EE(t *testing.T) {
	plain := runSessionScenario(t, NewPlaintext(t))
	enc := runSessionScenario(t, New(t))

	if !reflect.DeepEqual(plain, enc) {
		t.Fatalf("plaintext vs e2ee session parity mismatch:\n  plain: %+v\n  e2ee:  %+v", plain, enc)
	}
}
