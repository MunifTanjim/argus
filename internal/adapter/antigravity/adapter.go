package antigravity

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type agAdapter struct{}

func New() adapter.Adapter { return agAdapter{} }

var _ adapter.Adapter = agAdapter{}

func (agAdapter) Agent() string      { return Agent }
func (agAdapter) AgentName() string  { return "Antigravity" }
func (agAdapter) AgentColor() string { return "#83a598" } // blue

func (agAdapter) NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) adapter.Discoverer {
	return NewDiscoverer(reg, clients)
}

// SpawnCommand launches agy. --prompt-interactive keeps the session interactive
// after the prompt runs (a bare positional arg is not a prompt).
func (agAdapter) SpawnCommand(prompt string) (string, []string) {
	if prompt == "" {
		return "agy", nil
	}
	return "agy", []string{"--prompt-interactive", prompt}
}

func (agAdapter) ResumeCommand(agentSessionID string) (string, []string, bool) {
	return "agy", []string{"--conversation", agentSessionID}, true
}

func (agAdapter) ProcessHook(reg *registry.Registry, ev adapter.HookEvent) (session.Session, bool) {
	return ProcessHook(reg, ev)
}

func (agAdapter) EventName(ev adapter.HookEvent) string { return EventName(ev) }

func (agAdapter) RescanOnHook(ev adapter.HookEvent) bool { return RescanOnHook(ev) }

func (agAdapter) PermissionPayload(ev adapter.HookEvent) (string, json.RawMessage) {
	return PermissionPayload(ev)
}

func (agAdapter) ShouldBlock(ev adapter.HookEvent) bool { return ShouldBlock(ev) }

func (agAdapter) FormatDecision(toolName string, toolInput json.RawMessage, p api.RespondParams) string {
	return FormatDecision(toolName, toolInput, p)
}

func (agAdapter) HookOutput(ev adapter.HookEvent) string { return HookOutput(EventName(ev)) }

func (agAdapter) ReadTranscriptView(path string) (transcript.TranscriptView, error) {
	return ReadTranscriptView(path)
}

func (agAdapter) ReadSubagentView(rootPath, agentID string) (transcript.TranscriptView, bool, error) {
	return ReadSubagentView(rootPath, agentID)
}

func (agAdapter) FindToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	return FindToolDetail(path, agentID, toolID)
}

func (agAdapter) NewStreamingTranscript(path, rootPath string, isSubagent bool) adapter.StreamingTranscript {
	return NewStreamingTranscript(path, rootPath, isSubagent)
}

func (agAdapter) SubagentFilePath(rootPath, agentID string) (string, bool) {
	return SubagentFilePath(rootPath, agentID)
}

func (agAdapter) ListHistoryProjects() ([]session.HistoryProject, error) {
	return ListHistoryProjects()
}

func (agAdapter) ListHistorySessions(projectDir string, limit, offset int) (session.HistorySessionPage, error) {
	return ListHistorySessions(projectDir, limit, offset)
}

func (agAdapter) ReadHistoryTranscript(path string) (transcript.TranscriptView, error) {
	return ReadHistoryTranscript(path)
}

func (agAdapter) ReadHistorySubagentView(path, agentID string) (transcript.TranscriptView, bool, error) {
	return ReadHistorySubagentView(path, agentID)
}

func (agAdapter) FindHistoryToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	return FindHistoryToolDetail(path, agentID, toolID)
}

func (agAdapter) PrepareTextInput(ctx context.Context, pc adapter.PaneController, paneID string) error {
	return PrepareTextInput(ctx, pc, paneID)
}

func (agAdapter) Install(argusBin string) error { return Install(argusBin, DefaultHookEvents) }

func (agAdapter) ReconcileIfInstalled(argusBin string) ([]string, error) {
	return ReconcileIfInstalled(argusBin)
}

func (agAdapter) Uninstall() error { return Uninstall() }

func (agAdapter) SettingsPath() (string, error) { return SettingsPath() }

func (agAdapter) DefaultHookEvents() []string { return DefaultHookEvents }
