package tui

import (
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// loadTerminalsCmd also reads server.info, so the node list that picks where a
// terminal runs stays as fresh as the terminals.
func (m model) loadTerminalsCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var info api.ServerInfo
		_ = client.Call(api.MethodServerInfo, nil, &info)
		var res api.TerminalListResult
		err := client.Call(api.MethodTerminalList, nil, &res)
		return terminalsMsg{list: res.Terminals, failed: res.FailedNodes, nodes: info.Nodes, err: err}
	}
}

// mergeTerminals keeps the previous terminals of the nodes that failed to list.
func mergeTerminals(prev, list []api.Terminal, failed []string) []api.Terminal {
	if len(failed) == 0 {
		return list
	}
	merged := slices.Clone(list)
	for _, t := range prev {
		if slices.Contains(failed, t.NodeID) {
			merged = append(merged, t)
		}
	}
	api.SortTerminalsByNode(merged)
	return merged
}

func (m model) createTerminalCmd(nodeID string) tea.Cmd {
	return m.callTerminalCreate(api.TerminalCreateParams{NodeID: nodeID})
}

// ws is a composite workspace id.
func (m model) createWorkspaceTerminalCmd(ws string) tea.Cmd {
	nodeID, localID, _ := session.SplitCompositeID(ws)
	return m.callTerminalCreate(api.TerminalCreateParams{NodeID: nodeID, WorkspaceID: localID})
}

func (m model) callTerminalCreate(p api.TerminalCreateParams) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var t api.Terminal
		if err := client.Call(api.MethodTerminalCreate, p, &t); err != nil {
			return terminalActionMsg{verb: "new terminal", err: err}
		}
		return terminalActionMsg{verb: "new terminal", created: &t}
	}
}

func (m model) renameTerminalCmd(id, name string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		return terminalActionMsg{verb: "rename terminal", err: client.Call(api.MethodTerminalRename, api.TerminalRenameParams{TerminalID: id, Name: name}, nil)}
	}
}

func (m model) killTerminalCmd(id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		return terminalActionMsg{verb: "kill terminal", err: client.Call(api.MethodTerminalKill, api.TerminalRef{TerminalID: id}, nil)}
	}
}

func terminalTitle(t api.Terminal) string {
	switch {
	case t.Name != "":
		return t.Name
	case t.Command != "":
		return t.Command
	}
	return "terminal"
}

func (m model) terminalByID(id string) (api.Terminal, bool) {
	for _, t := range m.terminals {
		if t.ID == id {
			return t, true
		}
	}
	return api.Terminal{}, false
}

func (m model) terminalNodes() []api.NodeInfo {
	var out []api.NodeInfo
	for _, n := range m.nodeInfo {
		if n.Capabilities.Terminal {
			out = append(out, n)
		}
	}
	return out
}

// terminalClientPane is this TUI's own pane when it runs inside argus's tmux
// server on the terminal's node, so the node can refuse to mirror the window
// that holds this TUI.
func terminalClientPane(t api.Terminal, hostname, tmuxEnv, tmuxPane string) string {
	if !sameMachine(t.NodeID, t.NodeLabel, hostname) || session.TmuxServer(tmux.SocketBaseFromEnv(tmuxEnv)) != session.TmuxServerArgus {
		return ""
	}
	return tmuxPane
}

// attachTerminal opens the live screen on terminal t; row is the tree row that
// the screen belongs to.
func attachTerminal(c *ctx, t api.Terminal, row string) tea.Cmd {
	m := c.m
	cols, rows := m.termDims()
	emu, cur := newScreenEmulator(cols, rows)
	s := screenComp{
		terminalID: t.ID, title: terminalTitle(t), node: nodeName(t.NodeLabel, t.NodeID), row: row,
		termID: newTermID(), term: emu, cursorState: cur, stop: make(chan struct{}),
	}
	go drainEmulator(s.term, s.stop)
	c.open(s)
	host, _ := os.Hostname()
	return m.termOpenCmd(api.TerminalOpenParams{
		TermID: s.termID, TerminalID: t.ID, Cols: cols, Rows: rows,
		ClientPane: terminalClientPane(t, host, os.Getenv("TMUX"), os.Getenv("TMUX_PANE")),
	})
}

// terminalNodePicker asks which node a new terminal runs on.
type terminalNodePicker struct {
	nodes  []api.NodeInfo
	cursor int
}

