package codex

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// cxAdapter is the Codex implementation of adapter.Adapter. Like the Claude Code
// adapter it is a thin wrapper: methods delegate to the package-level functions
// that hold the logic.
type cxAdapter struct{}

// New returns the Codex adapter.
func New() adapter.Adapter { return cxAdapter{} }

var _ adapter.Adapter = cxAdapter{}

func (cxAdapter) Tool() string { return Tool }

// --- Discovery ---

func (cxAdapter) NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) adapter.Discoverer {
	return NewDiscoverer(reg, clients)
}

// --- Hooks ---

func (cxAdapter) HookMethod() string { return HookMethod }

func (cxAdapter) ProcessHook(reg *registry.Registry, ev adapter.HookEvent) (session.Session, bool) {
	return ProcessHook(reg, ev)
}

func (cxAdapter) EventName(ev adapter.HookEvent) string { return EventName(ev) }

func (cxAdapter) PermissionPayload(ev adapter.HookEvent) (string, json.RawMessage) {
	return PermissionPayload(ev)
}

// --- Transcript (stubbed; see transcript.go) ---

func (cxAdapter) ReadTranscriptView(path string) (transcript.TranscriptView, error) {
	return ReadTranscriptView(path)
}

func (cxAdapter) ReadSubagentView(rootPath, agentID string) (transcript.TranscriptView, bool, error) {
	return ReadSubagentView(rootPath, agentID)
}

func (cxAdapter) FindToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	return FindToolDetail(path, agentID, toolID)
}

func (cxAdapter) NewStreamingTranscript(path, rootPath string, isSubagent bool) adapter.StreamingTranscript {
	return NewStreamingTranscript(path, rootPath, isSubagent)
}

func (cxAdapter) SubagentFilePath(rootPath, agentID string) (string, bool) {
	return SubagentFilePath(rootPath, agentID)
}

// --- History (stubbed) ---

func (cxAdapter) ListHistoryProjects() ([]session.HistoryProject, error) {
	return ListHistoryProjects()
}

func (cxAdapter) ListHistorySessions(projectDir string, limit, offset int) (session.HistorySessionPage, error) {
	return ListHistorySessions(projectDir, limit, offset)
}

func (cxAdapter) ReadHistoryTranscript(path string) (transcript.TranscriptView, error) {
	return ReadHistoryTranscript(path)
}

func (cxAdapter) ReadHistorySubagentView(path, agentID string) (transcript.TranscriptView, bool, error) {
	return ReadHistorySubagentView(path, agentID)
}

func (cxAdapter) FindHistoryToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	return FindHistoryToolDetail(path, agentID, toolID)
}

// --- Input ---

func (cxAdapter) PrepareTextInput(ctx context.Context, pc adapter.PaneController, paneID string) error {
	return PrepareTextInput(ctx, pc, paneID)
}

// --- Installer ---

func (cxAdapter) Install(argusBin string) error { return Install(argusBin, DefaultHookEvents) }

func (cxAdapter) ReconcileIfInstalled(argusBin string) ([]string, error) {
	return ReconcileIfInstalled(argusBin)
}

func (cxAdapter) Uninstall() error { return Uninstall() }

func (cxAdapter) SettingsPath() (string, error) { return SettingsPath() }

func (cxAdapter) DefaultHookEvents() []string { return DefaultHookEvents }
