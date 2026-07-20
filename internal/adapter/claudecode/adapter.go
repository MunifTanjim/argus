package claudecode

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/adapter/hookdecision"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type ccAdapter struct{}

func New() adapter.Adapter { return ccAdapter{} }

var _ adapter.Adapter = ccAdapter{}
var _ adapter.TaskSource = ccAdapter{}

func (ccAdapter) Agent() string      { return Agent }
func (ccAdapter) AgentName() string  { return "Claude" }
func (ccAdapter) AgentColor() string { return "#fe8019" }

func (ccAdapter) NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) adapter.Discoverer {
	return NewDiscoverer(reg, clients)
}

func (ccAdapter) SpawnCommand(prompt string) (string, []string) {
	if prompt == "" {
		return "claude", nil
	}
	return "claude", []string{prompt}
}

func (ccAdapter) ResumeCommand(agentSessionID string) (string, []string, bool) {
	return "claude", []string{"--resume", agentSessionID}, true
}

func (ccAdapter) ProcessHook(reg *registry.Registry, ev adapter.HookEvent) (session.Session, bool) {
	return ProcessHook(reg, ev)
}

func (ccAdapter) EventName(ev adapter.HookEvent) string { return EventName(ev) }

func (ccAdapter) RescanOnHook(ev adapter.HookEvent) bool {
	e := EventName(ev)
	return e == "SessionStart" || e == "SessionEnd"
}

func (ccAdapter) ShouldBlock(ev adapter.HookEvent) bool { return ShouldBlock(ev) }

func (ccAdapter) PermissionPayload(ev adapter.HookEvent) (string, json.RawMessage) {
	return PermissionPayload(ev)
}

func (ccAdapter) FormatDecision(toolName string, toolInput json.RawMessage, p api.RespondParams) string {
	return hookdecision.FormatDecision(toolName, toolInput, p)
}

func (ccAdapter) HookOutput(adapter.HookEvent) string { return "" }

func (ccAdapter) CollectSessionFiles(transcriptPath string) ([]adapter.BundledFile, error) {
	return collectSessionFiles(transcriptPath, claudeHome())
}

func (ccAdapter) ReadTasks(transcriptPath string) ([]api.Task, error) {
	return ReadTasks(transcriptPath)
}

func (ccAdapter) TaskActivityCount(chunks []transcript.Chunk) (int, bool) {
	return TaskActivityCount(chunks)
}

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

func (ccAdapter) PrepareTextInput(ctx context.Context, pc adapter.PaneController, paneID string) error {
	return PrepareTextInput(ctx, pc, paneID)
}

func (ccAdapter) Install(argusBin string) error { return Install(argusBin, DefaultHookEvents) }

func (ccAdapter) ReconcileIfInstalled(argusBin string) ([]string, error) {
	return ReconcileIfInstalled(argusBin)
}

func (ccAdapter) Uninstall() error { return Uninstall() }

func (ccAdapter) SettingsPath() (string, error) { return SettingsPath() }

func (ccAdapter) DefaultHookEvents() []string { return DefaultHookEvents }
