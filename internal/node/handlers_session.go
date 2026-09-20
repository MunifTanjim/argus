package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/spawn"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
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
	return s
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
	res := api.IdentifyResult{ID: d.id, Label: d.label, Version: d.version, Capabilities: d.caps, IdentityPubKey: d.identityPubB64, SignerPubKey: d.signerPubB64}
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
		Version: d.version,
		Nodes:   []api.NodeInfo{{Label: d.label, Version: d.version, Capabilities: d.caps}},
	}, nil
}

// handleSessionsRefresh rescans on demand, then returns the current snapshot.
func (d *Node) handleSessionsRefresh(ctx context.Context, _ json.RawMessage) (any, error) {
	d.scan(ctx)
	return d.snapshotWithCaps(), nil
}

// handleTranscriptView returns the grouped, display-ready chunk view for a session.
func (d *Node) handleTranscriptView(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TranscriptParams](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	if s.TranscriptPath == "" {
		return transcript.TranscriptView{}, nil
	}
	return d.adapterFor(s.Agent).ReadTranscriptView(s.TranscriptPath)
}

// handleSessionToolDetail returns one tool item's full input/result by tool_use
// id; transcript chunks ship without these heavy bodies.
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
// agent's own prompt API for an InputAPI session (opencode), or tmux keystrokes
// for an InputPane session.
func (d *Node) handleSessionInput(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.InputParams](params)
	if err != nil {
		return nil, err
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", p.SessionID)
	}
	switch s.Input {
	case session.InputAPI:
		return d.sendPromptInput(ctx, s, p)
	case session.InputPane:
		return d.sendPaneInput(ctx, s, p)
	default:
		return nil, fmt.Errorf("%s: %w", s.ID, api.ErrNoTerminalControl)
	}
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

// sendPromptInput delivers a prompt to an InputAPI session over its agent's API,
// when the session is idle enough to accept one. It reports the absent terminal
// control otherwise, as the pane path would.
func (d *Node) sendPromptInput(ctx context.Context, s session.Session, p api.InputParams) (any, error) {
	pr, ok := d.adapterFor(s.Agent).(adapter.Prompter)
	if !ok || p.Text == "" || !promptEligible(s) {
		return nil, fmt.Errorf("%s: %w", s.ID, api.ErrNoTerminalControl)
	}
	if err := pr.SendPrompt(ctx, s, p.Text); err != nil {
		return nil, err
	}
	return nil, nil
}

// promptEligible reports whether a session is idle enough to receive a new prompt.
func promptEligible(s session.Session) bool {
	return s.Status != session.StatusWorking &&
		(s.Interaction == nil || s.Interaction.Kind == session.InteractionIdle)
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
func (d *Node) resolveSpawnCommand(p api.SpawnParams) (command string, args []string) {
	if p.Command != "" {
		if p.Prompt != "" {
			args = []string{p.Prompt}
		}
		return p.Command, args
	}
	return d.adapterFor(p.Agent).SpawnCommand(p.Prompt)
}

// handleAgentsList reports agents with spawnable flags. Probed live per call so an
// agent installed after startup is offered.
func (d *Node) handleAgentsList(_ context.Context, params json.RawMessage) (any, error) {
	if _, err := api.Decode[api.AgentsListParams](params); err != nil {
		return nil, err
	}
	agents := make([]api.AgentInfo, 0, len(d.adapterList))
	for _, a := range d.adapterList {
		name, _ := a.SpawnCommand("")
		spawnable := false
		if d.caps.SpawnSession && name != "" {
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
	if !d.caps.SpawnSession {
		return nil, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: "spawn unavailable: tmux not found on node " + d.label,
		}
	}
	command, args := d.resolveSpawnCommand(p)
	// Fail loudly rather than opening a tmux pane that dies "command not found".
	if _, err := exec.LookPath(command); err != nil {
		return nil, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: fmt.Sprintf("cannot spawn: %q is not installed on node %s", command, d.label),
		}
	}
	paneID, err := d.launchPane(ctx, p.Name, command, args, p.Cwd)
	if err != nil {
		return nil, err
	}
	// Discovery registers the session, but only once the agent process is visible in
	// ps — which can lag the pane's creation. Retry with backoff so the session shows
	// up without a manual refresh, stopping early once it's registered.
	sid := string(session.TmuxServerArgus) + ":" + paneID
	go d.rescanUntilRegistered(sid)
	return api.SpawnResult{SessionID: sid, PaneID: paneID}, nil
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

// spawnEnv returns extra environment assignments for a spawned command. OpenCode
// runs with tabs disabled so one pane maps to exactly one session, which is how
// argus tracks and adopts it.
func spawnEnv(command string) []string {
	if filepath.Base(command) == "opencode" {
		return []string{`OPENCODE_CLI_CONFIG_CONTENT={"tabs":{"enabled":false}}`}
	}
	return nil
}

// launchPane opens a new tmux pane running command in cwd. A blank sessionName
// gets a node-generated default. Discovery registers the pane shortly; a scan is
// triggered for immediacy.
func (d *Node) launchPane(ctx context.Context, sessionName, command string, args []string, cwd string) (string, error) {
	c := d.clients[session.TmuxServerArgus]
	if sessionName == "" {
		sessionName = spawn.SessionName(ctx, c, cwd)
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
		return d.dismissSession(ctx, s)
	}
	if !s.Controllable() {
		return d.dismissSession(ctx, s)
	}
	c, err := d.clientFor(s)
	if err != nil {
		return nil, err
	}
	if err := c.KillPane(ctx, s.Tmux.PaneID); err != nil {
		return nil, err
	}
	d.clearResuming(s.ID)
	go d.scan(context.Background())
	return nil, nil
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
