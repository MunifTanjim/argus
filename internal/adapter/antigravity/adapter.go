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
func (agAdapter) IsHeadless() bool   { return false }

func (agAdapter) NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) adapter.Discoverer {
	return NewDiscoverer(reg, clients)
}

// SpawnCommand launches agy. --prompt-interactive keeps the session interactive
// after the prompt runs (a bare positional arg is not a prompt).
func (agAdapter) SpawnCommand(opts adapter.SpawnOptions) (string, []string) {
	if opts.Prompt == "" {
		return "agy", nil
	}
	return "agy", []string{"--prompt-interactive", opts.Prompt}
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

func (agAdapter) CollectSessionFiles(transcriptPath string) ([]adapter.BundledFile, error) {
	return collectSessionFiles(transcriptPath)
}

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

func (agAdapter) Install(opts adapter.InstallOptions) error {
	return Install(opts.ArgusBin, DefaultHookEvents)
}

func (agAdapter) Reconcile(opts adapter.InstallOptions) ([]string, error) {
	return Reconcile(opts.ArgusBin)
}

func (agAdapter) Uninstall() error { return Uninstall() }

func (agAdapter) SettingsPath() (string, error) { return SettingsPath() }

func (agAdapter) DefaultHookEvents() []string { return DefaultHookEvents }

var _ adapter.PromptCompleter = agAdapter{}

var builtinCommands = []api.PromptCompletion{
	{Name: "exit", Description: "Exit the CLI"},
	{Name: "compact", Description: "Compact the conversation now"},
	{Name: "clear", Description: "Clear conversation and start a new one"},
	{Name: "new", Description: "Clear conversation and start a new one"},
}

func (agAdapter) CompletePrompt(_ context.Context, _ session.Session, trigger string) ([]api.PromptCompletion, error) {
	if trigger != "/" {
		return nil, nil
	}
	return builtinCommands, nil
}
