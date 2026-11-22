package tui

import (
	tea "charm.land/bubbletea/v2"
)

// agentComp is the agent view: one persistent agent's threads, routines and
// memory.
type agentComp struct {
	agentID string
}

func openAgent(c *ctx, id string) tea.Cmd {
	c.replaceBase(agentComp{agentID: id})
	c.focusOn(mainPane)
	return nil
}

func (a agentComp) section() string                           { return "agent" }
func (a agentComp) raw(*ctx) bool                             { return false }
func (a agentComp) spins(*ctx) bool                           { return false }
func (a agentComp) fullScreen(*ctx) fullLevel                 { return notFull }
func (a agentComp) close(*ctx) tea.Cmd                        { return nil }
func (a agentComp) offers(*ctx) []binding                     { return sectionOffers[a.section()].keys }
func (a agentComp) commands(*ctx) []binding                   { return sectionLists[a.section()].own }
func (a agentComp) layer() layer                              { return baseLayer }
func (a agentComp) pageStep(c *ctx) int                       { return c.m.listPageStep() }
func (a agentComp) update(*ctx, tea.Msg) (component, tea.Cmd) { return a, nil }
func (a agentComp) footer(*ctx) []binding                     { return []binding{listKeys.Back} }

func (a agentComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	if c.m.matches(msg, listKeys.Back) {
		c.focusTree()
		return a, nil, true
	}
	return a, nil, false
}

func (a agentComp) view(c *ctx, w, _ int) string {
	ag, _ := c.m.pagent(a.agentID)
	return centerBlock(ag.Name, min(w, maxCardWidth), w)
}
