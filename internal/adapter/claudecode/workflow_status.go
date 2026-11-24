package claudecode

import (
	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// resolveWorkflowRef maps a launch to its ref. Status comes from, in order:
// the launch's end record; the run's summary JSON status when terminal (local
// runs only); "stopped" when the session is finished; else "running". rootPath
// is the session root whose directory holds the run state.
func resolveWorkflowRef(l parser.WorkflowLaunch, rootPath string, finished bool) transcript.WorkflowRef {
	ref := transcript.WorkflowRef{
		RunID:      l.RunID,
		Name:       l.Name,
		Remote:     l.Remote,
		LaunchedAt: formatTS(l.LaunchedAt),
		Status:     l.End,
	}
	if ref.Status == "" && l.RunID != "" && !l.Remote {
		if final, err := parser.ReadWorkflowFinal(rootPath, l.RunID); err == nil && final != "" && final != "running" {
			ref.Status = parser.MapWorkflowEnd(final)
		}
	}
	if ref.Status == "" {
		if finished {
			ref.Status = "stopped"
		} else {
			ref.Status = "running"
		}
	}
	return ref
}
