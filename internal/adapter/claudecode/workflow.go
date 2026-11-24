package claudecode

import (
	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type workflowTracker struct {
	scanner *parser.WorkflowScanner // nil until the subagent file is found
	root    string
	agentID string
}

func (a *ccAdapter) NewWorkflowTracker(rootPath, agentID string) adapter.WorkflowTracker {
	return &workflowTracker{root: rootPath, agentID: agentID}
}

// Refresh scans appended lines, then re-resolves every launch so running refs
// pick up summary-JSON status written since the last call. A subagent file
// not yet on disk reads as no launches.
func (t *workflowTracker) Refresh() ([]transcript.WorkflowRef, error) {
	if t.scanner == nil {
		path, ok := launchTranscript(t.root, t.agentID)
		if !ok {
			return nil, nil
		}
		t.scanner = parser.NewWorkflowScanner(path)
	}
	if err := t.scanner.Scan(); err != nil {
		return nil, err
	}
	launches := t.scanner.Launches()
	refs := make([]transcript.WorkflowRef, 0, len(launches))
	for _, l := range launches {
		refs = append(refs, resolveWorkflowRef(l, t.root, false))
	}
	return refs, nil
}

func (a *ccAdapter) ReadWorkflow(rootPath string, ref transcript.WorkflowRef) (transcript.WorkflowView, error) {
	empty := transcript.WorkflowView{WorkflowRef: ref, Phases: []transcript.WorkflowPhase{}}
	if ref.Remote || ref.RunID == "" {
		return empty, nil
	}
	run, ok, err := parser.ReadWorkflowRun(rootPath, ref.RunID, ref.Status != "running")
	if err != nil {
		return transcript.WorkflowView{}, err
	}
	if !ok {
		return empty, nil
	}
	view := transcript.WorkflowView{WorkflowRef: ref, Phases: make([]transcript.WorkflowPhase, 0, len(run.Phases))}
	for _, p := range run.Phases {
		phase := transcript.WorkflowPhase{Title: p.Title, Agents: make([]transcript.WorkflowAgent, 0, len(p.Agents))}
		for _, ag := range p.Agents {
			_, hasTrace := parser.WorkflowAgentFilePath(rootPath, ref.RunID, ag.ID)
			phase.Agents = append(phase.Agents, transcript.WorkflowAgent{
				ID: ag.ID, Label: ag.Label, State: ag.State, HasTrace: hasTrace,
			})
		}
		view.Phases = append(view.Phases, phase)
	}
	return view, nil
}

func (a *ccAdapter) FindWorkflow(rootPath, agentID, runID string, finished bool) (transcript.WorkflowRef, bool, error) {
	path, ok := launchTranscript(rootPath, agentID)
	if !ok {
		return transcript.WorkflowRef{}, false, nil
	}
	s := parser.NewWorkflowScanner(path)
	if err := s.Scan(); err != nil {
		return transcript.WorkflowRef{}, false, err
	}
	for _, l := range s.Launches() {
		if l.RunID == runID {
			return resolveWorkflowRef(l, rootPath, finished), true, nil
		}
	}
	return transcript.WorkflowRef{}, false, nil
}

// launchTranscript is the file whose Workflow launches are read: the session
// transcript at rootPath or, with agentID, that subagent's file if it exists.
func launchTranscript(rootPath, agentID string) (string, bool) {
	if agentID == "" {
		return rootPath, true
	}
	return parser.SubagentFilePath(rootPath, agentID)
}
