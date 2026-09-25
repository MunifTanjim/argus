package tui

import (
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// Workspace/project management on the projects screen: create (branch input),
// remove (confirm + force), and curate (rename input, hide/pin toggles).

func newProjectsInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetWidth(40)
	return ti
}

func (m model) inputActive() bool { return m.projects.inputMode != pmNone }

// cursorProjectID is the project the cursor row belongs to (project or workspace
// row); "" on a node row or an empty tree.
func (m model) cursorProjectID() string {
	rows, c := m.projects.rows, m.projects.cursor
	if c < 0 || c >= len(rows) {
		return ""
	}
	switch rows[c].kind {
	case rowProject:
		return rows[c].id
	case rowWorkspace:
		for i := c; i >= 0; i-- {
			if rows[i].kind == rowProject {
				return rows[i].id
			}
		}
	}
	return ""
}

func (m model) findProject(id string) (api.ProjectNode, bool) {
	for _, p := range m.projects.tree {
		if p.ID == id {
			return p, true
		}
	}
	return api.ProjectNode{}, false
}

func (m model) startInput(mode projInputMode, target, initial string) (tea.Model, tea.Cmd) {
	m.projects.inputMode = mode
	m.projects.inputTarget = target
	m.projects.input = newProjectsInput()
	m.projects.input.SetValue(initial)
	return m, m.projects.input.Focus()
}

func (m model) handleProjectsInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == tea.KeyEscape {
		mode := m.projects.inputMode
		m.projects.inputMode = pmNone
		if mode == pmFilter {
			m.projects.setFilter("")
			return m.syncPane()
		}
		return m, nil
	}
	if msg.String() == "enter" {
		val := strings.TrimSpace(m.projects.input.Value())
		mode, target := m.projects.inputMode, m.projects.inputTarget
		m.projects.inputMode = pmNone
		if val == "" {
			return m, nil
		}
		switch mode {
		case pmRename:
			return m, m.renameProjectCmd(target, val)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.projects.input, cmd = m.projects.input.Update(msg)
	if m.projects.inputMode != pmFilter {
		return m, cmd
	}
	m.projects.setFilter(m.projects.input.Value())
	mm, sync := m.syncPane()
	return mm, tea.Batch(cmd, sync)
}

// handleRemoveConfirm consumes the y/n answer to a pending remove.
func (m model) handleRemoveConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	wsID, force := m.projects.pendingRemove, m.projects.pendingRemoveForce
	m.projects.pendingRemove, m.projects.pendingRemoveForce = "", false
	if msg.String() == "y" {
		if m.projects.removing == nil {
			m.projects.removing = map[string]bool{}
		}
		m.projects.removing[wsID] = true
		return m, m.removeWorkspaceCmd(wsID, force)
	}
	return m, nil
}

// --- management key actions (invoked from the tree) ---------------------------

func (m model) actNewWorkspace() (tea.Model, tea.Cmd) {
	projID := m.cursorProjectID()
	if projID == "" {
		m.flash = "select a project or workspace first"
		return m, nil
	}
	return m.startCreate(projID)
}

func (m model) actRenameProject() (tea.Model, tea.Cmd) {
	projID := m.cursorProjectID()
	if projID == "" {
		m.flash = "select a project first"
		return m, nil
	}
	p, _ := m.findProject(projID)
	return m.startInput(pmRename, projID, p.Name)
}

func (m model) actForgetProject() (tea.Model, tea.Cmd) {
	projID := m.cursorProjectID()
	if projID == "" {
		m.flash = "select a project first"
		return m, nil
	}
	p, _ := m.findProject(projID)
	act := m.workspaceActivity()
	live := 0
	for _, w := range p.Workspaces {
		live += act[w.ID].live
	}
	if live > 0 {
		it := "it"
		if live > 1 {
			it = "them"
		}
		m.flash = p.Name + " has " + plural(live, "live session") + " · kill " + it + " first"
		return m, nil
	}
	m.projects.pendingForget = projID
	return m, nil
}

func (m model) forgetProjectCmd(projectID string) tea.Cmd {
	client := m.client
	name := "project"
	if p, ok := m.findProject(projectID); ok {
		name = p.Name
	}
	return func() tea.Msg {
		err := client.Call(api.MethodProjectForget, api.ProjectRef{ProjectID: projectID}, nil)
		return projectsActionMsg{verb: "forget project", ok: "forgot " + name, err: err}
	}
}

func (m model) actToggleHidden() (tea.Model, tea.Cmd) {
	projID := m.cursorProjectID()
	if projID == "" {
		return m, nil
	}
	p, _ := m.findProject(projID)
	ok := "unhid " + p.Name
	if !p.Hidden {
		ok = "hid " + p.Name
		if !m.projects.showHidden {
			ok += " · z shows hidden"
		}
	}
	return m, m.setHiddenCmd(projID, !p.Hidden, ok)
}

func (m model) actTogglePinned() (tea.Model, tea.Cmd) {
	projID := m.cursorProjectID()
	if projID == "" {
		return m, nil
	}
	p, _ := m.findProject(projID)
	ok := "unpinned " + p.Name
	if !p.Pinned {
		ok = "pinned " + p.Name
	}
	return m, m.setPinnedCmd(projID, !p.Pinned, ok)
}

