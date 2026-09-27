package tui

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func (m model) findProject(id string) (api.ProjectNode, bool) { return m.left.tree.findProject(id) }

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

func openSetupLog(c *ctx, wsID string) tea.Cmd {
	if wsID == "" {
		c.setFlash("select a workspace to see its setup log")
		return nil
	}
	fileComp{ws: wsID, path: "setup log", log: true, loading: true, live: c.m.setupRunning(wsID)}.show(c)
	c.focusOn(mainPane)
	return c.m.fetchSetupLog(wsID)
}

func (m model) fetchSetupLog(wsID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var r api.SetupLogResult
		err := client.Call(api.MethodWorkspaceSetupLog, api.WorkspaceRef{WorkspaceID: wsID}, &r)
		return setupLogMsg{ws: wsID, output: r.Output, err: err}
	}
}

func (m model) projectOfWorkspace(wsID string) string { return m.left.tree.projectOfWorkspace(wsID) }

func (m model) removePrompt() string {
	name := "this workspace"
	teardown := ""
	if r, ok := m.left.tree.row(m.left.tree.pendingRemove); ok {
		name = r.label
		if r.branch != "" {
			name += " (" + r.branch + ")"
		}
		if p, ok := m.findProject(m.projectOfWorkspace(r.id)); ok && p.Scripts != nil && p.Scripts.Teardown != "" {
			teardown = "runs teardown: " + commandLine(p.Scripts.Teardown) + " · "
		}
	}
	if m.left.tree.pendingRemoveForce {
		return "force-remove workspace " + name + "? uncommitted changes are lost · " + teardown + "y/n"
	}
	return "remove workspace " + name + "? " + teardown + "y/n"
}

// spawnSession fixes the new session's node and directory to row r's workspace.
func spawnSession(c *ctx, r projectsRow) tea.Cmd {
	m := c.m
	var wsID string
	switch r.kind {
	case rowHome:
		return m.newSessionCmd()
	case rowWorkspace:
		wsID = r.id
	case rowProject:
		if p, ok := m.findProject(r.id); ok {
			for _, w := range p.Workspaces {
				if w.IsMain {
					wsID = w.ID
				}
			}
		}
	}
	w, ok := m.findWorkspace(wsID)
	switch {
	case !ok:
		c.setFlash("select a workspace to start a session in")
		return nil
	case w.IsGone:
		c.setFlash("this workspace is gone")
		return nil
	}
	nodeID, _, _ := session.SplitCompositeID(w.ID)
	return openPresetSpawn(c, nodeID, w.Dir, "")
}

func (m model) findWorkspace(id string) (api.WorkspaceNode, bool) {
	return m.left.tree.findWorkspace(id)
}

func (m model) removeWorkspaceCmd(workspaceID string, force bool) tea.Cmd {
	client, ok, next := m.client, "removed "+m.workspaceLabel(workspaceID), m.left.tree.removeNeighbor(workspaceID)
	return func() tea.Msg {
		var res api.WorkspaceRemoveResult
		err := client.Call(api.MethodWorkspaceRemove, api.WorkspaceRemoveParams{WorkspaceID: workspaceID, Force: force}, &res)
		done := ok
		if res.Warning != "" {
			done += " · " + res.Warning
		}
		return projectsActionMsg{verb: "remove workspace", ok: done, selectID: next, removed: workspaceID, err: err}
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
