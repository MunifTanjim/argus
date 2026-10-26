package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/spawn"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
	"golang.org/x/sys/unix"
)

// Session-facing RPC handlers: reads from the registry and tmux control of panes.

// submitDelay is the pause between injecting text and the submitting Enter. A CR
// coalesced into the same stdin read as the text is swallowed (treated as paste),
// so the message never submits; holding Enter back as a separate read fixes it.
// See TestSessionInputDelaysEnterAfterText.
var submitDelay = 75 * time.Millisecond

func (d *Node) handleSessionsList(context.Context, json.RawMessage) (any, error) {
	return d.snapshotWithCaps(), nil
}

func (d *Node) snapshotWithCaps() []session.Session {
	snap := d.reg.Snapshot()
	for i := range snap {
		snap[i] = d.withCaps(snap[i])
	}
	return snap
}

// withCaps stamps node-computed capability fields onto a session before it is
// emitted to a client, so they reflect this node's tmux and installed agents.
func (d *Node) withCaps(s session.Session) session.Session {
	s.CanOpenTerminal = d.canOpenTerminal(s)
	q, ok := d.adapterFor(s.Agent).(adapter.PromptQueuer)
	s.CanPrompt = s.TakesPrompt(ok && q.QueuesPrompts(s))
	s.Parent = d.parentRef(s)
	return s
}

func (d *Node) parentRef(s session.Session) *session.ParentRef {
	key := partyKey(s)
	if d.mesh == nil || key == "" {
		return nil
	}
	link, ok, err := d.mesh.Link(context.Background(), key)
	if err != nil {
		d.log.Debug("mesh: link lookup failed", "session", s.ID, "err", err)
		return nil
	}
	if !ok {
		return nil
	}
	ref := &session.ParentRef{Node: link.Parent.Node, Key: link.Parent.Key}
	if p, live := d.sessionForKey(link.Parent.Key); live {
		ref.SessionID = p.ID
	} else {
		ref.Gone = true
	}
	return ref
}

// canOpenTerminal reports whether argus can show a terminal for this live session
// on this node.
func (d *Node) canOpenTerminal(s session.Session) bool {
	return s.Controllable() || d.canSpawnViewer(s)
}

// canSpawnViewer reports whether argus can spawn a terminal viewer for a session
// whose agent is headless (adapter.IsHeadless): a pane is a disposable view of a
// session that lives on its own, not the session's own process.
func (d *Node) canSpawnViewer(s session.Session) bool {
	if !d.adapterFor(s.Agent).IsHeadless() || !d.caps.SpawnSession || s.Cwd == "" || s.AgentSessionID == "" {
		return false
	}
	name, _, ok := d.adapterFor(s.Agent).ResumeCommand(s.AgentSessionID)
	if !ok {
		return false
	}
	return d.binaryAvailable(name)
}

// recoverViewerPane recovers from a mirror setup failure caused by an adopted
// viewer pane that died out-of-band, leaving a stale controllable record. It only
// applies to sessions whose pane is a viewer (canSpawnViewer); for any other
// session it reports ok=false so the original error stands.
func (d *Node) recoverViewerPane(ctx context.Context, s session.Session) (session.Session, *tmux.Client, bool) {
	if !d.canSpawnViewer(s) {
		return s, nil, false
	}
	d.reg.ClearPane(s.AgentSessionID)
	if _, err := d.spawnAndAdopt(ctx, s.Agent, s.AgentSessionID, s.Cwd, s.ID); err != nil {
		return s, nil, false
	}
	s2, c2, err := d.resolve(s.ID)
	if err != nil {
		return s, nil, false
	}
	return s2, c2, true
}

// binaryAvailable memoizes a PATH lookup for an agent binary. It backs the
// display signal only; terminal.open does the authoritative check when it spawns.
func (d *Node) binaryAvailable(name string) bool {
	if name == "" {
		return false
	}
	d.binMu.Lock()
	defer d.binMu.Unlock()
	if d.binCache == nil {
		d.binCache = map[string]bool{}
	}
	if v, ok := d.binCache[name]; ok {
		return v
	}
	_, err := exec.LookPath(name)
	d.binCache[name] = err == nil
	return d.binCache[name]
}

