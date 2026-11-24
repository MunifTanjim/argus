package claudecode

import (
	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type workflowTracker struct {
	scanner  *parser.WorkflowScanner
	rootPath string
}

func (a *ccAdapter) NewWorkflowTracker(transcriptPath string) adapter.WorkflowTracker {
	return &workflowTracker{scanner: parser.NewWorkflowScanner(transcriptPath), rootPath: transcriptPath}
}

// Refresh scans appended lines, then re-resolves every launch so running refs
// pick up summary-JSON status written since the last call.
func (t *workflowTracker) Refresh() ([]transcript.WorkflowRef, error) {
	if err := t.scanner.Scan(); err != nil {
		return nil, err
	}
	return workflowRefs(t.scanner.Launches(), t.rootPath, false), nil
}

func workflowRefs(launches []parser.WorkflowLaunch, rootPath string, finished bool) []transcript.WorkflowRef {
	refs := make([]transcript.WorkflowRef, 0, len(launches))
	for _, l := range launches {
		refs = append(refs, resolveWorkflowRef(l, rootPath, finished))
	}
	return refs
}

func (a *ccAdapter) ReadWorkflow(rootPath string, ref transcript.WorkflowRef) (transcript.WorkflowView, bool, error) {
	if ref.Remote || ref.RunID == "" {
		return transcript.WorkflowView{}, false, nil
	}
	run, ok, err := parser.ReadWorkflowRun(rootPath, ref.RunID, ref.Status != "running")
	if err != nil || !ok {
		return transcript.WorkflowView{}, false, err
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
	return view, true, nil
}

func (a *ccAdapter) FindWorkflow(rootPath, agentID, runID string, finished bool) (transcript.WorkflowRef, bool, error) {
	path := rootPath
	if agentID != "" {
		p, ok := parser.SubagentFilePath(rootPath, agentID)
		if !ok {
			return transcript.WorkflowRef{}, false, nil
		}
		path = p
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
