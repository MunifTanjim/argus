package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"

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
var _ adapter.Beaconed = (*piAdapter)(nil)

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

// Pi uses no shell hooks; status and approvals arrive over RPC (owned
// sessions) or a beacon connection (foreign sessions).
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

func (a *piAdapter) ServeBeacon(conn net.Conn) {
	if a.disc == nil {
		conn.Close()
		return
	}
	a.disc.serveBeacon(conn)
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

// beaconPath is the global gate-extension install location. Every pi process
// auto-loads it, so installing here turns the extension into a beacon for
// foreign (non-Argus-spawned) sessions. Managed sessions are unaffected:
// ARGUS_MANAGED switches the same file to RPC-dialog mode.
func beaconPath() (string, error) {
	home, err := piHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "extensions", "argus.ts"), nil
}

// Pi needs no hook config; the integration is the Tier 2 beacon, the gate
// extension staged where every pi process auto-loads it.
func (a piAdapter) Install(opts adapter.InstallOptions) error {
	_, err := a.Reconcile(opts)
	return err
}

// Reconcile writes opts.Socket, the node's mod socket, into the installed gate
// so beacons find the node that installed it.
func (piAdapter) Reconcile(opts adapter.InstallOptions) ([]string, error) {
	path, err := beaconPath()
	if err != nil {
		return nil, err
	}
	cur, _ := os.ReadFile(path)
	socket := opts.Socket
	if socket == "" {
		socket, _ = stampedSocket(string(cur))
	}
	want := stampSocket(socket)
	if string(cur) == want {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		return nil, err
	}
	return []string{path}, nil
}

func (piAdapter) Uninstall() error {
	path, err := beaconPath()
	if err != nil {
		return err
	}
	cur, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if _, ours := stampedSocket(string(cur)); !ours {
		return nil // user-crafted: leave it alone
	}
	return os.Remove(path)
}

// SettingsPath returns the pi home dir (not a file) so the install happens
// whenever pi has run once, even though the extensions directory itself is
// created lazily.
func (piAdapter) SettingsPath() (string, error) { return piHome() }

func (piAdapter) DefaultHookEvents() []string { return nil }