// handleNodeIdentify announces this node's identity over the gateway uplink.
func (d *Node) handleNodeIdentify(context.Context, json.RawMessage) (any, error) {
	res := api.IdentifyResult{ID: d.id, Label: d.label, Version: d.version, Capabilities: d.caps, IdentityPubKey: d.identityPubB64, SignerPubKey: d.signerPubB64, LockDisabled: d.localDisabled()}
	if st := d.trust.Load(); st != nil {
		res.Tip = st.Tip()
	}
	return res, nil
}

// handleServerInfo lets a client talking directly to a plain node read the
// version and spawn target (just this node) so it can gate the spawn UI on tmux
// availability up front. The ID is empty: a plain node has no routing namespace,
// so the client addresses it implicitly. The gateway overrides this with its own
// cross-node aggregation.
func (d *Node) handleServerInfo(context.Context, json.RawMessage) (any, error) {
	return api.ServerInfo{
		Version:   d.version,
		StartedAt: api.StartedAtRFC3339(),
		Nodes:     []api.NodeInfo{{Label: d.label, Version: d.version, Capabilities: d.caps}},
	}, nil
}

// handleSessionsRefresh rescans on demand, then returns the current snapshot.
func (d *Node) handleSessionsRefresh(ctx context.Context, _ json.RawMessage) (any, error) {
	d.scan(ctx)
	return d.snapshotWithCaps(), nil
}

func (d *Node) handleTranscriptView(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TranscriptParams](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	return d.transcriptView(s)
}

// handleSessionToolDetail returns one tool entry's full input/result by tool_use
// id; transcript entries ship without these heavy bodies.
func (d *Node) handleSessionToolDetail(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.ToolDetailParams](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	if s.TranscriptPath == "" {
		return nil, fmt.Errorf("session has no transcript: %s", p.SessionID)
	}
	td, found, err := d.adapterFor(s.Agent).FindToolDetail(s.TranscriptPath, p.AgentID, p.ToolID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown tool: " + p.ToolID}
	}
	return toAPIToolDetail(td), nil
}

func toAPIToolDetail(td transcript.ToolDetail) api.ToolDetail {
	return api.ToolDetail{ToolInput: td.ToolInput, Result: td.Result, ResultIsError: td.ResultIsError}
}

// handleSessionCapture returns a live capture of a session's tmux pane.
func (d *Node) handleSessionCapture(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SessionRef](params)
	if err != nil {
		return nil, err
	}
	s, c, err := d.resolve(p.SessionID)
	if err != nil {
		return nil, err
	}
	// NoJoin: keep physical rows so the app renders the pane exactly (no wrap).
	screen, err := c.CapturePane(ctx, s.Tmux.PaneID, tmux.CaptureOpts{Escapes: true, NoJoin: true})
	if err != nil {
		return nil, err
	}
	return api.CaptureResult{Screen: screen}, nil
}

// handleSessionInput delivers input to a session over its input channel: the
// agent's own prompt API for an InputAPI session (opencode, Claude over its
// mod), or tmux keystrokes for an InputPane session.
func (d *Node) handleSessionInput(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.InputParams](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	return d.sendInput(ctx, s, p)
}

func (d *Node) sendInput(ctx context.Context, s session.Session, p api.InputParams) (any, error) {
	switch s.Input {
	case session.InputAPI:
		return d.sendPromptInput(ctx, s, p)
	case session.InputPane:
		return d.sendPaneInput(ctx, s, p)
	default:
		return nil, fmt.Errorf("%s: %w", s.ID, api.ErrNoTerminalControl)
	}
}

func (d *Node) deliverPrompt(ctx context.Context, s session.Session, text string) error {
	_, err := d.sendInput(ctx, s, api.InputParams{SessionID: s.ID, Text: text, Submit: true, Prepare: true})
	return err
}

