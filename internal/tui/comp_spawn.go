package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// spawnComp is the "new session" flow.
type spawnComp struct {
	spawnState
	back container // the focus to return to when the flow ends
}

func (s spawnComp) section() string { return "spawn" }

// raw while typing; the list steps take resolved keys.
func (s spawnComp) raw(*ctx) bool {
	return s.step == spawnStepPrompt || s.step == spawnStepDir && s.custom || s.filter.on
}

func (s spawnComp) spins(*ctx) bool           { return false }
func (s spawnComp) fullScreen(*ctx) fullLevel { return notFull }
func (s spawnComp) footer(*ctx) []binding     { return nil }
func (s spawnComp) close(*ctx) tea.Cmd        { return nil }
func (s spawnComp) offers(*ctx) []binding     { return nil }
func (s spawnComp) commands(*ctx) []binding   { return nil }
func (s spawnComp) pageStep(c *ctx) int       { return c.m.listPageStep() }
func (s spawnComp) layer() layer              { return overLayer }

// newSpawn starts the full flow. A lone node without tmux stays on the node
// step so its disabled state is visible rather than auto-selected.
func newSpawn(c *ctx, nodes []api.NodeInfo, projects []session.HistoryProject, fallbackCwd string) (spawnComp, tea.Cmd) {
	s := spawnComp{spawnState: spawnState{nodes: nodes, allProjects: projects, fallbackCwd: fallbackCwd}}
	s.cwd = newSpawnCwdInput()
	if len(nodes) >= 2 {
		s.step = spawnStepNode
		return s, nil
	}
	if len(nodes) == 1 {
		if !nodes[0].Capabilities.SpawnSession {
			s.step = spawnStepNode
			return s, nil
		}
		s.nodeID = nodes[0].ID
	}
	cmd := s.startAgentStep(c) // 0 nodes → empty nodeID (sole node, server-side)
	return s, cmd
}

func newPresetSpawn(c *ctx, nodeID, cwd, prompt string) (spawnComp, tea.Cmd) {
	s := spawnComp{spawnState: spawnState{nodeID: nodeID, fixedCwd: true, presetPrompt: prompt}}
	s.cwd = newSpawnCwdInput()
	s.cwd.SetValue(cwd)
	cmd := s.startAgentStep(c)
	return s, cmd
}

func openPresetSpawn(c *ctx, nodeID, cwd, prompt string) tea.Cmd {
	s, cmd := newPresetSpawn(c, nodeID, cwd, prompt)
	s.open(c)
	return cmd
}

func (s spawnComp) open(c *ctx) {
	s.back = c.m.focused
	c.open(s)
	c.focusOn(mainPane)
}

func (s spawnComp) end(c *ctx) {
	c.back()
	c.focusOn(s.back)
}

func (s *spawnComp) startAgentStep(c *ctx) tea.Cmd {
	s.step = spawnStepAgent
	s.agents = nil // loading
	s.cursor = 0
	return c.m.fetchSpawnAgents(s.nodeID)
}

func (s *spawnComp) enterPrompt(c *ctx) tea.Cmd {
	s.step = spawnStepPrompt
	s.prompt = newSpawnPromptArea()
	s.prompt.SetWidth(historyWidth(c.m.bodyWidth()))
	s.prompt.SetHeight(max(1, c.m.bodyHeight()-6))
	return s.prompt.Focus()
}

func (s *spawnComp) afterAgentStep(c *ctx) tea.Cmd {
	if !s.fixedCwd {
		s.enterDirStep()
		return nil
	}
	cmd := s.enterPrompt(c)
	s.prompt.SetValue(s.presetPrompt)
	return cmd
}

func (s spawnComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	switch msg := msg.(type) {
	case spawnAgentsMsg:
		if s.step != spawnStepAgent || msg.nodeID != s.nodeID {
			return s, nil // the flow moved on, or the node changed under a slow probe
		}
		if msg.err != nil || len(msg.agents) <= 1 {
			if len(msg.agents) == 1 {
				s.agent = msg.agents[0].ID
			}
			cmd := s.afterAgentStep(c)
			return s, cmd
		}
		s.agents = msg.agents // cursor stays 0 (set by startAgentStep; frozen while loading)
	case tea.PasteMsg:
		switch {
		case s.step == spawnStepPrompt:
			s.prompt.SetValue(s.prompt.Value() + msg.Content)
		case s.step == spawnStepDir && s.custom:
			// A path is one line: strip line breaks so a pasted CRLF or LF cannot
			// submit or corrupt it.
			cleaned := strings.NewReplacer("\r", "", "\n", "").Replace(msg.Content)
			s.cwd.SetValue(s.cwd.Value() + cleaned)
		}
	}
	return s, nil
}

