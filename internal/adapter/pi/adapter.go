package pi

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

type piAdapter struct {
	disc *discoverer
}

func New() adapter.Adapter { return &piAdapter{} }

var _ adapter.Adapter = (*piAdapter)(nil)
var _ adapter.Responder = (*piAdapter)(nil)
var _ adapter.Dismisser = (*piAdapter)(nil)
var _ adapter.Prompter = (*piAdapter)(nil)
var _ adapter.Spawner = (*piAdapter)(nil)

func (piAdapter) Agent() string      { return Agent }
func (piAdapter) AgentName() string  { return "Pi" }
func (piAdapter) AgentColor() string { return "#b294bb" } // muted purple
func (piAdapter) IsHeadless() bool   { return true }

func (a *piAdapter) NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) adapter.Discoverer {
	a.disc = newDiscoverer(reg, clients)
	return a.disc
}

// SpawnCommand names the binary only: Pi is headless, so spawn goes through
// the RPC supervisor (SpawnSession), not a launched pane. The name still backs
// the installed-agent PATH probe in handleAgentsList.
func (piAdapter) SpawnCommand(string) (string, []string) { return "pi", nil }

// ResumeCommand is the optional viewer pane: another client on the same
// session file. argus spawn -t <id> attaches without disturbing the owned
// RPC process.
func (piAdapter) ResumeCommand(agentSessionID string) (string, []string, bool) {
	if agentSessionID == "" {
		return "pi", nil, true
	}
	return "pi", []string{"--session", agentSessionID}, true
}

// Pi uses no shell hooks; status and approvals arrive over RPC (owned
// sessions) or the beacon socket (foreign sessions).
func (piAdapter) ProcessHook(_ *registry.Registry, _ adapter.HookEvent) (session.Session, bool) {
	return session.Session{}, false
}
func (piAdapter) EventName(adapter.HookEvent) string                               { return "" }
func (piAdapter) RescanOnHook(adapter.HookEvent) bool                              { return false }
func (piAdapter) PermissionPayload(adapter.HookEvent) (string, json.RawMessage)    { return "", nil }
func (piAdapter) ShouldBlock(adapter.HookEvent) bool                               { return false }
func (piAdapter) FormatDecision(string, json.RawMessage, api.RespondParams) string { return "" }
func (piAdapter) HookOutput(adapter.HookEvent) string                              { return "" }

func (piAdapter) CollectSessionFiles(transcriptPath string) ([]adapter.BundledFile, error) {
	return collectSessionFiles(transcriptPath)
}

func (piAdapter) ReadTranscriptView(path string) (transcript.TranscriptView, error) {
	return readTranscriptView(path, false)
}
func (piAdapter) ReadSubagentView(rootPath, agentID string) (transcript.TranscriptView, bool, error) {
	return readSubagentView(rootPath, agentID, false)
}
func (piAdapter) FindToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	return findToolDetail(path, agentID, toolID)
}
func (piAdapter) NewStreamingTranscript(path, rootPath string, isSubagent bool) adapter.StreamingTranscript {
	return newStreamingTranscript(path, rootPath, isSubagent)
}
func (piAdapter) SubagentFilePath(rootPath, agentID string) (string, bool) {
	return subagentFilePath(rootPath, agentID)
}

func (piAdapter) ListHistoryProjects() ([]session.HistoryProject, error) {
	return listHistoryProjects()
}
func (piAdapter) ListHistorySessions(projectDir string, limit, offset int) (session.HistorySessionPage, error) {
	return listHistorySessions(projectDir, limit, offset)
}
func (piAdapter) ReadHistoryTranscript(path string) (transcript.TranscriptView, error) {
	return readTranscriptView(path, true)
}
func (piAdapter) ReadHistorySubagentView(path, agentID string) (transcript.TranscriptView, bool, error) {
	return readSubagentView(path, agentID, true)
}
func (piAdapter) FindHistoryToolDetail(path, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	return findToolDetail(path, agentID, toolID)
}

func (piAdapter) PrepareTextInput(ctx context.Context, pc adapter.PaneController, paneID string) error {
	return prepareTextInput(ctx, pc, paneID)
}

// live returns the discoverer, or an error before the node starts it.
func (a *piAdapter) live() (*discoverer, error) {
	if a.disc == nil {
		return nil, fmt.Errorf("pi: no active discoverer")
	}
	return a.disc, nil
}

func (a *piAdapter) Respond(ctx context.Context, sess session.Session, p api.RespondParams) error {
	d, err := a.live()
	if err != nil {
		return err
	}
	return d.respond(ctx, sess, p)
}

func (a *piAdapter) Dismiss(ctx context.Context, sess session.Session) error {
	d, err := a.live()
	if err != nil {
		return err
	}
	return d.dismiss(ctx, sess.AgentSessionID)
}

func (a *piAdapter) SendPrompt(ctx context.Context, sess session.Session, text string) error {
	d, err := a.live()
	if err != nil {
		return err
	}
	return d.sendPrompt(ctx, sess.AgentSessionID, text)
}

func (a *piAdapter) SpawnSession(ctx context.Context, cwd, prompt string) (string, error) {
	d, err := a.live()
	if err != nil {
		return "", err
	}
	return d.spawnSession(ctx, cwd, prompt)
}

// Pi needs no argus-managed config: the gate extension ships with argus and is
// passed with -e at spawn, so there is nothing to install into pi's settings.
func (piAdapter) Install(string) error                          { return nil }
func (piAdapter) ReconcileIfInstalled(string) ([]string, error) { return nil, nil }
func (piAdapter) Uninstall() error                              { return nil }
func (piAdapter) SettingsPath() (string, error)                 { return "", nil }
func (piAdapter) DefaultHookEvents() []string                   { return nil }