// sendPaneInput sends text (and optionally Enter) to a session's pane, after
// optionally normalizing the pane for input (exit copy mode; ensure vim insert).
func (d *Node) sendPaneInput(ctx context.Context, s session.Session, p api.InputParams) (any, error) {
	_, c, err := d.resolve(s.ID)
	if err != nil {
		return nil, err
	}
	sentText := false
	if p.Text != "" {
		if p.Prepare {
			if err := d.adapterFor(s.Agent).PrepareTextInput(ctx, c, s.Tmux.PaneID); err != nil {
				return nil, err
			}
		}
		// Multi-line must be pasted: as literal keystrokes a raw LF is dropped and a
		// raw CR submits early, so newlines are lost; bracketed paste preserves them.
		// Single-line stays literal so interactive triggers (slash menus, @-mentions)
		// still fire as typed.
		if strings.Contains(p.Text, "\n") {
			if err := c.PasteText(ctx, s.Tmux.PaneID, p.Text); err != nil {
				return nil, err
			}
		} else if err := c.SendText(ctx, s.Tmux.PaneID, p.Text); err != nil {
			return nil, err
		}
		sentText = true
	}
	if p.Submit {
		// Hold Enter back after injecting text so the TUI reads it separately. See submitDelay.
		if sentText {
			if err := sleepCtx(ctx, submitDelay); err != nil {
				return nil, err
			}
		}
		if err := c.SendKeys(ctx, s.Tmux.PaneID, "Enter"); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// sendPromptInput delivers a prompt to an InputAPI session over its agent's API.
// A working session whose agent fills its own input box gets the text filled
// and submitted with Enter in its pane, so the agent queues it as typed there.
func (d *Node) sendPromptInput(ctx context.Context, s session.Session, p api.InputParams) (any, error) {
	a := d.adapterFor(s.Agent)
	if p.Text == "" {
		return nil, fmt.Errorf("%s: %w", s.ID, api.ErrNoTerminalControl)
	}
	if si, ok := a.(adapter.ShellInput); ok && p.Submit && si.IsShellInput(p.Text) {
		if !s.Controllable() {
			return nil, fmt.Errorf("%s: %w", s.ID, api.ErrShellNeedsPane)
		}
		return nil, si.SendShellInput(ctx, s, p.Text)
	}
	if f, ok := a.(adapter.PaneFiller); ok && s.Status == session.StatusWorking && s.Controllable() {
		return nil, d.fillAndSubmit(ctx, s, f, p.Text)
	}
	pr, ok := a.(adapter.Prompter)
	if !ok {
		return nil, fmt.Errorf("%s: %w", s.ID, api.ErrNoTerminalControl)
	}
	if err := pr.SendPrompt(ctx, s, p.Text); err != nil {
		return nil, err
	}
	return nil, nil
}

// fillAndSubmit leaves copy mode before pressing Enter so the key reaches the
// agent. A permission, question or plan that opened during the fill gets no
// Enter: the key would answer it.
func (d *Node) fillAndSubmit(ctx context.Context, s session.Session, f adapter.PaneFiller, text string) error {
	_, c, err := d.resolve(s.ID)
	if err != nil {
		return err
	}
	filled, err := f.FillPrompt(ctx, s, text)
	if err != nil || !filled {
		return err
	}
	if cur, ok := d.reg.Get(s.ID); !ok || cur.InteractionPending() {
		return fmt.Errorf("%s: %w", s.ID, api.ErrSessionBusy)
	}
	inMode, err := c.PaneInMode(ctx, s.Tmux.PaneID)
	if err != nil {
		return err
	}
	if inMode {
		if err := c.CancelMode(ctx, s.Tmux.PaneID); err != nil {
			return err
		}
	}
	return c.SendKeys(ctx, s.Tmux.PaneID, "Enter")
}

// sleepCtx waits for d, returning early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// handleSessionKey sends one or more named keys (Escape, C-c, ...) to a pane.
func (d *Node) handleSessionKey(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.KeyParams](params)
	if err != nil {
		return nil, err
	}
	s, c, err := d.resolve(p.SessionID)
	if err != nil {
		return nil, err
	}
	if len(p.Keys) == 0 {
		return nil, nil
	}
	return nil, c.SendKeys(ctx, s.Tmux.PaneID, p.Keys...)
}

// resolveSpawnCommand: an explicit p.Command wins; otherwise the agent's adapter
// owns construction (adapterFor falls back to the first adapter for unknown agents).
func (d *Node) resolveSpawnCommand(p api.SpawnParams, presetID string) (command string, args []string) {
	if p.Command != "" {
		if p.Prompt != "" {
			args = []string{p.Prompt}
		}
		return p.Command, args
	}
	return d.adapterFor(p.Agent).SpawnCommand(adapter.SpawnOptions{Prompt: p.Prompt, Model: p.Model, SessionID: presetID})
}

func (d *Node) handleAgentModelList(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.AgentModelListParams](params)
	if err != nil {
		return nil, err
	}
	a, ok := d.adapters[p.Agent]
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown agent " + p.Agent}
	}
	ml, ok := a.(adapter.ModelLister)
	if !ok {
		return api.AgentModelListResult{Models: []api.ModelInfo{}}, nil
	}
	models, err := ml.ListModels(ctx)
	if err != nil {
		return nil, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: "cannot list " + a.AgentName() + " models: " + err.Error(),
		}
	}
	if models == nil {
		models = []api.ModelInfo{}
	}
	return api.AgentModelListResult{Models: models}, nil
}

