package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// The workflow page: a Workflow run's phases and agents, listed in an items
// frame so cursor, drill, mouse and breadcrumb work as on a subagent trace.

// workflowToolName marks a workflow agent entry (its ToolName) apart from a
// spawned subagent.
const workflowToolName = "Workflow"

// workflowMsg carries a fetched Workflow run for the frames showing it.
type workflowMsg struct {
	owner          transcriptOwner
	runID, agentID string
	view           transcript.WorkflowView
	err            error
}

// workflowPollEvery is how often a live page opened from a trace refetches
// while its run is running; Session.Workflows lists main-session runs only.
const workflowPollEvery = 2 * time.Second

// workflowTickMsg asks the frame holding token to refetch its run.
type workflowTickMsg struct {
	owner transcriptOwner
	token string
}

// workflowRef is a workflow entry's run, if the run can be opened.
func workflowRef(e transcript.Entry) (*transcript.WorkflowRef, bool) {
	ok := e.Kind == transcript.EntryWorkflow && e.Workflow != nil && e.Workflow.RunID != "" && !e.Workflow.Remote
	return e.Workflow, ok
}

func isWorkflowAgent(e transcript.Entry) bool {
	return e.Kind == transcript.EntrySubagent && e.ToolName == workflowToolName
}

// workflowEntries lists a run as a header entry per phase followed by its
// agents; a sole untitled phase gets no header.
func workflowEntries(v transcript.WorkflowView) []transcript.Entry {
	es := make([]transcript.Entry, 0, len(v.Phases))
	for i, p := range v.Phases {
		if p.Title != "" || len(v.Phases) > 1 {
			title := p.Title
			if title == "" {
				title = "Phase " + strconv.Itoa(i+1)
			}
			es = append(es, transcript.Entry{
				ID: "phase:" + strconv.Itoa(i), Kind: transcript.EntryCompact, Summary: title,
			})
		}
		for _, a := range p.Agents {
			es = append(es, transcript.Entry{
				ID: a.ID, Kind: transcript.EntrySubagent, ToolName: workflowToolName,
				Subagents: []transcript.Subagent{{ID: a.ID, Name: a.Label, Status: a.State, HasTrace: a.HasTrace}},
			})
		}
	}
	return es
}

// drillWorkflow pushes the page of run runID, launched from the trace agentID
// ("" = main), and fetches it.
func (m tview) drillWorkflow(runID, agentID string) tea.Cmd {
	m.transcript.detailStack = append(m.transcript.detailStack, detailFrame{
		label: "Workflow", items: []transcript.Entry{}, expanded: map[int]bool{},
		workflowRunID: runID, workflowAgentID: agentID,
	})
	return m.fetchWorkflowCmd(runID, agentID)
}

func (m tview) fetchWorkflowCmd(runID, agentID string) tea.Cmd {
	if !m.live {
		return m.c.m.fetchHistWorkflowFor(m.owner(), m.history.addr(), agentID, runID)
	}
	client, owner, sessionID := m.c.m.client, m.owner(), m.sessionID
	return func() tea.Msg {
		var v transcript.WorkflowView
		err := client.Call(api.MethodSessionWorkflow, api.WorkflowParams{
			SessionID: sessionID, AgentID: agentID, RunID: runID,
		}, &v)
		return workflowMsg{owner: owner, runID: runID, agentID: agentID, view: v, err: err}
	}
}

// applyWorkflow fills the frames showing msg's run, keeping each cursor on its
// row, and schedules the next poll of those that poll.
func (m tview) applyWorkflow(msg workflowMsg) tea.Cmd {
	if msg.err != nil {
		m.c.setFlash("workflow: " + msg.err.Error())
		return nil
	}
	var ticks []tea.Cmd
	for i := range m.transcript.detailStack {
		f := &m.transcript.detailStack[i]
		if f.workflowRunID != msg.runID || f.workflowAgentID != msg.agentID {
			continue
		}
		ref := m.frameRowID(f)
		f.workflowName, f.workflowStatus = msg.view.Name, msg.view.Status
		f.items = workflowEntries(msg.view)
		f.cursor = restoreRowCursor(f.items, m.frameRows(f), ref, f.cursor, false)
		if m.live && f.workflowAgentID != "" && f.workflowStatus == "running" {
			ticks = append(ticks, m.workflowTick(f))
		} else {
			f.workflowTick = ""
		}
	}
	m.clampDetailScroll()
	return tea.Batch(ticks...)
}