func (m model) actRemoveWorkspace(force bool) (tea.Model, tea.Cmd) {
	if m.projects.cursor >= len(m.projects.rows) {
		return m, nil
	}
	r := m.projects.rows[m.projects.cursor]
	if r.kind != rowWorkspace {
		m.flash = "select a workspace to remove"
		return m, nil
	}
	if r.isMain {
		m.flash = "cannot remove the main worktree"
		return m, nil
	}
	if m.projects.removing[r.id] {
		m.flash = "already removing " + r.label
		return m, nil
	}
	if n := m.workspaceActivity()[r.id].live; n > 0 {
		it := "it"
		if n > 1 {
			it = "them"
		}
		m.flash = r.label + " has " + plural(n, "live session") + " · kill " + it + " first"
		return m, nil
	}
	m.projects.pendingRemove, m.projects.pendingRemoveForce = r.id, force
	return m, nil
}

func (m model) removePrompt() string {
	name := "this workspace"
	if r, ok := m.projects.row(m.projects.pendingRemove); ok {
		name = r.label
		if r.branch != "" {
			name += " (" + r.branch + ")"
		}
	}
	if m.projects.pendingRemoveForce {
		return "force-remove workspace " + name + "? uncommitted changes are lost · y/n"
	}
	return "remove workspace " + name + "? y/n"
}

func (m model) actRetarget() (tea.Model, tea.Cmd) {
	wsID := m.selectedWorkspaceID()
	if wsID == "" {
		m.flash = "select a workspace to change its target"
		return m, nil
	}
	projID := m.cursorProjectID()
	pick := newBranchPicker()
	pick.current = m.targetOf(wsID)
	label := wsID
	if r, ok := m.cursorRow(); ok {
		label = r.label
	}
	m.projects.retarget = &retargetState{workspaceID: wsID, projectID: projID, label: label, pick: pick}
	return m, m.fetchBranchesCmd(projID)
}

func (m model) handleRetargetKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	rt := m.projects.retarget
	if msg.String() == "esc" {
		m.projects.retarget = nil
		return m, nil
	}
	picked, cmd := rt.pick.key(msg)
	if picked == nil {
		return m, cmd
	}
	m.projects.retarget = nil
	return m, m.setTargetCmd(rt.workspaceID, picked.Name)
}

// actSpawnSession starts a session in the selected workspace, or the selected
// project's main workspace, with its node and directory fixed. On the Home row
// it runs the full spawn flow.
func (m model) actSpawnSession() (tea.Model, tea.Cmd) {
	wsID := m.selectedWorkspaceID()
	if r, ok := m.cursorRow(); ok {
		switch r.kind {
		case rowHome:
			return m.actListNew(tea.KeyPressMsg{})
		case rowProject:
			if p, ok := m.findProject(r.id); ok {
				for _, w := range p.Workspaces {
					if w.IsMain {
						wsID = w.ID
					}
				}
			}
		}
	}
	w, ok := m.findWorkspace(wsID)
	switch {
	case !ok:
		m.flash = "select a workspace to start a session in"
		return m, nil
	case w.IsGone:
		m.flash = "this workspace is gone"
		return m, nil
	}
	nodeID, _, _ := session.SplitCompositeID(w.ID)
	return m, m.beginPresetSpawn(nodeID, w.Dir, "")
}

func (m model) findWorkspace(id string) (api.WorkspaceNode, bool) {
	for _, p := range m.projects.tree {
		for _, w := range p.Workspaces {
			if w.ID == id {
				return w, true
			}
		}
	}
	return api.WorkspaceNode{}, false
}

// --- commands -----------------------------------------------------------------

func (m model) removeWorkspaceCmd(workspaceID string, force bool) tea.Cmd {
	client, ok, next := m.client, "removed "+m.workspaceLabel(workspaceID), m.projects.removeNeighbor(workspaceID)
	return func() tea.Msg {
		err := client.Call(api.MethodWorkspaceRemove, api.WorkspaceRemoveParams{WorkspaceID: workspaceID, Force: force}, nil)
		return projectsActionMsg{verb: "remove workspace", ok: ok, selectID: next, removed: workspaceID, err: err}
	}
}

func (m model) renameProjectCmd(projectID, name string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		err := client.Call(api.MethodProjectRename, api.ProjectRenameParams{ProjectID: projectID, Name: name}, nil)
		return projectsActionMsg{verb: "rename", ok: "renamed to " + name, err: err}
	}
}

func (m model) setHiddenCmd(projectID string, hidden bool, ok string) tea.Cmd {
	client, verb := m.client, "hide"
	if !hidden {
		verb = "unhide"
	}
	return func() tea.Msg {
		err := client.Call(api.MethodProjectSetHidden, api.ProjectFlagParams{ProjectID: projectID, Value: hidden}, nil)
		return projectsActionMsg{verb: verb, ok: ok, err: err}
	}
}

func (m model) setPinnedCmd(projectID string, pinned bool, ok string) tea.Cmd {
	client, verb := m.client, "pin"
	if !pinned {
		verb = "unpin"
	}
	return func() tea.Msg {
		err := client.Call(api.MethodProjectSetPinned, api.ProjectFlagParams{ProjectID: projectID, Value: pinned}, nil)
		return projectsActionMsg{verb: verb, ok: ok, err: err}
	}
}

func (m model) setTargetCmd(workspaceID, branch string) tea.Cmd {
	client, ok := m.client, "target of "+m.workspaceLabel(workspaceID)+" → "+branch
	return func() tea.Msg {
		err := client.Call(api.MethodWorkspaceSetTarget, api.WorkspaceSetTargetParams{WorkspaceID: workspaceID, TargetBranch: branch}, nil)
		return projectsActionMsg{verb: "set target", ok: ok, reloadChanges: true, err: err}
	}
}

func (m model) workspaceLabel(id string) string {
	if w, ok := m.findWorkspace(id); ok {
		return filepath.Base(w.Dir)
	}
	return "workspace"
}
