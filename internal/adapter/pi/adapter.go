package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
var _ adapter.ModelLister = (*piAdapter)(nil)
var _ adapter.Renamer = (*piAdapter)(nil)
var _ adapter.PromptQueuer = (*piAdapter)(nil)
var _ adapter.ShellInput = (*piAdapter)(nil)

func (piAdapter) Agent() string      { return Agent }
func (piAdapter) AgentName() string  { return "Pi" }
func (piAdapter) AgentColor() string { return "#b294bb" } // muted purple
func (piAdapter) IsHeadless() bool   { return true }

func (a *piAdapter) NewDiscoverer(reg *registry.Registry, _ map[session.TmuxServer]*tmux.Client) adapter.Discoverer {
	a.disc = newDiscoverer(reg)
	return a.disc
}

// SpawnCommand names the binary only: Pi is headless, so spawn goes through
// the RPC supervisor (SpawnSession), not a launched pane. The name still backs
// the installed-agent PATH probe in handleAgentsList.
func (piAdapter) SpawnCommand(adapter.SpawnOptions) (string, []string) { return "pi", nil }

// ResumeCommand reopens a past session in a pane. Pi cannot attach to a
// session another process runs: `pi --session` would start a second agent
// writing the same file, so a live session gets no pane.
func (a *piAdapter) ResumeCommand(agentSessionID string) (string, []string, bool) {
	if agentSessionID == "" {
		return "pi", nil, true
	}
	if a.disc != nil && a.disc.running(agentSessionID) {
		return "pi", nil, false
	}
	return "pi", []string{"--session", agentSessionID}, true
}

// Pi uses no shell hooks; status and dialogs arrive over RPC.
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
func (a *piAdapter) NewStreamingTranscript(path, rootPath string, isSubagent bool) adapter.StreamingTranscript {
	st := newStreamingTranscript(path)
	if a.disc != nil {
		disc := a.disc
		st.queue = func() []string { return disc.queueFor(path) }
	}
	return st
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

// QueuesPrompts: pi steers a prompt sent mid-run into the running turn.
func (piAdapter) QueuesPrompts(session.Session) bool { return true }

func (a *piAdapter) SendPrompt(ctx context.Context, sess session.Session, text string) error {
	d, err := a.live()
	if err != nil {
		return err
	}
	if sess.InteractionPending() {
		return fmt.Errorf("%s: %w", sess.ID, api.ErrSessionBusy)
	}
	return d.sendPrompt(ctx, sess.AgentSessionID, text)
}

// IsShellInput also takes /new, which starts a new session.
func (piAdapter) IsShellInput(text string) bool {
	return strings.HasPrefix(text, "!") || isNewCommand(text)
}

// SendShellInput runs "!cmd" through pi's bash command, as pi's own editor
// does; "!!cmd" keeps the output out of the model's context. /new starts a new
// session.
func (a *piAdapter) SendShellInput(ctx context.Context, sess session.Session, text string) (bool, error) {
	d, err := a.live()
	if err != nil {
		return false, err
	}
	if sess.InteractionPending() {
		return false, fmt.Errorf("%s: %w", sess.ID, api.ErrSessionBusy)
	}
	return d.runShell(ctx, sess.AgentSessionID, text)
}

func (a *piAdapter) SpawnSession(ctx context.Context, cwd string, opts adapter.SpawnOptions) (string, error) {
	d, err := a.live()
	if err != nil {
		return "", err
	}
	return d.spawnSession(ctx, cwd, opts)
}

func (a *piAdapter) RenameSession(ctx context.Context, sess session.Session, name string) (bool, error) {
	d, err := a.live()
	if err != nil {
		return false, err
	}
	return true, d.rename(ctx, sess.AgentSessionID, name)
}

func (piAdapter) ListModels(ctx context.Context) ([]api.ModelInfo, error) {
	out, err := runListModels(ctx)
	if err != nil {
		return nil, err
	}
	return parseModelList(out), nil
}

// Pi needs no argus-managed config: there is nothing to install into pi's
// settings.
func (piAdapter) Install(adapter.InstallOptions) error { return nil }
func (piAdapter) Reconcile(adapter.InstallOptions) ([]string, error) {
	return nil, nil
}
func (piAdapter) Uninstall() error              { return nil }
func (piAdapter) SettingsPath() (string, error) { return "", nil }
func (piAdapter) DefaultHookEvents() []string   { return nil }