func (d *Node) handleSessionPromptComplete(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PromptCompleteParams](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown session " + p.SessionID}
	}
	var items []api.PromptCompletion
	if pc, ok := d.adapterFor(s.Agent).(adapter.PromptCompleter); ok {
		if items, err = pc.CompletePrompt(ctx, s, p.Trigger); err != nil {
			return nil, &api.RPCError{
				Code:    api.CodeInvalidRequest,
				Message: "cannot complete prompt: " + err.Error(),
			}
		}
	}
	if items == nil {
		items = []api.PromptCompletion{}
	}
	return api.PromptCompleteResult{Items: items}, nil
}

// handleAgentsList reports agents with spawnable flags. Probed live per call so an
// agent installed after startup is offered.
func (d *Node) handleAgentsList(_ context.Context, params json.RawMessage) (any, error) {
	if _, err := api.Decode[api.AgentsListParams](params); err != nil {
		return nil, err
	}
	agents := make([]api.AgentInfo, 0, len(d.adapterList))
	for _, a := range d.adapterList {
		name, _ := a.SpawnCommand(adapter.SpawnOptions{})
		// A Spawner (headless) agent creates sessions over its service API, so it
		// spawns without tmux; every other agent's spawn opens a tmux pane.
		_, isSpawner := a.(adapter.Spawner)
		spawnable := false
		if (d.caps.SpawnSession || isSpawner) && name != "" {
			if _, err := exec.LookPath(name); err == nil {
				spawnable = true
			}
		}
		agents = append(agents, api.AgentInfo{
			ID:        a.Agent(),
			Name:      a.AgentName(),
			Color:     a.AgentColor(),
			Spawnable: spawnable,
		})
	}
	return api.AgentsListResult{Agents: agents}, nil
}

func (d *Node) handleSessionSpawn(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SpawnParams](params)
	if err != nil {
		return nil, err
	}
	res, err := d.spawn(ctx, p, "")
	if err != nil {
		return nil, err
	}
	return res, nil
}

// spawn starts a session; presetID is the agent session id for an adapter.SessionIDPresetter.
func (d *Node) spawn(ctx context.Context, p api.SpawnParams, presetID string) (api.SpawnResult, error) {
	home, _ := os.UserHomeDir()
	p.Cwd = expandHome(p.Cwd, home)
	// Checked for every agent; tmux would silently fall back to $HOME.
	if err := d.cwdError("cannot spawn", p.Cwd); err != nil {
		return api.SpawnResult{}, err
	}
	a := d.adapterFor(p.Agent)
	// Never drop a model choice silently: reject what cannot apply it.
	if p.Model != "" {
		if p.Command != "" {
			return api.SpawnResult{}, &api.RPCError{Code: api.CodeInvalidRequest, Message: "cannot choose a model with an explicit command"}
		}
		if _, ok := a.(adapter.ModelLister); !ok {
			return api.SpawnResult{}, &api.RPCError{Code: api.CodeInvalidRequest, Message: a.AgentName() + " has no model choice"}
		}
	}
	// A Spawner agent (headless, e.g. opencode) has no pane process: create the
	// session over its service API and register it under the canonical agent-keyed
	// id. This path needs no tmux, so it runs ahead of the pane-spawn tmux guard.
	if sp, ok := a.(adapter.Spawner); ok {
		id, err := sp.SpawnSession(ctx, p.Cwd, adapter.SpawnOptions{Prompt: p.Prompt, Model: p.Model})
		if err != nil {
			return api.SpawnResult{}, &api.RPCError{
				Code:    api.CodeInvalidRequest,
				Message: "cannot spawn " + a.AgentName() + " session: " + err.Error(),
			}
		}
		return api.SpawnResult{SessionID: registry.AgentSessionKey(p.Agent, id)}, nil
	}
	if !d.caps.SpawnSession {
		return api.SpawnResult{}, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: "spawn unavailable: tmux not found on node " + d.label,
		}
	}
	command, args := d.resolveSpawnCommand(p, presetID)
	// Fail loudly rather than opening a tmux pane that dies "command not found".
	if _, err := exec.LookPath(command); err != nil {
		return api.SpawnResult{}, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: fmt.Sprintf("cannot spawn: %q is not installed on node %s", command, d.label),
		}
	}
	paneID, err := d.launchPane(ctx, p.Name, command, args, p.Cwd)
	if err != nil {
		return api.SpawnResult{}, err
	}
	// Discovery registers the session, but only once the agent process is visible in
	// ps — which can lag the pane's creation. Retry with backoff so the session shows
	// up without a manual refresh, stopping early once it's registered.
	sid := string(session.TmuxServerArgus) + ":" + paneID
	go d.rescanUntilRegistered(sid)
	return api.SpawnResult{SessionID: sid, PaneID: paneID}, nil
}