func (p terminalNodePicker) id() string                            { return "terminal-node" }
func (p terminalNodePicker) keySection() string                    { return "" }
func (p terminalNodePicker) spins(*ctx) bool                       { return false }
func (p terminalNodePicker) update(*ctx, tea.Msg) (popup, tea.Cmd) { return p, nil }

func (p terminalNodePicker) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	switch msg.String() {
	case "esc":
		c.closePopup()
	case "up", "k":
		p.cursor = cursorUp(p.cursor)
	case "down", "j":
		p.cursor = cursorDown(p.cursor, len(p.nodes))
	case "enter":
		c.closePopup()
		return p, c.m.createTerminalCmd(p.nodes[p.cursor].ID)
	}
	return p, nil
}

func (p terminalNodePicker) click(c *ctx, t hitTarget) (popup, tea.Cmd) {
	p.cursor = t.index
	return p.handleKey(c, enterKey)
}

func (p terminalNodePicker) wheel(_ *ctx, d int) (popup, tea.Cmd) {
	p.cursor = cursorBy(p.cursor, d, len(p.nodes))
	return p, nil
}

func (p terminalNodePicker) draw(c *ctx, scr uv.Screen, _ uv.Rectangle) {
	area := c.m.mainRect()
	f := pickerFrame(area)
	f.title = "New terminal on which node?"
	f.help = c.m.hints(f.innerWidth(), hint("↑/↓", "move"), hint("enter", "select"), hint("esc", "cancel"))
	l := itemLines{key: "terminal-node-picker"}
	for i, n := range p.nodes {
		l.add(i, spawnChoiceRow(nodeName(n.Label, n.ID), "", i == p.cursor, f.innerWidth()))
	}
	f.parts = l.window(f.bodyCtx(c), p.cursor, f.bodyHeight())
	c.hitRect(drawCenter(scr, area, f.render()))
}

type nodeTab int

const (
	nodeTabProjects nodeTab = iota
	nodeTabTerminals
)

var nodeTabLabels = []string{"Projects", "Terminals"}

// nodeHasTerminals is false only for a node that reports no tmux; a node not
// in the last server.info gets the tab, and its create reports any error.
func (m model) nodeHasTerminals(nodeID string) bool {
	n, ok := m.node(nodeID)
	return !ok || n.Capabilities.Terminal
}

func (m model) node(id string) (api.NodeInfo, bool) {
	for _, n := range m.nodeInfo {
		if n.ID == id {
			return n, true
		}
	}
	return api.NodeInfo{}, false
}

func (m model) nodeTabs(active nodeTab) string { return m.tabLine(nodeTabLabels, int(active)) }

func (m model) tabLine(labels []string, active int) string {
	parts := make([]string, len(labels))
	for i, l := range labels {
		st := StyleDim
		if i == active {
			st = m.paneHeadStyle()
		}
		parts[i] = st.Render(l)
	}
	return strings.Join(parts, StyleDim.Render("   "))
}

func (m model) hitNodeTabs(c *ctx, x int) { c.hitTabs(x, 0, 3, nodeTabLabels...) }

type wsTab int

const (
	wsTabSessions wsTab = iota
	wsTabTerminals
)

var wsTabLabels = []string{"Sessions", "Terminals"}

// wsTabsHeader shows only the Sessions tab for a node without tmux; x is the
// tabs' column, for their click zones.
func (m model) wsTabsHeader(c *ctx, ws string, active wsTab, x int) string {
	if !m.wsHasTerminals(ws) {
		return m.tabLine(wsTabLabels[:1], 0)
	}
	c.hitTabs(x, 0, 3, wsTabLabels...)
	return m.tabLine(wsTabLabels, int(active))
}

func (m model) wsHasTerminals(ws string) bool {
	nodeID, _, _ := session.SplitCompositeID(ws)
	return m.nodeHasTerminals(nodeID)
}

func openWorkspaceSessions(c *ctx, ws string) { c.replaceBase(workspaceComp{ws: ws}) }

func openWorkspaceTerminals(c *ctx, ws string) tea.Cmd {
	c.replaceBase(terminalsComp{ws: ws})
	return c.m.loadTerminalsCmd()
}

func openNodeTerminals(c *ctx, nodeID string) tea.Cmd {
	c.replaceBase(terminalsComp{nodeID: nodeID})
	return c.m.loadTerminalsCmd()
}
