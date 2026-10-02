package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func withMouse(m model) model {
	m.mouse = true
	return m
}

func TestMouseModeFollowsTheToggle(t *testing.T) {
	m := homeTestModel()
	if m.View().MouseMode != tea.MouseModeNone {
		t.Error("with the mouse off, Home must leave the mouse to the terminal")
	}
	m = withMouse(m)
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Error("with the mouse on, Home must capture the mouse")
	}
}

func TestToggleMouseCommand(t *testing.T) {
	m := withMouse(homeTestModel())
	m, _ = upd(m, cmdMsg(projectsKeys.ToggleMouse.name))
	if m.mouse || m.flash != "mouse off" {
		t.Fatalf("mouse=%v flash=%q, want off", m.mouse, m.flash)
	}
	m, _ = upd(m, cmdMsg(projectsKeys.ToggleMouse.name))
	if !m.mouse || m.flash != "mouse on" {
		t.Fatalf("mouse=%v flash=%q, want on", m.mouse, m.flash)
	}
}
