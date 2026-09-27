package tui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"

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
	step         spawnStep
	nodes        []api.NodeInfo           // node step shown when ≥2
	allProjects  []session.HistoryProject // unfiltered, server order
	dirs         []session.HistoryProject // projects filtered to nodeID
	nodeID       string                   // chosen node ("" = local/single)
	agent        string                   // chosen agent id ("" = node default)
	agents       []api.AgentInfo          // agents launchable on the node; nil while probing
	cursor       int                      // list cursor (node, agent, and dir steps)
	custom       bool                     // dir step: free-text path entry active
	cwd          textinput.Model          // resolved working directory
	prompt       textarea.Model           // initial prompt (mandatory; multi-line via shift+enter)
	fallbackCwd  string                   // seeds custom path / empty-history case
	fixedCwd     bool                     // cwd preset by the caller; skip the dir step
	presetPrompt string                   // seeds the prompt step
}

func newSpawnCwdInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	return ti
}

func newSpawnPromptArea() textarea.Model {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	// enter submits (router), so newlines come from shift+enter or the ctrl+j fallback.
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "ctrl+j"))
	return ta
}

// dirCursorMax is the dir-step selectable row count: one per project plus the
// trailing "Custom path…" row.
func (s spawnState) dirCursorMax() int { return len(s.dirs) + 1 }

// enterDirStep filters projects to the chosen node, positions at the most recent,
// and falls into custom path entry when there are no projects.
func (s *spawnState) enterDirStep() {
	s.step = spawnStepDir
	s.dirs = projectsForNode(s.allProjects, s.nodeID)
	s.cursor = 0
	if len(s.dirs) == 0 {
		s.custom = true
		s.cwd.SetValue(s.fallbackCwd)
		s.cwd.Focus()
	}
}