// workflowTick schedules f's next poll; a new token voids any pending one, so
// a frame has at most one poll in flight.
func (m tview) workflowTick(f *detailFrame) tea.Cmd {
	f.workflowTick = randID()
	msg := workflowTickMsg{owner: m.owner(), token: f.workflowTick}
	return tea.Tick(workflowPollEvery, func(time.Time) tea.Msg { return msg })
}

// pollWorkflow refetches the frame msg was scheduled for, if it is still open.
func (m tview) pollWorkflow(msg workflowTickMsg) tea.Cmd {
	for i := range m.transcript.detailStack {
		if f := &m.transcript.detailStack[i]; f.workflowTick == msg.token {
			f.workflowTick = ""
			return m.fetchWorkflowCmd(f.workflowRunID, f.workflowAgentID)
		}
	}
	return nil
}

// refetchWorkflows refetches the open pages of a live session's runs whose
// progress changed or that stopped running.
func (m *model) refetchWorkflows(prev, next session.Session) tea.Cmd {
	if slices.Equal(prev.Workflows, next.Workflows) {
		return nil
	}
	return m.editLiveTranscripts(next.ID, func(v tview) tea.Cmd {
		var fetches []tea.Cmd
		for _, f := range v.transcript.detailStack {
			if f.workflowRunID != "" && workflowChanged(prev.Workflows, next.Workflows, f.workflowRunID) {
				fetches = append(fetches, v.fetchWorkflowCmd(f.workflowRunID, f.workflowAgentID))
			}
		}
		return tea.Batch(fetches...)
	})
}

// refetchRemovedWorkflows rereads, from the transcript, the open pages of a
// removed session's runs still shown running: the node no longer serves the
// session live, and a run its agent process left behind reads stopped.
func (m *model) refetchRemovedWorkflows(s session.Session) tea.Cmd {
	if s.TranscriptPath == "" {
		return nil
	}
	addr := histAddr{nodeID: s.NodeID, path: s.TranscriptPath, agent: s.Agent}
	return m.editLiveTranscripts(s.ID, func(v tview) tea.Cmd {
		var fetches []tea.Cmd
		for j := range v.transcript.detailStack {
			f := &v.transcript.detailStack[j]
			if f.workflowRunID == "" || f.workflowStatus != "running" {
				continue
			}
			f.workflowTick = "" // void a pending live poll
			fetches = append(fetches, m.fetchHistWorkflowFor(v.owner(), addr, f.workflowAgentID, f.workflowRunID))
		}
		return tea.Batch(fetches...)
	})
}

// editLiveTranscripts applies edit to every open live transcript of sessionID.
func (m *model) editLiveTranscripts(sessionID string, edit func(tview) tea.Cmd) tea.Cmd {
	var cmds []tea.Cmd
	for i := range m.main {
		if t, ok := m.main[i].(transcriptComp); ok && t.live && t.sessionID == sessionID {
			cmds = append(cmds, m.editTranscript(i, edit))
		}
	}
	return tea.Batch(cmds...)
}

func workflowChanged(prev, next []session.RunningWorkflow, runID string) bool {
	find := func(ws []session.RunningWorkflow) (session.RunningWorkflow, bool) {
		for _, w := range ws {
			if w.RunID == runID {
				return w, true
			}
		}
		return session.RunningWorkflow{}, false
	}
	p, hadP := find(prev)
	n, hasN := find(next)
	if !hasN {
		return hadP
	}
	return !hadP || p.Done != n.Done || p.Total != n.Total
}

// workflowRow is a run's transcript row: icon, name, status.
func workflowRow(name, status string) string {
	return workflowLine("Workflow "+name, status)
}

// workflowHeader is a run's page header: name · status.
func workflowHeader(name, status string) string {
	if name == "" {
		name = "Workflow"
	}
	return workflowLine(name, status)
}

func workflowLine(label, status string) string {
	line := Icon.Workflow.Render() + " " + StylePrimaryBold.Render(strings.TrimSpace(label))
	if status != "" {
		line += " " + Icon.Dot.Glyph + " " + StyleSecondary.Render(status)
	}
	return line
}
