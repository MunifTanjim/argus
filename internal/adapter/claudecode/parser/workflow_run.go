package parser

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

type WorkflowRunAgent struct {
	ID, Label string
	State     string // "running" | "done" | "failed" | "queued", or the raw summary state when unrecognised
}

type WorkflowRunPhase struct {
	Title  string
	Agents []WorkflowRunAgent
}

type WorkflowRun struct {
	Phases []WorkflowRunPhase
}

// WorkflowAgentFilePath returns the transcript of a workflow agent in a known
// run (subagents/workflows/<runID>/agent-<agentID>.jsonl), if it exists.
func WorkflowAgentFilePath(rootPath, runID, agentID string) (string, bool) {
	return regularFile(filepath.Join(subagentsDir(rootPath), "workflows", runID, "agent-"+agentID+".jsonl"))
}

// agentFilePath finds agent-<id><ext> under subagents/, then under
// subagents/workflows/*/.
func agentFilePath(rootPath, agentID, ext string) (string, bool) {
	if p, ok := regularFile(filepath.Join(subagentsDir(rootPath), "agent-"+agentID+ext)); ok {
		return p, true
	}
	dir := filepath.Join(subagentsDir(rootPath), "workflows")
	runs, _ := os.ReadDir(dir)
	for _, run := range runs {
		if !run.IsDir() {
			continue
		}
		if p, ok := regularFile(filepath.Join(dir, run.Name(), "agent-"+agentID+ext)); ok {
			return p, true
		}
	}
	return "", false
}

func regularFile(p string) (string, bool) {
	if info, err := os.Stat(p); err != nil || info.IsDir() {
		return "", false
	}
	return p, true
}

// ReadWorkflowRun reads the run under the session root. ended: the run has an
// end record; started-without-result agents then read "failed". ok=false when
// neither journal nor summary exists. The summary, once written, takes
// precedence over the journal.
func ReadWorkflowRun(rootPath, runID string, ended bool) (WorkflowRun, bool, error) {
	if b, err := os.ReadFile(workflowSummaryPath(rootPath, runID)); err == nil {
		if run, ok := parseWorkflowSummary(b); ok {
			return run, true, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return WorkflowRun{}, false, err
	}

	f, err := os.Open(workflowJournalPath(rootPath, runID))
	if errors.Is(err, fs.ErrNotExist) {
		return WorkflowRun{}, false, nil
	}
	if err != nil {
		return WorkflowRun{}, false, err
	}
	defer f.Close()
	run, err := readWorkflowJournal(f, ended)
	return run, err == nil, err
}

// terminalFinals memoizes terminal summary statuses by summary path: once a
// run has ended its status never changes, so live polls stop re-reading it.
var terminalFinals sync.Map // string -> string

// ReadWorkflowFinal returns the status from a run's summary JSON, or "" when
// the summary is absent or unreadable as JSON. It does not read the journal.
func ReadWorkflowFinal(rootPath, runID string) (string, error) {
	path := workflowSummaryPath(rootPath, runID)
	if v, ok := terminalFinals.Load(path); ok {
		return v.(string), nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var s struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(b, &s) != nil {
		return "", nil
	}
	if s.Status != "" && s.Status != "running" {
		terminalFinals.Store(path, s.Status)
	}
	return s.Status, nil
}

func workflowJournalPath(rootPath, runID string) string {
	return filepath.Join(subagentsDir(rootPath), "workflows", runID, "journal.jsonl")
}

func workflowSummaryPath(rootPath, runID string) string {
	return filepath.Join(filepath.Dir(subagentsDir(rootPath)), "workflows", runID+".json")
}

func parseWorkflowSummary(b []byte) (WorkflowRun, bool) {
	var s struct {
		Phases []struct {
			Title string `json:"title"`
		} `json:"phases"`
		Progress []struct {
			Type       string `json:"type"`
			Label      string `json:"label"`
			PhaseTitle string `json:"phaseTitle"`
			AgentID    string `json:"agentId"`
			State      string `json:"state"`
		} `json:"workflowProgress"`
	}
	if json.Unmarshal(b, &s) != nil {
		return WorkflowRun{}, false
	}
	pb := newPhaseBuilder()
	for _, p := range s.Phases {
		pb.phase(p.Title)
	}
	for _, p := range s.Progress {
		if p.Type != "workflow_agent" {
			continue
		}
		pb.add(p.PhaseTitle, WorkflowRunAgent{ID: p.AgentID, Label: p.Label, State: mapSummaryState(p.State)})
	}
	return WorkflowRun{Phases: pb.phases}, true
}

func mapSummaryState(s string) string {
	switch s {
	case "queued", "pending":
		return "queued"
	case "failed", "error":
		return "failed"
	}
	return s
}

func readWorkflowJournal(f *os.File, ended bool) (WorkflowRun, error) {
	type entry struct {
		phase string
		idx   int
	}
	pb := newPhaseBuilder()
	byID := map[string]entry{}
	done := map[string]bool{}
	lr := newLineReader(f)
	for {
		line, ok := lr.next()
		if !ok {
			break
		}
		var ev struct {
			Type    string `json:"type"`
			AgentID string `json:"agentId"`
			Label   string `json:"label"`
			Phase   string `json:"phase"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil || ev.AgentID == "" {
			continue
		}
		switch ev.Type {
		case "started":
			if _, dup := byID[ev.AgentID]; dup {
				continue
			}
			pb.add(ev.Phase, WorkflowRunAgent{ID: ev.AgentID, Label: ev.Label, State: "running"})
			byID[ev.AgentID] = entry{ev.Phase, len(pb.phases[pb.index[ev.Phase]].Agents) - 1}
		case "result":
			done[ev.AgentID] = true
		}
	}
	if err := lr.Err(); err != nil {
		return WorkflowRun{}, err
	}
	for id, e := range byID {
		a := &pb.phases[pb.index[e.phase]].Agents[e.idx]
		switch {
		case done[id]:
			a.State = "done"
		case ended:
			a.State = "failed"
		}
	}
	return WorkflowRun{Phases: pb.phases}, nil
}

// phaseBuilder accumulates agents into phases in first-seen order.
type phaseBuilder struct {
	phases []WorkflowRunPhase
	index  map[string]int
}

func newPhaseBuilder() *phaseBuilder { return &phaseBuilder{index: map[string]int{}} }

func (b *phaseBuilder) phase(title string) int {
	i, ok := b.index[title]
	if !ok {
		i = len(b.phases)
		b.index[title] = i
		b.phases = append(b.phases, WorkflowRunPhase{Title: title})
	}
	return i
}

func (b *phaseBuilder) add(title string, a WorkflowRunAgent) {
	i := b.phase(title)
	b.phases[i].Agents = append(b.phases[i].Agents, a)
}
