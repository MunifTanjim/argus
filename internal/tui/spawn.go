package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// projectsForNode keeps the projects belonging to nodeID, preserving the
// server's newest-first order. An empty nodeID (local or single-node) keeps all.
func projectsForNode(all []session.HistoryProject, nodeID string) []session.HistoryProject {
	if nodeID == "" {
		return all
	}
	out := make([]session.HistoryProject, 0, len(all))
	for _, p := range all {
		if p.NodeID == nodeID {
			out = append(out, p)
		}
	}
	return out
}

type spawnStep int

const (
	spawnInactive spawnStep = iota
	spawnStepNode
	spawnStepDir
	spawnStepPrompt
)

// spawnState drives the staged "new session" flow. It is the single source of
// truth for what the spawn footer renders and what keys do.
type spawnState struct {
	step        spawnStep
	nodes       []api.NodeInfo           // when ≥2, the node step is shown
	allProjects []session.HistoryProject // unfiltered, as returned by the server
	dirs        []session.HistoryProject // projects filtered to nodeID (the dir list)
	nodeID      string                   // chosen node ("" = local/single)
	cursor      int                      // list cursor (node and dir steps)
	custom      bool                     // dir step: free-text path entry active
	cwd         string                   // resolved working directory
	prompt      string                   // initial prompt buffer (mandatory; multi-line via shift+enter)
	fallbackCwd string                   // seeds custom path / empty-history case
}

func (s spawnState) active() bool { return s.step != spawnInactive }

// dirCursorMax is the selectable row count in the dir step: one per project plus
// the trailing "Custom path…" row.
func (s spawnState) dirCursorMax() int { return len(s.dirs) + 1 }

// editText applies a keypress to a free-text buffer. It returns the new text and
// whether Enter was pressed (submit). Mirrors the idle composer in
// prompt_handlers.go: named keys via msg.String(), printable runes via msg.Text.
func editText(cur string, msg tea.KeyPressMsg) (string, bool) {
	switch msg.String() {
	case "enter":
		return cur, true
	case "backspace":
		if cur == "" {
			return cur, false
		}
		_, sz := utf8.DecodeLastRuneInString(cur)
		return cur[:len(cur)-sz], false
	}
	if msg.Text != "" {
		return cur + msg.Text, false
	}
	return cur, false
}

// beginSpawn initializes the staged flow. With ≥2 nodes it starts at the node
// step; otherwise it records the single node (if any) and drops to the dir step,
// pre-selecting the most recent project. Empty history opens free-text path
// entry seeded with fallbackCwd.
func (m *model) beginSpawn(nodes []api.NodeInfo, projects []session.HistoryProject, fallbackCwd string) {
	m.spawn = spawnState{nodes: nodes, allProjects: projects, fallbackCwd: fallbackCwd}
	if len(nodes) >= 2 {
		m.spawn.step = spawnStepNode
		return
	}
	if len(nodes) == 1 {
		m.spawn.nodeID = nodes[0].NodeID
	}
	m.spawn.enterDirStep()
}

// enterDirStep filters projects to the chosen node, positions at the most recent,
// and falls into custom path entry when there are no projects.
func (s *spawnState) enterDirStep() {
	s.step = spawnStepDir
	s.dirs = projectsForNode(s.allProjects, s.nodeID)
	s.cursor = 0
	if len(s.dirs) == 0 {
		s.custom = true
		s.cwd = s.fallbackCwd
	}
}

// handleSpawnKey routes a keypress through the active spawn step. Esc cancels.
func (m model) handleSpawnKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.spawn = spawnState{}
		return m, nil
	}
	switch m.spawn.step {
	case spawnStepNode:
		switch msg.String() {
		case "up", "k":
			m.spawn.cursor = cursorUp(m.spawn.cursor)
		case "down", "j":
			m.spawn.cursor = cursorDown(m.spawn.cursor, len(m.spawn.nodes))
		case "enter":
			if m.spawn.cursor < len(m.spawn.nodes) {
				m.spawn.nodeID = m.spawn.nodes[m.spawn.cursor].NodeID
				m.spawn.enterDirStep()
			}
		}
		return m, nil
	case spawnStepDir:
		if m.spawn.custom {
			txt, submit := editText(m.spawn.cwd, msg)
			m.spawn.cwd = txt
			if submit {
				m.spawn.step = spawnStepPrompt
			}
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			m.spawn.cursor = cursorUp(m.spawn.cursor)
		case "down", "j":
			m.spawn.cursor = cursorDown(m.spawn.cursor, m.spawn.dirCursorMax())
		case "enter":
			if m.spawn.cursor < len(m.spawn.dirs) {
				m.spawn.cwd = m.spawn.dirs[m.spawn.cursor].Cwd
				m.spawn.step = spawnStepPrompt
			} else { // the trailing "Custom path…" row
				m.spawn.custom = true
				m.spawn.cwd = m.spawn.fallbackCwd
			}
		}
		return m, nil
	case spawnStepPrompt:
		switch msg.String() {
		case "enter":
			if strings.TrimSpace(m.spawn.prompt) == "" {
				return m, nil // mandatory: don't spawn without a prompt
			}
			cwd := strings.TrimSpace(m.spawn.cwd)
			prompt := m.spawn.prompt
			nodeID := m.spawn.nodeID
			m.spawn = spawnState{}
			return m, m.spawnCmd(cwd, nodeID, prompt)
		case "shift+enter", "ctrl+j":
			// shift+enter needs the Kitty keyboard protocol; ctrl+j is a
			// universally-transmitted fallback for inserting a newline.
			m.spawn.prompt += "\n"
		default:
			// Backspace and printable runes share the idle-composer editor.
			m.spawn.prompt, _ = editText(m.spawn.prompt, msg)
		}
		return m, nil
	}
	return m, nil
}
