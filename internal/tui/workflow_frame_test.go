package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func workflowEntry(ref *transcript.WorkflowRef) transcript.Entry {
	return transcript.Entry{ID: "w", Kind: transcript.EntryWorkflow, ToolName: "Workflow", ToolID: "toolu_w", Workflow: ref}
}

func TestWorkflowEntries(t *testing.T) {
	v := transcript.WorkflowView{
		WorkflowRef: transcript.WorkflowRef{RunID: "wf1", Name: "review", Status: "running"},
		Phases: []transcript.WorkflowPhase{
			{Title: "Scan", Agents: []transcript.WorkflowAgent{
				{ID: "a1", Label: "scan api", State: "done", HasTrace: true},
				{ID: "a2", Label: "scan tui", State: "running"},
			}},
			{Title: "Fix", Agents: []transcript.WorkflowAgent{
				{ID: "a3", Label: "fix", State: "running", HasTrace: true},
			}},
		},
	}
	es := workflowEntries(v)
	if len(es) != 5 {
		t.Fatalf("want 2 headers + 3 agents, got %d: %+v", len(es), es)
	}
	wantAgents := map[int]transcript.WorkflowAgent{1: v.Phases[0].Agents[0], 2: v.Phases[0].Agents[1], 4: v.Phases[1].Agents[0]}
	for i, title := range map[int]string{0: "Scan", 3: "Fix"} {
		if es[i].Label != title {
			t.Errorf("entry %d: want phase header %q, got %+v", i, title, es[i])
		}
	}
	for i, a := range wantAgents {
		e := es[i]
		s, ok := soleSubagent(e)
		if !ok || e.ID != a.ID || s.ID != a.ID || s.Name != a.Label || s.Status != a.State || s.HasTrace != a.HasTrace {
			t.Errorf("entry %d: want agent %+v, got %+v", i, a, e)
		}
	}
}

func TestWorkflowEntriesSkipsSoleUntitledPhase(t *testing.T) {
	es := workflowEntries(transcript.WorkflowView{Phases: []transcript.WorkflowPhase{
		{Agents: []transcript.WorkflowAgent{{ID: "a1", Label: "one", State: "running"}}},
	}})
	if len(es) != 1 || es[0].ID != "a1" {
		t.Fatalf("a sole untitled phase needs no header: %+v", es)
	}
}

func TestWorkflowRowDrillable(t *testing.T) {
	if !drillable(workflowEntry(&transcript.WorkflowRef{RunID: "wf1", Status: "running"})) {
		t.Error("a local run with a run id should be drillable")
	}
	for name, ref := range map[string]*transcript.WorkflowRef{
		"no run id": {Status: "running"},
		"remote":    {RunID: "wf1", Status: "running", Remote: true},
		"no ref":    nil,
	} {
		if drillable(workflowEntry(ref)) {
			t.Errorf("%s: should not be drillable", name)
		}
	}
}

func TestWorkflowRowNoProgress(t *testing.T) {
	m := bareTv()
	e := workflowEntry(&transcript.WorkflowRef{RunID: "wf1", Name: "final-review", Status: "running"})
	row := ansi.Strip(m.entryBlock(e, false, false, false, false, 100))
	if strings.Count(row, "\n") != 0 {
		t.Errorf("row should be one line:\n%s", row)
	}
	if !strings.Contains(row, "Workflow") || !strings.Contains(row, "final-review") || !strings.Contains(row, "running") {
		t.Errorf("row should show the name and status: %q", row)
	}
	if strings.Contains(row, "/") {
		t.Errorf("row must not show a progress count: %q", row)
	}
}