// listKey reports whether msg was a list key.
func (s *spawnComp) listKey(c *ctx, msg tea.KeyPressMsg, n int) bool {
	m, k, step := c.m, listKeys, c.m.cardListPageStep()
	switch {
	case m.matches(msg, k.Up):
		s.cursor = cursorUp(s.cursor)
	case m.matches(msg, k.Down):
		s.cursor = cursorDown(s.cursor, n)
	case m.matches(msg, k.Top):
		s.cursor = 0
	case m.matches(msg, k.Bottom):
		s.cursor = cursorBottom(n)
	case m.matches(msg, k.HalfUp):
		s.cursor = max(0, s.cursor-step)
	case m.matches(msg, k.HalfDown):
		s.cursor = min(cursorBottom(n), s.cursor+step)
	case m.matches(msg, k.PageUp):
		s.cursor = cursorBy(s.cursor, -2*step, n)
	case m.matches(msg, k.PageDown):
		s.cursor = cursorBy(s.cursor, 2*step, n)
	case m.matches(msg, k.Open):
		return true
	}
	return false
}

func (s spawnComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	if s.filter.on {
		var cmd tea.Cmd
		s.filter, cmd, s.dirQuery = s.filter.handleKey(msg)
		s.dirQuery = strings.TrimSpace(s.dirQuery)
		s.cursor = min(s.cursor, cursorBottom(s.dirCursorMax()))
		return s, cmd, true
	}
	// Typed keys stay literal, so only esc leaves while typing.
	back := msg.String() == "esc"
	if !s.raw(c) {
		back = c.m.matches(msg, listKeys.Back)
	}
	switch {
	case back && s.step == spawnStepDir && !s.custom && s.dirQuery != "":
		// A kept filter clears first, as in every other list.
		s.dirQuery, s.cursor = "", 0
		return s, nil, true
	case back:
		s.end(c)
		return s, nil, true
	}
	var cmd tea.Cmd
	switch s.step {
	case spawnStepNode:
		if s.listKey(c, msg, len(s.nodes)) {
			if s.cursor < len(s.nodes) {
				n := s.nodes[s.cursor]
				if !n.Capabilities.SpawnSession {
					break // disabled: no tmux on this node
				}
				s.nodeID = n.ID
				cmd = s.startAgentStep(c)
			}
		}
	case spawnStepAgent:
		if s.agents == nil {
			break // still probing; ignore keys but esc
		}
		if s.listKey(c, msg, len(s.agents)) {
			if s.cursor < len(s.agents) {
				s.agent = s.agents[s.cursor].ID
				cmd = s.afterAgentStep(c)
			}
		}
	case spawnStepDir:
		if s.custom {
			if msg.String() == "enter" {
				cmd = s.enterPrompt(c)
				break
			}
			s.cwd, cmd = s.cwd.Update(msg)
			break
		}
		if c.m.matches(msg, spawnKeys.Filter) {
			s.filter, cmd = s.filter.start(s.dirQuery)
			break
		}
		if s.listKey(c, msg, s.dirCursorMax()) {
			if dirs := s.shownDirs(); s.cursor < len(dirs) {
				s.cwd.SetValue(dirs[s.cursor].Cwd)
				cmd = s.enterPrompt(c)
				break
			}
			// the trailing "Custom path…" row
			s.custom = true
			s.cwd.SetValue(s.fallbackCwd)
			cmd = s.cwd.Focus()
		}
	case spawnStepPrompt:
		if msg.String() == "enter" {
			if strings.TrimSpace(s.prompt.Value()) == "" {
				break // mandatory: don't spawn without a prompt
			}
			s.end(c)
			cmd = c.m.spawnCmd(strings.TrimSpace(s.cwd.Value()), s.nodeID, s.agent, s.prompt.Value())
			break
		}
		s.prompt, cmd = s.prompt.Update(msg)
	}
	return s, cmd, true
}

func (s spawnComp) click(c *ctx, t hitTarget, focused bool) (component, tea.Cmd) {
	if focused && t.index == s.cursor {
		comp, cmd, _ := s.handleKey(c, enterKey)
		return comp, cmd
	}
	s.cursor = t.index
	return s, nil
}

func (s spawnComp) wheel(_ *ctx, d int) (component, tea.Cmd) {
	if n := s.choices(); n > 0 {
		s.cursor = cursorBy(s.cursor, d, n)
	}
	return s, nil
}

func (s spawnComp) choices() int {
	switch {
	case s.step == spawnStepNode:
		return len(s.nodes)
	case s.step == spawnStepAgent:
		return len(s.agents)
	case s.step == spawnStepDir && !s.custom:
		return s.dirCursorMax()
	}
	return 0
}

