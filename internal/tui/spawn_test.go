package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
)

// spawnPickClient serves a canned nodes.list and records the spawn it receives.
type spawnPickClient struct {
	nodes       []api.NodeInfo
	spawnCalled bool
	spawnNodeID string
}

func (c *spawnPickClient) Call(method string, params, out any) error {
	switch method {
	case api.MethodNodesList:
		if p, ok := out.(*[]api.NodeInfo); ok {
			*p = c.nodes
		}
	case api.MethodSessionSpawn:
		c.spawnCalled = true
		if sp, ok := params.(api.SpawnParams); ok {
			c.spawnNodeID = sp.NodeID
		}
	}
	return nil
}

func (c *spawnPickClient) Events() <-chan api.Notification { return make(chan api.Notification) }
func (c *spawnPickClient) States() <-chan bool             { return make(chan bool) }
func (c *spawnPickClient) Reconnect()                      {}
func (c *spawnPickClient) Close() error                    { return nil }

// With 2+ nodes, New opens a picker and the spawn routes to the chosen node.
func TestSpawnPickerRoutesToChosenNode(t *testing.T) {
	c := &spawnPickClient{nodes: []api.NodeInfo{
		{NodeID: "alpha", NodeLabel: "Alpha"},
		{NodeID: "beta", NodeLabel: "Beta"},
	}}
	m := newModel(c, false, nil)

	_, cmd := m.actListNew(tea.KeyPressMsg{})
	if cmd == nil {
		t.Fatal("actListNew returned no command")
	}
	mm, _ := m.Update(cmd()) // cmd() -> spawnNodesMsg
	m = mm.(model)
	if !m.spawnPick || len(m.spawnNodes) != 2 {
		t.Fatalf("expected picker with 2 nodes, got pick=%v nodes=%d", m.spawnPick, len(m.spawnNodes))
	}

	mm, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = mm.(model)
	if m.spawnCursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.spawnCursor)
	}

	_, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter returned no spawn command")
	}
	runCmd(cmd)
	if !c.spawnCalled || c.spawnNodeID != "beta" {
		t.Fatalf("spawn called=%v node=%q, want beta", c.spawnCalled, c.spawnNodeID)
	}
}

// A single node (or a plain local node) skips the picker and spawns immediately.
func TestSpawnSingleNodeSkipsPicker(t *testing.T) {
	c := &spawnPickClient{nodes: []api.NodeInfo{{NodeID: "only", NodeLabel: "Only"}}}
	m := newModel(c, false, nil)

	_, cmd := m.actListNew(tea.KeyPressMsg{})
	mm, spawn := m.Update(cmd())
	m = mm.(model)
	if m.spawnPick {
		t.Fatal("single node should not open the picker")
	}
	if spawn == nil {
		t.Fatal("expected an immediate spawn command")
	}
	runCmd(spawn)
	if !c.spawnCalled || c.spawnNodeID != "only" {
		t.Fatalf("spawn called=%v node=%q, want only", c.spawnCalled, c.spawnNodeID)
	}
}

// Esc dismisses the picker without spawning.
func TestSpawnPickerEscCancels(t *testing.T) {
	c := &spawnPickClient{nodes: []api.NodeInfo{
		{NodeID: "alpha", NodeLabel: "Alpha"},
		{NodeID: "beta", NodeLabel: "Beta"},
	}}
	m := newModel(c, false, nil)
	_, cmd := m.actListNew(tea.KeyPressMsg{})
	mm, _ := m.Update(cmd())
	m = mm.(model)

	mm, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = mm.(model)
	if m.spawnPick {
		t.Fatal("esc should dismiss the picker")
	}
	if c.spawnCalled {
		t.Fatal("esc should not spawn")
	}
}
