package claudecode

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// ccAdapter is the Claude Code implementation of adapter.Adapter. It is a thin
// wrapper: every method delegates to the package-level functions that hold the
// actual logic, so this file is just the interface binding.
type ccAdapter struct{}

// New returns the Claude Code adapter.
func New() adapter.Adapter { return ccAdapter{} }

var _ adapter.Adapter = ccAdapter{}

func (ccAdapter) Tool() string { return Tool }

// --- Discovery ---

func (ccAdapter) NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) adapter.Discoverer {
	return NewDiscoverer(reg, clients)
}

// --- Hooks ---

func (ccAdapter) HookMethod() string { return HookMethod }

func (ccAdapter) ProcessHook(reg *registry.Registry, ev adapter.HookEvent) (session.Session, bool) {
	return ProcessHook(reg, ev)
}

func (ccAdapter) EventName(ev adapter.HookEvent) string { return EventName(ev) }

func (ccAdapter) PermissionPayload(ev adapter.HookEvent) (string, json.RawMessage) {
	return PermissionPayload(ev)
}

// --- Transcript (live) ---

func (ccAdapter) ReadTranscriptView(path string) (transcript.TranscriptView, error) {
	return ReadTranscriptView(path)
}

func (ccAdapter) ReadSubagentView(rootPath, agentID string) (transcript.TranscriptView, bool, error) {
	return ReadSubagentView(rootPath, agentID)
}

func (ccAdapter) FindToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	return FindToolDetail(path, agentID, toolID)
}

func (ccAdapter) NewStreamingTranscript(path, rootPath string, isSubagent bool) adapter.StreamingTranscript {
	return NewStreamingTranscript(path, rootPath, isSubagent)
}

func (ccAdapter) SubagentFilePath(rootPath, agentID string) (string, bool) {
	return parser.SubagentFilePath(rootPath, agentID)
}

// --- History ---

func (ccAdapter) ListHistoryProjects() ([]session.HistoryProject, error) {
	return ListHistoryProjects()
}

func (ccAdapter) ListHistorySessions(projectDir string, limit, offset int) (session.HistorySessionPage, error) {
	return ListHistorySessions(projectDir, limit, offset)
}

func (ccAdapter) ReadHistoryTranscript(path string) (transcript.TranscriptView, error) {
	return ReadHistoryTranscript(path)
}

func (ccAdapter) ReadHistorySubagentView(path, agentID string) (transcript.TranscriptView, bool, error) {
	return ReadHistorySubagentView(path, agentID)
}

func (ccAdapter) FindHistoryToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	return FindHistoryToolDetail(path, agentID, toolID)
}

// --- Input ---

func (ccAdapter) PrepareTextInput(ctx context.Context, pc adapter.PaneController, paneID string) error {
	return PrepareTextInput(ctx, pc, paneID)
}

// --- Installer ---

func (ccAdapter) Install(argusBin string) error { return Install(argusBin, DefaultHookEvents) }

func (ccAdapter) ReconcileIfInstalled(argusBin string) ([]string, error) {
	return ReconcileIfInstalled(argusBin)
}

func (ccAdapter) Uninstall() error { return Uninstall() }

func (ccAdapter) SettingsPath() (string, error) { return SettingsPath() }

func (ccAdapter) DefaultHookEvents() []string { return DefaultHookEvents }
