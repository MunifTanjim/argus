package codex

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type cxAdapter struct {
	disc *discoverer
}

func New() adapter.Adapter { return &cxAdapter{} }

var _ adapter.Adapter = (*cxAdapter)(nil)
var _ adapter.Responder = (*cxAdapter)(nil)
var _ adapter.InteractionOwner = (*cxAdapter)(nil)
var _ adapter.Dismisser = (*cxAdapter)(nil)
var _ adapter.Prompter = (*cxAdapter)(nil)
var _ adapter.Spawner = (*cxAdapter)(nil)

func (cxAdapter) Agent() string      { return Agent }
func (cxAdapter) AgentName() string  { return "Codex" }
func (cxAdapter) AgentColor() string { return "#b8bb26" } // green
func (cxAdapter) IsHeadless() bool   { return true }

func (a *cxAdapter) NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) adapter.Discoverer {
	a.disc = newDiscoverer(reg, clients)
	return a.disc
}

// SpawnCommand names the binary only: spawn goes through the daemon
// (SpawnSession). The name backs the installed-agent PATH probe.
func (cxAdapter) SpawnCommand(string) (string, []string) { return "codex", nil }

// ResumeCommand is the viewer pane: another client on the same daemon thread.
// It is unavailable until the thread can be resumed (see discoverer.resumable).
func (a *cxAdapter) ResumeCommand(agentSessionID string) (string, []string, bool) {
	if agentSessionID != "" && a.disc != nil && !a.disc.resumable(agentSessionID) {
		return "codex", nil, false
	}
	return "codex", []string{"resume", agentSessionID}, true
}

// Codex uses no shell hooks; status and approvals arrive from the daemon.
func (cxAdapter) ProcessHook(*registry.Registry, adapter.HookEvent) (session.Session, bool) {
	return session.Session{}, false
}
func (cxAdapter) EventName(adapter.HookEvent) string                               { return "" }
func (cxAdapter) RescanOnHook(adapter.HookEvent) bool                              { return false }
func (cxAdapter) PermissionPayload(adapter.HookEvent) (string, json.RawMessage)    { return "", nil }
func (cxAdapter) ShouldBlock(adapter.HookEvent) bool                               { return false }
func (cxAdapter) FormatDecision(string, json.RawMessage, api.RespondParams) string { return "" }
func (cxAdapter) HookOutput(adapter.HookEvent) string                              { return "" }

func (cxAdapter) CollectSessionFiles(transcriptPath string) ([]adapter.BundledFile, error) {
	return collectSessionFiles(transcriptPath)
}

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

func (cxAdapter) PrepareTextInput(ctx context.Context, pc adapter.PaneController, paneID string) error {
	return PrepareTextInput(ctx, pc, paneID)
}

// live returns the discoverer, or an error before the node starts it.
func (a *cxAdapter) live() (*discoverer, error) {
	if a.disc == nil {
		return nil, fmt.Errorf("codex: no active discoverer")
	}
	return a.disc, nil
}

func (a *cxAdapter) Respond(ctx context.Context, sess session.Session, p api.RespondParams) error {
	d, err := a.live()
	if err != nil {
		return err
	}
	return d.respond(ctx, sess, p)
}

// OwnsInteraction: respond refreshes the card itself to show the next queued request.
func (*cxAdapter) OwnsInteraction() bool { return true }

func (a *cxAdapter) SendPrompt(ctx context.Context, sess session.Session, text string) error {
	d, err := a.live()
	if err != nil {
		return err
	}
	return d.sendPrompt(ctx, sess.AgentSessionID, text)
}

func (a *cxAdapter) SpawnSession(ctx context.Context, cwd, prompt string) (string, error) {
	d, err := a.live()
	if err != nil {
		return "", err
	}
	return d.spawnSession(ctx, cwd, prompt)
}

func (a *cxAdapter) Dismiss(ctx context.Context, sess session.Session) error {
	d, err := a.live()
	if err != nil {
		return err
	}
	d.dismiss(ctx, sess.AgentSessionID)
	return nil
}

// Codex needs no argus-managed config. Node start strips hooks older argus
// versions installed; `argus hooks uninstall` still removes them too.
func (cxAdapter) Install(string) error                          { return nil }
func (cxAdapter) ReconcileIfInstalled(string) ([]string, error) { return nil, Uninstall() }
func (cxAdapter) Uninstall() error                              { return Uninstall() }
func (cxAdapter) SettingsPath() (string, error)                 { return "", nil }
func (cxAdapter) DefaultHookEvents() []string                   { return nil }