// dirMissing marks a history project whose cwd is gone; other stat errors (e.g.
// permissions) do not hide it.
func dirMissing(cwd string) bool {
	if cwd == "" {
		return false
	}
	fi, err := os.Stat(cwd)
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	return !fi.IsDir()
}

// cwdError rejects a cwd tmux would silently swap for $HOME: one that is
// relative, missing, not a directory, or not enterable. An empty cwd is left to
// the caller.
func (d *Node) cwdError(action, cwd string) error {
	if cwd == "" {
		return nil
	}
	var reason string
	switch fi, err := os.Stat(cwd); {
	case !filepath.IsAbs(cwd):
		reason = "is not an absolute path"
	case errors.Is(err, fs.ErrNotExist), err == nil && !fi.IsDir():
		reason = "does not exist"
	case err != nil, unix.Access(cwd, unix.X_OK) != nil:
		reason = "is not accessible"
	default:
		return nil
	}
	return &api.RPCError{
		Code:    api.CodeInvalidRequest,
		Message: fmt.Sprintf("%s: directory %s %s on node %s", action, cwd, reason, d.label),
	}
}

// rescanUntilRegistered rescans discovery with backoff until the given session id
// appears in the registry or the attempts are exhausted. Covers the window between a
// spawned pane existing and the agent process becoming visible to discovery's ps probe.
func (d *Node) rescanUntilRegistered(id string) {
	// Leading 0 scans immediately (Sleep(0) is a no-op) to catch the fast case, then
	// backs off while waiting for the process to appear in ps.
	backoffs := []time.Duration{0, 250 * time.Millisecond, 500 * time.Millisecond, 750 * time.Millisecond, 1500 * time.Millisecond, 2 * time.Second}
	for _, wait := range backoffs {
		time.Sleep(wait)
		d.scan(context.Background())
		if _, ok := d.reg.Get(id); ok {
			return
		}
	}
}

// spawnEnv returns extra environment assignments for a spawned command. An
// OpenCode viewer pane (opencode --session <id>) runs with tabs disabled so one
// pane maps to exactly one session, which is how argus tracks and adopts it.
func spawnEnv(command string) []string {
	if filepath.Base(command) == "opencode" {
		return []string{`OPENCODE_CLI_CONFIG_CONTENT={"tabs":{"enabled":false}}`}
	}
	return nil
}

// launchPane opens a new tmux pane running command in cwd. A blank sessionName
// gets a node-generated default; the terminals' session name gets a suffix.
// Discovery registers the pane shortly; a scan is triggered for immediacy.
func (d *Node) launchPane(ctx context.Context, sessionName, command string, args []string, cwd string) (string, error) {
	c := d.clients[session.TmuxServerArgus]
	if sessionName == "" {
		sessionName = spawn.SessionName(ctx, c, cwd)
	} else {
		sessionName = spawn.AvoidReserved(ctx, c, sessionName)
	}
	paneID, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: sessionName, Cwd: cwd, Command: command, Args: args, Env: spawnEnv(command)})
	if err != nil {
		return "", err
	}
	go d.scan(context.Background())
	return paneID, nil
}

