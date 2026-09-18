package opencode

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

type ocAdapter struct{}

func New() adapter.Adapter { return ocAdapter{} }

var _ adapter.Adapter = ocAdapter{}
var _ adapter.Responder = ocAdapter{}

func (ocAdapter) Agent() string      { return Agent }
func (ocAdapter) AgentName() string  { return "OpenCode" }
func (ocAdapter) AgentColor() string { return "#83a598" }

func (ocAdapter) NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) adapter.Discoverer {
	return newDiscoverer(reg, clients)
}

func (ocAdapter) SpawnCommand(prompt string) (string, []string) {
	if prompt == "" {
		return "opencode", nil
	}
	return "opencode", []string{"run", "--prompt", prompt}
}

func (ocAdapter) ResumeCommand(agentSessionID string) (string, []string, bool) {
	return "opencode", []string{"--session", agentSessionID}, true
}

// OpenCode uses no shell hooks; status and permissions arrive over SSE.
func (ocAdapter) ProcessHook(_ *registry.Registry, _ adapter.HookEvent) (session.Session, bool) {
	return session.Session{}, false
}
func (ocAdapter) EventName(adapter.HookEvent) string                               { return "" }
func (ocAdapter) RescanOnHook(adapter.HookEvent) bool                              { return false }
func (ocAdapter) PermissionPayload(adapter.HookEvent) (string, json.RawMessage)    { return "", nil }
func (ocAdapter) ShouldBlock(adapter.HookEvent) bool                               { return false }
func (ocAdapter) FormatDecision(string, json.RawMessage, api.RespondParams) string { return "" }
func (ocAdapter) HookOutput(adapter.HookEvent) string                              { return "" }

func (ocAdapter) CollectSessionFiles(transcriptPath string) ([]adapter.BundledFile, error) {
	return collectSessionFiles(transcriptPath)
}

func (ocAdapter) ReadTranscriptView(path string) (transcript.TranscriptView, error) {
	return readTranscriptView(path)
}
func (ocAdapter) ReadSubagentView(rootPath, agentID string) (transcript.TranscriptView, bool, error) {
	return readSubagentView(rootPath, agentID)
}
func (ocAdapter) FindToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	return findToolDetail(path, agentID, toolID)
}
func (ocAdapter) NewStreamingTranscript(path, rootPath string, isSubagent bool) adapter.StreamingTranscript {
	return newStreamingTranscript(path, rootPath, isSubagent)
}
func (ocAdapter) SubagentFilePath(rootPath, agentID string) (string, bool) {
	return subagentFilePath(rootPath, agentID)
}

func (ocAdapter) ListHistoryProjects() ([]session.HistoryProject, error) {
	return listHistoryProjects()
}
func (ocAdapter) ListHistorySessions(projectDir string, limit, offset int) (session.HistorySessionPage, error) {
	return listHistorySessions(projectDir, limit, offset)
}
func (ocAdapter) ReadHistoryTranscript(path string) (transcript.TranscriptView, error) {
	return readTranscriptView(path)
}
func (ocAdapter) ReadHistorySubagentView(path, agentID string) (transcript.TranscriptView, bool, error) {
	return readSubagentView(path, agentID)
}
func (ocAdapter) FindHistoryToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	return findToolDetail(path, agentID, toolID)
}

func (ocAdapter) PrepareTextInput(ctx context.Context, pc adapter.PaneController, paneID string) error {
	return prepareTextInput(ctx, pc, paneID)
}

func (ocAdapter) Respond(ctx context.Context, sess session.Session, p api.RespondParams) error {
	return respond(ctx, sess, p)
}

// OpenCode needs no argus-managed config.
func (ocAdapter) Install(string) error                          { return nil }
func (ocAdapter) ReconcileIfInstalled(string) ([]string, error) { return nil, nil }
func (ocAdapter) Uninstall() error                              { return nil }
func (ocAdapter) SettingsPath() (string, error)                 { return "", nil }
func (ocAdapter) DefaultHookEvents() []string                   { return nil }