func (s spawnComp) view(c *ctx, w, h int) string {
	m := c.m
	cardW := historyWidth(w)
	title := headerStyle.Render("new session")
	avail := max(1, h-4)
	var body string
	switch s.step {
	case spawnStepNode:
		cards := make([]string, len(s.nodes))
		for i, n := range s.nodes {
			sub := ""
			if !n.Capabilities.SpawnSession {
				sub = "no tmux" // disabled: can't spawn here
			}
			cards[i] = spawnChoiceRow(nodeName(n.Label, n.ID), sub, i == s.cursor, cardW)
		}
		body = StyleSecondaryBold.Render("Spawn on which node?") + "\n\n" +
			renderCardList(c.below(4), "spawn", cards, s.cursor, max(1, avail-2))
	case spawnStepAgent:
		if s.agents == nil {
			body = StyleSecondaryBold.Render("Which agent?") + "\n\n" +
				dimStyle.Render("Detecting agents…")
			break
		}
		cards := make([]string, len(s.agents))
		for i, a := range s.agents {
			cards[i] = spawnChoiceRow(a.Name, "", i == s.cursor, cardW)
		}
		body = StyleSecondaryBold.Render("Which agent?") + "\n\n" +
			renderCardList(c.below(4), "spawn", cards, s.cursor, max(1, avail-2))
	case spawnStepDir:
		if s.custom {
			ci := s.cwd
			ci.SetWidth(cardW - 1)
			body = StyleSecondaryBold.Render("Working directory") + "\n\n" +
				asstStyle.Render(ci.View())
			break
		}
		dirs := s.shownDirs()
		cards := make([]string, 0, len(dirs)+1)
		for i, p := range dirs {
			cards = append(cards, spawnChoiceRow(p.Label, p.Cwd, i == s.cursor, cardW))
		}
		cards = append(cards, spawnChoiceRow("Custom path…", "", s.cursor == len(dirs), cardW))
		head := StyleSecondaryBold.Render("Choose a directory")
		switch {
		case s.filter.on:
			head += "\n" + s.filter.view()
		case s.dirQuery != "":
			head += dimStyle.Render("  /" + s.dirQuery)
		}
		rows := strings.Count(head, "\n") + 2 // head and the blank line
		body = head + "\n\n" +
			renderCardList(c.below(2+rows), "spawn", cards, s.cursor, max(1, avail-rows))
	case spawnStepPrompt:
		head := StyleSecondaryBold.Render("Initial prompt") + " " + dimStyle.Render("(required)")
		rows := avail - 2
		if s.fixedCwd { // the dir step was skipped, so say where it runs
			head += "\n" + dimStyle.Render("in "+truncateLeft(s.cwd.Value(), max(1, cardW-3)))
			rows--
		}
		if m.setupRunningAt(s.cwd.Value()) {
			head += "\n" + dimStyle.Render("setup is still running")
			rows--
		}
		ta := s.prompt
		ta.SetWidth(cardW)
		ta.SetHeight(max(1, rows))
		body = head + "\n\n" + ta.View()
	}
	return centerBlock(title+"\n\n"+body, cardW, w)
}

func (s spawnComp) footerText(c *ctx) string {
	switch {
	case s.step == spawnStepAgent && s.agents == nil:
		return dimStyle.Render("esc cancel")
	case s.step == spawnStepDir && s.custom:
		return dimStyle.Render("type a path · enter confirm · esc cancel")
	case s.step == spawnStepPrompt:
		return dimStyle.Render("enter launch · shift+enter/^j newline · esc cancel")
	case s.step == spawnStepDir:
		esc := "esc cancel"
		if s.dirQuery != "" {
			esc = "esc clear filter"
		}
		return dimStyle.Render("↑/↓ move · " + c.m.keyTextOn(s.section(), spawnKeys.Filter) + " filter · enter select · " + esc)
	}
	return dimStyle.Render("↑/↓ move · enter select · esc cancel")
}

func (m model) spawnTop() (spawnComp, bool) {
	s, ok := m.main.top().(spawnComp)
	return s, ok
}

func (m model) topSpawn() int {
	if _, ok := m.spawnTop(); ok {
		return len(m.main) - 1
	}
	return -1
}

func (m model) spawnAt() int {
	for i := len(m.main) - 1; i >= 0; i-- {
		if _, ok := m.main[i].(spawnComp); ok {
			return i
		}
	}
	return -1
}

// beginSpawn opens the full flow, in place of one already open. The reply that
// starts it can arrive while the live screen shows; the flow then waits under
// the screen, which keeps every key.
func (m model) beginSpawn(nodes []api.NodeInfo, projects []session.HistoryProject, fallbackCwd string) (model, tea.Cmd) {
	c := &ctx{m: &m}
	s, cmd := newSpawn(c, nodes, projects, fallbackCwd)
	switch i, top := m.spawnAt(), len(m.main)-1; {
	case i >= 0:
		s.back = m.main[i].(spawnComp).back
		m.main = m.main.replaceAt(i, s)
	case m.topScreen() >= 0:
		m.disarmRoot()
		s.back, m.focused = m.focused, mainPane
		m.main = append(m.main[:top:top], s, m.main[top])
	default:
		m.disarmRoot()
		s.open(c)
	}
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}

func (m model) updateSpawn(i int, msg tea.Msg) (model, tea.Cmd) {
	if i < 0 {
		return m, nil
	}
	c := &ctx{m: &m}
	comp, cmd := m.main[i].update(c, msg)
	m.main = m.main.replaceAt(i, comp)
	cmd = tea.Batch(cmd, m.apply(c))
	return m, cmd
}