// resumeGraceWindow must outlast the discovery + agent-hook latency that
// re-registers a resumed session under its own id, after which the registry check
// takes over from the in-flight guard.
var resumeGraceWindow = 30 * time.Second

// handleSessionResume restarts a past (not currently live) session by its agent
// session id in a tmux pane, and returns an id the caller can enter once the pane
// is live. A session that is already running is reused rather than duplicated.
// This is distinct from opening a terminal view of a live session (terminal.open).
func (d *Node) handleSessionResume(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.ResumeParams](params)
	if err != nil {
		return nil, err
	}
	if p.Agent == "" || p.AgentSessionID == "" {
		return nil, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: "resume requires agent and agent_session_id",
		}
	}
	// Resume must reopen in the session's original directory; an unknown cwd (e.g.
	// some antigravity sessions) would launch the agent somewhere arbitrary.
	if p.Cwd == "" {
		return nil, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: "cannot resume: session working directory is unknown",
		}
	}
	if err := d.cwdError("cannot resume", p.Cwd); err != nil {
		return nil, err
	}
	if !d.caps.SpawnSession {
		return nil, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: "resume unavailable: tmux not found on node " + d.label,
		}
	}

	// Reconcile discovery so a pane killed out-of-band is detached before we reuse
	// or relaunch; a still-live pane stays adopted and is reused below.
	d.scan(ctx)

	// A live controllable session with this agent session id is reused as-is; a
	// paneless record (an OpenCode presence card) is adopted onto by the launch.
	adoptID := ""
	for _, s := range d.reg.Snapshot() {
		if s.Agent != p.Agent || s.AgentSessionID != p.AgentSessionID {
			continue
		}
		if s.Controllable() {
			return api.ResumeResult{SessionID: s.ID}, nil
		}
		adoptID = s.ID
	}

	sid, err := d.spawnAndAdopt(ctx, p.Agent, p.AgentSessionID, p.Cwd, adoptID)
	if err != nil {
		return nil, err
	}
	return api.ResumeResult{SessionID: sid}, nil
}

// spawnAndAdopt launches the agent's resume command in a tmux pane and waits for
// discovery to adopt it, returning the id of the now-controllable session. A
// non-empty existingID adopts the pane onto a live paneless record (keeping its
// id); an empty existingID is a fresh launch keyed by the pane. An in-flight guard
// stops a concurrent duplicate launch of the same session.
func (d *Node) spawnAndAdopt(ctx context.Context, agent, agentSessionID, cwd, existingID string) (string, error) {
	if cwd == "" {
		return "", &api.RPCError{Code: api.CodeInvalidRequest, Message: "cannot open terminal: session working directory is unknown"}
	}
	if err := d.cwdError("cannot open terminal", cwd); err != nil {
		return "", err
	}
	if !d.caps.SpawnSession {
		return "", &api.RPCError{Code: api.CodeInvalidRequest, Message: "terminal unavailable: tmux not found on node " + d.label}
	}

	key := agent + "\x00" + agentSessionID
	d.resumeMu.Lock()
	unlocked := false
	unlock := func() {
		if !unlocked {
			d.resumeMu.Unlock()
			unlocked = true
		}
	}
	defer unlock()

	// Don't relaunch while a prior launch of this session is in flight — a duplicate
	// would race discovery. Jump to it once live, else tell the caller to retry (a
	// kill clears this guard immediately; see clearResuming).
	if id, ok := d.resuming[key]; ok {
		if s, ok := d.reg.Get(id); ok && s.Controllable() {
			return id, nil
		}
		return "", &api.RPCError{Code: api.CodeInvalidRequest, Message: "session is starting; try again in a moment"}
	}

	name, args, ok := d.adapterFor(agent).ResumeCommand(agentSessionID)
	if !ok {
		return "", &api.RPCError{Code: api.CodeInvalidRequest, Message: "terminal not supported for agent " + agent}
	}
	if _, err := exec.LookPath(name); err != nil {
		return "", &api.RPCError{Code: api.CodeInvalidRequest, Message: fmt.Sprintf("cannot open terminal: %q is not installed on node %s", name, d.label)}
	}
	paneID, err := d.launchPane(ctx, "", name, args, cwd)
	if err != nil {
		return "", err
	}
	// An adopted record keeps its id; a fresh launch is pane-keyed, matching the
	// record discovery will create.
	sid := existingID
	if sid == "" {
		sid = string(session.TmuxServerArgus) + ":" + paneID
	}
	d.resuming[key] = sid
	time.AfterFunc(resumeGraceWindow, func() {
		d.resumeMu.Lock()
		delete(d.resuming, key)
		d.resumeMu.Unlock()
	})
	// Release before the wait so an unrelated launch is not blocked behind it.
	unlock()

	if !d.waitControllable(sid) {
		return "", &api.RPCError{Code: api.CodeInvalidRequest, Message: "session is starting; try again in a moment"}
	}
	return sid, nil
}

