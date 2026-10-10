package tui

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// groupBy is the dimension that groups the home sessions list. The zero value
// means host.
type groupBy string

const (
	groupByHost      groupBy = "host"
	groupByProject   groupBy = "project"
	groupByWorkspace groupBy = "workspace"
	groupByAgent     groupBy = "agent"
	groupByStatus    groupBy = "status"
)

// groupByCycle is every dimension, in the order the cycle key steps through.
var groupByCycle = []groupBy{groupByHost, groupByProject, groupByWorkspace, groupByAgent, groupByStatus}

func parseGroupBy(s string) (groupBy, bool) {
	g := groupBy(s)
	return g, slices.Contains(groupByCycle, g)
}

func (g groupBy) next() groupBy {
	if g == "" {
		g = groupByHost
	}
	return groupByCycle[(slices.Index(groupByCycle, g)+1)%len(groupByCycle)]
}

// statusOrder is the lifecycle order of status sections; awaiting input has its
// own section.
var statusOrder = []session.Status{
	session.StatusDiscovered, session.StatusStarting, session.StatusWorking, session.StatusIdle, session.StatusDead,
}

const (
	rankNeedsYou = 0
	rankGroup    = 1
	rankOther    = 1 << 10
)

// sectionInfo places a session in the home list: sections sort by rank, then
// key; a header is drawn where the section changes.
type sectionInfo struct {
	key, title string
	rank       int
	icon       StyledIcon // leads the header
	needsYou   bool
	namesHost  bool // the title identifies the host, so cards need not
}

func (a sectionInfo) same(b sectionInfo) bool { return a.rank == b.rank && a.key == b.key }

// otherSection holds the sessions a dimension cannot place; its header takes
// the dimension's icon, dimmed.
func otherSection(icon StyledIcon) sectionInfo {
	icon.Color = ColorTextDim
	return sectionInfo{title: "other", rank: rankOther, icon: icon}
}

func (m model) byHost() bool { return m.groupBy == "" || m.groupBy == groupByHost }

func hostName(label string) string {
	if label == "" {
		return "local"
	}
	return label
}

func (m model) sessionSection(s session.Session) sectionInfo {
	if s.Status == session.StatusAwaitingInput {
		icon := StyledIcon{statusGlyph(s.Status), statusColor(s.Status)}
		return sectionInfo{title: "Needs you", rank: rankNeedsYou, icon: icon, needsYou: true}
	}
	switch m.groupBy {
	case groupByProject:
		p, _, ok := m.workspaceOf(s.WorkspaceID)
		if !ok {
			return otherSection(Icon.Repo)
		}
		return sectionInfo{key: p.Name, title: p.Name, rank: rankGroup, icon: Icon.Repo}
	case groupByWorkspace:
		p, w, ok := m.workspaceOf(s.WorkspaceID)
		if !ok {
			return otherSection(Icon.Folder)
		}
		name, host := filepath.Base(w.Dir), hostName(s.NodeLabel)
		title := p.Name + " / " + name
		if m.grouped() {
			title += " · " + host
		}
		// A project's workspaces sort together, main first, as in the tree.
		main := "1"
		if w.IsMain {
			main = "0"
		}
		key := strings.Join([]string{p.Name, main, name, host, s.WorkspaceID}, "\x00")
		// As in the tree: a folder for the main or a plain workspace, a branch
		// for a worktree.
		icon := Icon.Folder
		if !w.IsMain && p.Kind != "plain" {
			icon = StyledIcon{Icon.Branch.Glyph, ColorTextDim}
		}
		return sectionInfo{key: key, title: title, rank: rankGroup, icon: icon, namesHost: true}
	case groupByAgent:
		name, accent := agentLabel(s.Agent)
		if name == "" {
			return otherSection(StyledIcon{Glyph: glyphRobotSolid})
		}
		return sectionInfo{key: name, title: name, rank: rankGroup, icon: StyledIcon{glyphRobotSolid, accent}}
	case groupByStatus:
		i := slices.Index(statusOrder, s.Status)
		if i < 0 {
			i = len(statusOrder)
		}
		icon := StyledIcon{statusGlyph(s.Status), statusColor(s.Status)}
		return sectionInfo{key: string(s.Status), title: statusWord(s), rank: rankGroup + i, icon: icon}
	default:
		return sectionInfo{key: s.NodeLabel, title: hostName(s.NodeLabel), rank: rankGroup, icon: Icon.Node, namesHost: true}
	}
}

func (m model) workspaceOf(wsID string) (api.ProjectNode, api.WorkspaceNode, bool) {
	if wsID == "" {
		return api.ProjectNode{}, api.WorkspaceNode{}, false
	}
	for _, p := range m.left.tree.data {
		for _, w := range p.Workspaces {
			if w.ID == wsID {
				return p, w, true
			}
		}
	}
	return api.ProjectNode{}, api.WorkspaceNode{}, false
}

// sectionHeader renders a section's header, or "" when it has none: host
// sections show headers only on a gateway, as before group-by existed.
func (m model) sectionHeader(sec sectionInfo) string {
	switch {
	case sec.needsYou:
		return sec.icon.Render() + " " + m.needsYouHeader()
	case m.byHost() && !m.grouped():
		return ""
	}
	h := sec.icon.Render() + " " + StyleSecondaryBold.Render(sec.title)
	if m.sectionOffline(sec) {
		h += dimStyle.Render("  (offline)")
	}
	return h
}

// sectionOffline reports whether every listed session in the section is offline.
func (m model) sectionOffline(sec sectionInfo) bool {
	seen := false
	for _, id := range m.order {
		s := m.sessions[id]
		if !m.sessionSection(s).same(sec) {
			continue
		}
		if !s.Offline {
			return false
		}
		seen = true
	}
	return seen
}
