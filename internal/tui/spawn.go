package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// projectsForNode keeps projects belonging to nodeID (preserving order); an
// empty nodeID keeps all. Deduped by cwd: the same path can appear on multiple
// nodes or repeat in history.
func projectsForNode(all []session.HistoryProject, nodeID string) []session.HistoryProject {
	out := make([]session.HistoryProject, 0, len(all))
	seen := make(map[string]struct{}, len(all))
	for _, p := range all {
		if nodeID != "" && p.NodeID != nodeID {
			continue
		}
		if _, dup := seen[p.Cwd]; dup {
			continue
		}
		seen[p.Cwd] = struct{}{}
		out = append(out, p)
	}
	return out
}

type spawnStep int

const (
	spawnInactive spawnStep = iota
	spawnStepNode
	spawnStepAgent
	spawnStepDir
	spawnStepPrompt
)

// spawnState drives the staged "new session" flow and is the source of truth for
// the spawn footer and key handling.
type spawnState struct {
	step        spawnStep
	nodes       []api.NodeInfo           // node step shown when ≥2
	allProjects []session.HistoryProject // unfiltered, server order
	dirs        []session.HistoryProject // projects filtered to nodeID
	nodeID      string                   // chosen node ("" = local/single)
	agent       string                   // chosen agent id ("" = node default)
	agents      []api.AgentInfo          // agents launchable on the node; nil while probing
	cursor      int                      // list cursor (node, agent, and dir steps)
	custom      bool                     // dir step: free-text path entry active
	cwd         string                   // resolved working directory
	prompt      string                   // initial prompt (mandatory; multi-line via shift+enter)
	fallbackCwd string                   // seeds custom path / empty-history case
}

func (s spawnState) active() bool { return s.step != spawnInactive }

// dirCursorMax is the dir-step selectable row count: one per project plus the
// trailing "Custom path…" row.
func (s spawnState) dirCursorMax() int { return len(s.dirs) + 1 }

// editText applies a keypress to a free-text buffer, returning the new text and
// whether Enter (submit) was pressed. Mirrors the idle composer in
// prompt_handlers.go.
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

// beginSpawn initializes the staged flow. A lone node without tmux stays on the
// node step so its disabled state is visible rather than auto-selected.
func (m *model) beginSpawn(nodes []api.NodeInfo, projects []session.HistoryProject, fallbackCwd string) tea.Cmd {
	m.spawn = spawnState{nodes: nodes, allProjects: projects, fallbackCwd: fallbackCwd}
	if len(nodes) >= 2 {
		m.spawn.step = spawnStepNode
		return nil
	}
	if len(nodes) == 1 {
		if !nodes[0].Capabilities.SpawnSession {
			m.spawn.step = spawnStepNode // surface the disabled node
			return nil
		}
		m.spawn.nodeID = nodes[0].ID
	}
	return m.startAgentStep() // 0 nodes → empty nodeID (sole node, server-side)
}

func (m *model) startAgentStep() tea.Cmd {
	m.spawn.step = spawnStepAgent
	m.spawn.agents = nil // loading
	m.spawn.cursor = 0
	return m.fetchSpawnAgents(m.spawn.nodeID)
}

func (m *model) applySpawnAgents(msg spawnAgentsMsg) {
	if m.spawn.step != spawnStepAgent || msg.nodeID != m.spawn.nodeID {
		return // flow moved on (cancelled), or the node changed under a slow probe
	}
	if msg.err != nil || len(msg.agents) <= 1 {
		if len(msg.agents) == 1 {
			m.spawn.agent = msg.agents[0].ID
		}
		m.spawn.enterDirStep()
		return
	}
	m.spawn.agents = msg.agents // cursor stays 0 (set by startAgentStep; frozen while loading)
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
				n := m.spawn.nodes[m.spawn.cursor]
				if !n.Capabilities.SpawnSession {
					return m, nil // disabled: no tmux on this node
				}
				m.spawn.nodeID = n.ID
				return m, m.startAgentStep()
			}
		}
		return m, nil
	case spawnStepAgent:
		if m.spawn.agents == nil {
			return m, nil // still probing; ignore keys but esc
		}
		switch msg.String() {
		case "up", "k":
			m.spawn.cursor = cursorUp(m.spawn.cursor)
		case "down", "j":
			m.spawn.cursor = cursorDown(m.spawn.cursor, len(m.spawn.agents))
		case "enter":
			if m.spawn.cursor < len(m.spawn.agents) {
				m.spawn.agent = m.spawn.agents[m.spawn.cursor].ID
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
			agent := m.spawn.agent
			m.spawn = spawnState{}
			return m, m.spawnCmd(cwd, nodeID, agent, prompt)
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
