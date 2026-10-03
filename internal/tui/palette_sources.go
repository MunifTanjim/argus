package tui

import (
	"slices"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/session"
)

// placeID is the palette id of a tree row. Home is the root, "".
func placeID(kind projRowKind, id string) string {
	switch kind {
	case rowNode:
		return "node:" + id
	case rowProject:
		return "project:" + id
	case rowWorkspace:
		return "ws:" + id
	}
	return ""
}

func placeNoun(kind projRowKind) string {
	switch kind {
	case rowNode:
		return "node"
	case rowProject:
		return "project"
	}
	return "workspace"
}

// The snapshot can be older than the model, so each action looks its target
// up again.

func openSessionAction(id string) paletteAction {
	return paletteAction{name: "open", run: func(c *ctx) {
		if _, ok := c.m.sessions[id]; !ok {
			c.setFlash("session ended")
			return
		}
		c.openSession(id)
	}}
}

func openPlace(kind projRowKind, id string) paletteAction {
	return paletteAction{name: "open", run: func(c *ctx) {
		if !slices.ContainsFunc(placeRows(c.m.left.tree), func(r projectsRow) bool { return r.id == id }) {
			c.setFlash(placeNoun(kind) + " no longer exists")
			return
		}
		c.goToRow(id)
	}}
}

// placeRows are the tree rows that goToRow can show: every row that the
// tree's hidden and gone settings keep, with no fold and no filter.
func placeRows(t projectTreeComp) []projectsRow {
	return buildProjectRows(projectTreeComp{data: t.data, showHidden: t.showHidden, showGone: t.showGone})
}

type placesSource struct{}

// items follow the tree: its order, its hidden and gone settings, and its node
// rows, which show only with more than one node. The tree's filter and folds
// do not apply.
func (placesSource) items(m model) []paletteItem {
	var out []paletteItem
	var node, nodeLabel, project, projectName string
	for _, r := range placeRows(m.left.tree) {
		id := placeID(r.kind, r.id)
		it := paletteItem{id: id, label: r.label, actions: []paletteAction{openPlace(r.kind, r.id)}}
		switch r.kind {
		case rowNode:
			node, nodeLabel = id, r.label
			it.kind, it.marker, it.detail = paletteNode, Icon.Node.Render(), "node"
		case rowProject:
			project, projectName = id, r.label
			it.kind, it.marker, it.detail, it.parent = paletteProject, Icon.Repo.Render(), nodeLabel, node
		case rowWorkspace:
			it.kind, it.marker, it.detail, it.parent = paletteWorkspace, wsIcon(r), projectName, project
			it.name = r.label
			if !r.isGone && !r.plain && r.branch != "" {
				it.label += " " + r.branch
			}
		default:
			continue
		}
		out = append(out, it)
	}
	return out
}

type sessionsSource struct{}

// items are every session that the TUI knows, in the home list's order, then
// the sessions that the list filters out. The list's filters do not apply.
func (sessionsSource) items(m model) []paletteItem {
	ids := slices.Clone(m.order)
	var rest []string
	for id := range m.sessions {
		if !slices.Contains(ids, id) {
			rest = append(rest, id)
		}
	}
	slices.Sort(rest)
	many := treeNodeCount(m.left.tree) > 1
	var out []paletteItem
	for _, id := range append(ids, rest...) {
		s, ok := m.sessions[id]
		if !ok {
			continue
		}
		// An offline session reads as inactive, as on its card.
		glyph, color := statusGlyph(s.Status), statusColor(s.Status)
		if s.Offline {
			glyph, color = "○", ColorBorder
		}
		it := paletteItem{
			id:      "session:" + id,
			kind:    paletteSession,
			marker:  lipgloss.NewStyle().Foreground(color).Render(glyph),
			label:   sessionLabel(s),
			detail:  sessionDetail(s, many),
			waiting: s.Status == session.StatusAwaitingInput && !s.Offline,
			actions: []paletteAction{openSessionAction(id)},
		}
		if s.WorkspaceID != "" {
			it.parent = placeID(rowWorkspace, s.WorkspaceID)
		}
		out = append(out, it)
	}
	return out
}

func sessionLabel(s session.Session) string {
	for _, l := range []string{s.Name, s.Tmux.SessionName, s.Agent} {
		if l != "" {
			return l
		}
	}
	return s.ID
}

func sessionDetail(s session.Session, many bool) string {
	parts := []string{s.Repo, s.Branch}
	if many {
		parts = append(parts, s.NodeLabel)
	}
	return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), " · ")
}

// treeNodeCount decides whether the tree shows node rows.
func treeNodeCount(t projectTreeComp) int {
	nodes := map[string]bool{}
	for _, p := range t.data {
		nodes[p.NodeID] = true
	}
	return len(nodes)
}

type commandsSource struct{}

// items are the commands that the command line offers in the section under
// the palette, by name, with their keys as hints. Each runs the way the
// command line runs it.
func (commandsSource) items(m model) []paletteItem {
	set := slices.SortedFunc(slices.Values(m.commandSet()), func(a, b binding) int { return strings.Compare(a.name, b.name) })
	sk := m.keymap().screenKeys(m.screen())
	out := make([]paletteItem, 0, len(set))
	for _, b := range set {
		var keys []string
		for _, id := range sk.listedKeys(b) {
			keys = append(keys, keyLabel(id))
		}
		name := b.name
		out = append(out, paletteItem{
			id:      "command:" + name,
			kind:    paletteCommand,
			marker:  StyleDim.Render(">"),
			label:   name,
			hint:    strings.Join(keys, " "),
			actions: []paletteAction{{name: "run", run: func(c *ctx) { c.runCommand(name) }}},
		})
	}
	return out
}
