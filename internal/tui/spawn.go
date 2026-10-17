package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// projectsForNode dedupes by cwd: the same path can appear on multiple nodes or
// repeat in history.
func projectsForNode(all []session.HistoryProject, nodeID string) []session.HistoryProject {
	out := make([]session.HistoryProject, 0, len(all))
	seen := make(map[string]struct{}, len(all))
	for _, p := range all {
		if p.Missing || nodeID != "" && p.NodeID != nodeID {
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
	spawnStepModel
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
	models       []api.ModelInfo          // models the agent offers; nil while loading
	modelsErr    string                   // why the list could not be loaded
	model        string                   // chosen model id ("" = the agent's default)
	cursor       int                      // list cursor (node, agent, model, and dir steps)
	filter       filterPrompt             // model and dir steps: editing query
	query        string                   // model and dir steps: shown rows match this
	custom       bool                     // dir step: free-text path entry active
	dirsSeq      int                      // custom path edits; only the latest one's completion request fires
	cwd          textinput.Model          // resolved working directory
	prompt       textarea.Model           // initial prompt (mandatory; multi-line via shift+enter)
	fixedCwd     bool                     // cwd preset by the caller; skip the dir step
	presetPrompt string                   // seeds the prompt step
}

func newSpawnCwdInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.ShowSuggestions = true
	return ti
}

func newSpawnPromptArea() textarea.Model {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	// enter submits (router), so newlines come from shift+enter or the ctrl+j fallback.
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "ctrl+j"))
	return withWordMotion(ta)
}

func (s spawnState) shownDirs() []session.HistoryProject {
	q := strings.ToLower(s.query)
	if q == "" {
		return s.dirs
	}
	var out []session.HistoryProject
	for _, p := range s.dirs {
		if strings.Contains(strings.ToLower(p.Label), q) || strings.Contains(strings.ToLower(p.Cwd), q) {
			out = append(out, p)
		}
	}
	return out
}

func (s spawnState) shownModels() []api.ModelInfo {
	q := strings.ToLower(s.query)
	if q == "" {
		return s.models
	}
	var out []api.ModelInfo
	for _, mi := range s.models {
		if strings.Contains(strings.ToLower(mi.Name), q) {
			out = append(out, mi)
		}
	}
	return out
}

// modelCursorMax is the model-step selectable row count: "Default" plus one
// per shown model.
func (s spawnState) modelCursorMax() int { return len(s.shownModels()) + 1 }

// dirCursorMax is the dir-step selectable row count: one per shown project
// plus the trailing "Custom path…" row.
func (s spawnState) dirCursorMax() int { return len(s.shownDirs()) + 1 }

// enterDirStep starts on the most recent project.
func (s *spawnState) enterDirStep() {
	s.step = spawnStepDir
	s.dirs = projectsForNode(s.allProjects, s.nodeID)
	s.query, s.filter = "", filterPrompt{}
	s.cursor = 0
}