// waitControllable rescans discovery with backoff until the session has adopted
// its spawned pane (Controllable), covering the lag between the pane existing and
// the agent process appearing in ps. Returns false if it never adopts in time.
func (d *Node) waitControllable(id string) bool {
	backoffs := []time.Duration{0, 250 * time.Millisecond, 500 * time.Millisecond, 750 * time.Millisecond, 1500 * time.Millisecond, 2 * time.Second}
	for _, wait := range backoffs {
		time.Sleep(wait)
		d.scan(context.Background())
		if s, ok := d.reg.Get(id); ok && s.Controllable() {
			return true
		}
	}
	return false
}

// handleSessionKill removes a session from the list: it kills the tmux pane when
// the session has one, otherwise it dismisses a paneless presence card (OpenCode).
func (d *Node) handleSessionKill(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SessionRef](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown session " + p.SessionID}
	}
	if err := d.killSession(ctx, s); err != nil {
		return nil, err
	}
	d.meshEnded(ctx, s, "owner")
	return nil, nil
}

func (d *Node) killSession(ctx context.Context, s session.Session) error {
	// A headless agent's pane is a disposable viewer, not the session's process:
	// killing it alone leaves the session in the list. Remove the session, tearing
	// down the adopted viewer pane too when one is present.
	if d.adapterFor(s.Agent).IsHeadless() {
		if s.Controllable() {
			if c, err := d.clientFor(s); err == nil {
				_ = c.KillPane(ctx, s.Tmux.PaneID)
			}
		}
		d.clearResuming(s.ID)
		_, err := d.dismissSession(ctx, s)
		return err
	}
	if !s.Controllable() {
		_, err := d.dismissSession(ctx, s)
		return err
	}
	c, err := d.clientFor(s)
	if err != nil {
		return err
	}
	if err := c.KillPane(ctx, s.Tmux.PaneID); err != nil {
		return err
	}
	d.clearResuming(s.ID)
	go d.scan(context.Background())
	return nil
}

// handleSessionRename renames a session through its agent; it needs no tmux
// pane. An applied name is recorded at once, ahead of the agent's own event.
func (d *Node) handleSessionRename(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SessionRenameParams](params)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(p.Name)
	if p.SessionID == "" || name == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session_id and name are required"}
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "name must not contain control characters"}
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown session " + p.SessionID}
	}
	r, ok := d.adapterFor(s.Agent).(adapter.Renamer)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "rename unsupported for " + s.Agent + " sessions"}
	}
	applied, err := r.RenameSession(ctx, s, name)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	if applied {
		d.reg.RenameSession(p.SessionID, name)
	}
	return api.SessionRenameResult{Name: name, Applied: applied}, nil
}

// dismissSession removes a session via the adapter's Dismisser. Kill is
// unconditional, matching pane kill for other agents.
func (d *Node) dismissSession(ctx context.Context, s session.Session) (any, error) {
	r, ok := d.adapterFor(s.Agent).(adapter.Dismisser)
	if !ok {
		return nil, fmt.Errorf("cannot remove %s session: no terminal pane and dismiss is unsupported", s.Agent)
	}
	if err := r.Dismiss(ctx, s); err != nil {
		return nil, err
	}
	return nil, nil
}

// clearResuming drops sid's in-flight guard so a session killed inside the grace
// window can be resumed again immediately.
func (d *Node) clearResuming(sid string) {
	d.resumeMu.Lock()
	defer d.resumeMu.Unlock()
	for k, v := range d.resuming {
		if v == sid {
			delete(d.resuming, k)
		}
	}
}