func TestWorkflowFrameRefetchOnProgress(t *testing.T) {
	rc := &recordingClient{}
	m := streamModel(rc)
	s := m.sessions["n1:s1"]
	s.Workflows = []session.RunningWorkflow{{RunID: "wf1", Done: 1, Total: 3}, {RunID: "wf2", Done: 0, Total: 2}}
	m.sessions["n1:s1"] = s
	m = resumeInto(m, "n1:s1", workflowEntry(&transcript.WorkflowRef{RunID: "wf1", Name: "review", Status: "running"}))
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	if n := rc.count(api.MethodSessionWorkflow); n != 1 {
		t.Fatalf("enter on the row should fetch the run once, got %d", n)
	}
	if f := trOf(m).transcript.detailStack; len(f) != 1 || f[0].workflowRunID != "wf1" {
		t.Fatalf("enter should push the workflow frame: %+v", f)
	}

	event := func(m model, wfs []session.RunningWorkflow) model {
		s.Workflows = wfs
		m, cmd := upd(m, notificationMsg(sessionEventNote(registry.EventUpdated, s)))
		runCmd(cmd)
		return m
	}
	m = event(m, []session.RunningWorkflow{{RunID: "wf1", Done: 1, Total: 3}, {RunID: "wf2", Done: 1, Total: 2}})
	if n := rc.count(api.MethodSessionWorkflow); n != 1 {
		t.Fatalf("progress on an unrelated run must not refetch, got %d calls", n)
	}
	m = event(m, []session.RunningWorkflow{{RunID: "wf1", Done: 2, Total: 3}, {RunID: "wf2", Done: 1, Total: 2}})
	if n := rc.count(api.MethodSessionWorkflow); n != 2 {
		t.Fatalf("progress on the frame's run should refetch, got %d calls", n)
	}
	event(m, []session.RunningWorkflow{{RunID: "wf2", Done: 1, Total: 2}})
	if n := rc.count(api.MethodSessionWorkflow); n != 3 {
		t.Fatalf("the frame's run ending should refetch, got %d calls", n)
	}
}

func TestWorkflowFrameShowsFetchedView(t *testing.T) {
	m := detailTestModel(workflowEntry(&transcript.WorkflowRef{RunID: "wf1", Status: "running"}))
	m.c.m.width, m.c.m.height = 100, 30
	m.updateMsg(workflowMsg{owner: m.owner(), runID: "wf1", view: transcript.WorkflowView{
		WorkflowRef: transcript.WorkflowRef{RunID: "wf1", Name: "review", Status: "running"},
		Phases: []transcript.WorkflowPhase{{Title: "Scan", Agents: []transcript.WorkflowAgent{
			{ID: "a1", Label: "scan api", State: "done", HasTrace: true},
		}}},
	}})
	f := m.topFrame()
	if got := ansi.Strip(f.detailHeaderText(80)); !strings.Contains(got, "review · running") {
		t.Errorf("header should show name · status, got %q", got)
	}
	lines, _ := m.frameLines(f, 100)
	body := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"Scan", "scan api", "done"} {
		if !strings.Contains(body, want) {
			t.Errorf("frame should show %q:\n%s", want, body)
		}
	}
}

func TestWorkflowDrillHistoryUsesHistoryMethod(t *testing.T) {
	rc := &recordingClient{}
	mm := withView(testModel(), viewHistoryTranscript)
	mm.client = rc
	m := tvOf(&mm)
	m.history.openNodeID, m.history.openPath, m.history.openAgent = "n1", "/p/sess.jsonl", "claude"
	runCmd(m.drillWorkflow("wf1", "agentX"))
	if rc.count(api.MethodSessionsHistoryWorkflow) != 1 {
		t.Fatalf("history drill should call %s, got %v", api.MethodSessionsHistoryWorkflow, rc.calls)
	}
	p, ok := rc.params[0].(api.HistoryWorkflowParams)
	if !ok || p.RunID != "wf1" || p.AgentID != "agentX" || p.TranscriptPath != "/p/sess.jsonl" || p.Agent != "claude" {
		t.Errorf("history params: %+v", rc.params[0])
	}
}

// liveWorkflowFrame opens run wf1's page, launched from trace agentID, in a
// live session and delivers a view with status.
func liveWorkflowFrame(t *testing.T, rc *recordingClient, agentID, status string) (tview, tea.Cmd) {
	t.Helper()
	m := resumeInto(streamModel(rc), "n1:s1")
	v := tvOf(&m)
	v.drillWorkflow("wf1", agentID)
	cmd := v.updateMsg(workflowMsg{owner: v.owner(), runID: "wf1", agentID: agentID, view: transcript.WorkflowView{
		WorkflowRef: transcript.WorkflowRef{RunID: "wf1", Name: "review", Status: status},
	}})
	return v, cmd
}

