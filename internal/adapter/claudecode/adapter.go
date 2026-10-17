package claudecode

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/adapter/hookdecision"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/modbridge"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type ccAdapter struct {
	bridge *modbridge.Bridge
}

func New() adapter.Adapter { return &ccAdapter{} }

var _ adapter.Adapter = (*ccAdapter)(nil)
var _ adapter.TaskSource = (*ccAdapter)(nil)
var _ adapter.Prompter = (*ccAdapter)(nil)
var _ adapter.ModBridged = (*ccAdapter)(nil)
var _ adapter.Dismisser = (*ccAdapter)(nil)
var _ adapter.ModelLister = (*ccAdapter)(nil)

func (ccAdapter) Agent() string      { return Agent }
func (ccAdapter) AgentName() string  { return "Claude" }
func (ccAdapter) AgentColor() string { return "#fe8019" }
func (ccAdapter) IsHeadless() bool   { return false }

// NewDiscoverer also wires the mod bridge: the node calls it once, so the bridge
// and the input policy live on the node's adapter instance.
func (a *ccAdapter) NewDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) adapter.Discoverer {
	a.bridge = modbridge.New(modGrace, modAckTimeout, reg.RefreshInput)
	reg.SetInputPolicy(Agent, a.inputMode)
	return NewDiscoverer(reg, clients)
}

func (ccAdapter) SpawnCommand(opts adapter.SpawnOptions) (string, []string) {
	var args []string
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Prompt != "" {
		args = append(args, opts.Prompt)
	}
	return "claude", args
}

// ListModels returns Claude's model aliases; the CLI resolves each to the current
// model, so the list needs no upkeep per release.
func (ccAdapter) ListModels(context.Context) ([]api.ModelInfo, error) {
	return []api.ModelInfo{
		{ID: "opus", Name: "Opus"}, {ID: "opus[1m]", Name: "Opus (1M)"},
		{ID: "sonnet", Name: "Sonnet"}, {ID: "sonnet[1m]", Name: "Sonnet (1M)"},
		{ID: "haiku", Name: "Haiku"}, {ID: "haiku[1m]", Name: "Haiku (1M)"},
		{ID: "fable", Name: "Fable"}, {ID: "fable[1m]", Name: "Fable (1M)"},
	}, nil
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

func (ccAdapter) ReadTasks(sessionIDs []string, transcriptPath string) ([]api.Task, error) {
	return ReadTasks(sessionIDs, transcriptPath)
}

func (ccAdapter) TaskActivityCount(entries []transcript.Entry) (int, bool) {
	return TaskActivityCount(entries)
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

func (ccAdapter) Install(opts adapter.InstallOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()
	return installMod(ctx, opts)
}

func (ccAdapter) ReconcileIfInstalled(opts adapter.InstallOptions) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()
	return reconcileMod(ctx, opts)
}

func (ccAdapter) Uninstall() error {
	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()
	return uninstallMod(ctx)
}

func (ccAdapter) SettingsPath() (string, error) { return SettingsPath() }

// DefaultHookEvents is empty: Claude Code is integrated through the mod.
func (ccAdapter) DefaultHookEvents() []string { return nil }