func TestWorkflowTraceFramePollsWhileRunning(t *testing.T) {
	rc := &recordingClient{}
	v, cmd := liveWorkflowFrame(t, rc, "agentX", "running")
	token := v.topFrame().workflowTick
	if cmd == nil || token == "" {
		t.Fatalf("a running page opened from a trace should schedule a poll: cmd=%v token=%q", cmd != nil, token)
	}
	runCmd(v.updateMsg(workflowTickMsg{owner: v.owner(), runID: "wf1", agentID: "agentX", token: token}))
	if n := rc.count(api.MethodSessionWorkflow); n != 1 {
		t.Fatalf("the poll should refetch the run, got %d calls", n)
	}
	if v.updateMsg(workflowTickMsg{owner: v.owner(), runID: "wf1", agentID: "agentX", token: token}) != nil {
		t.Error("a spent poll token must not refetch again")
	}
}

func TestWorkflowTraceFrameStopsPollingWhenDone(t *testing.T) {
	v, cmd := liveWorkflowFrame(t, &recordingClient{}, "agentX", "completed")
	if cmd != nil || v.topFrame().workflowTick != "" {
		t.Error("a finished run should schedule no poll")
	}
}

func TestWorkflowClosedFrameIgnoresPoll(t *testing.T) {
	rc := &recordingClient{}
	v, _ := liveWorkflowFrame(t, rc, "agentX", "running")
	token := v.topFrame().workflowTick
	v.detailBack()
	if v.updateMsg(workflowTickMsg{owner: v.owner(), runID: "wf1", agentID: "agentX", token: token}) != nil {
		t.Error("a poll for a closed page must not refetch")
	}
}

func TestWorkflowMainFrameDoesNotPoll(t *testing.T) {
	v, cmd := liveWorkflowFrame(t, &recordingClient{}, "", "running")
	if cmd != nil || v.topFrame().workflowTick != "" {
		t.Error("a main-session page follows Session.Workflows and should not poll")
	}
}

func TestWorkflowFrameRefetchOnSessionRemoved(t *testing.T) {
	rc := &recordingClient{}
	m := streamModel(rc)
	s := m.sessions["n1:s1"]
	s.NodeID, s.Agent, s.TranscriptPath = "n1", "claude", "/p/s1.jsonl"
	s.Workflows = []session.RunningWorkflow{{RunID: "wf1", Done: 1, Total: 3}}
	m.sessions["n1:s1"] = s
	m = resumeInto(m, "n1:s1", workflowEntry(&transcript.WorkflowRef{RunID: "wf1", Status: "running"}))
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	owner := transcriptOwner{live: true, sessionID: "n1:s1"}
	m, _ = upd(m, workflowMsg{owner: owner, runID: "wf1", view: transcript.WorkflowView{
		WorkflowRef: transcript.WorkflowRef{RunID: "wf1", Status: "running"},
	}})

	m, cmd = upd(m, notificationMsg(sessionEventNote(registry.EventRemoved, session.Session{ID: "n1:s1"})))
	runCmd(cmd)
	if n := rc.count(api.MethodSessionsHistoryWorkflow); n != 1 {
		t.Fatalf("a removed session's running page should be reread from its transcript, got %v", rc.calls)
	}
	p := rc.params[len(rc.params)-1].(api.HistoryWorkflowParams)
	if p.RunID != "wf1" || p.TranscriptPath != "/p/s1.jsonl" || p.Agent != "claude" || p.NodeID != "n1" {
		t.Errorf("history params: %+v", p)
	}

	m, _ = upd(m, workflowMsg{owner: owner, runID: "wf1", view: transcript.WorkflowView{
		WorkflowRef: transcript.WorkflowRef{RunID: "wf1", Status: "stopped"},
	}})
	if got := trOf(m).transcript.detailStack[0].workflowStatus; got != "stopped" {
		t.Errorf("page status = %q, want stopped", got)
	}
}
