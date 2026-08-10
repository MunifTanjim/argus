package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

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
	return d.reg.Snapshot(), nil
}

// handleNodeIdentify announces this node's identity over the gateway uplink.
// A fresh beacon is produced on each identify call so the gateway always
// receives a beacon with the current tip and a strictly increasing counter.
func (d *Node) handleNodeIdentify(context.Context, json.RawMessage) (any, error) {
	res := api.IdentifyResult{
		ID:             d.id,
		Label:          d.label,
		Version:        d.version,
		Capabilities:   d.caps,
		IdentityPubKey: d.identityPubB64,
		SignerPubKey:   d.signerPubB64,
		BeaconPubKey:   d.beaconPubB64,
	}
	if len(d.beacon.Private) > 0 {
		if b, err := d.makeBeacon(); err == nil {
			res.Beacon = &b
		}
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
	return d.reg.Snapshot(), nil
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

// handleSessionInput sends text (and optionally Enter) to a session's pane, after
// optionally normalizing the pane for input (exit copy mode; ensure vim insert).
func (d *Node) handleSessionInput(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.InputParams](params)
	if err != nil {
		return nil, err
	}
	s, c, err := d.resolve(p.SessionID)
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

// launchPane opens a new tmux pane running command in cwd. A blank sessionName
// gets a node-generated default. Discovery registers the pane shortly; a scan is
// triggered for immediacy.
func (d *Node) launchPane(ctx context.Context, sessionName, command string, args []string, cwd string) (string, error) {
	c := d.clients[session.TmuxServerArgus]
	if sessionName == "" {
		sessionName = spawn.SessionName(ctx, c, cwd)
	}
	paneID, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: sessionName, Cwd: cwd, Command: command, Args: args})
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

// handleSessionResume resumes a past session by its agent session id. If that
// session is already running live (or a concurrent resume is launching it), it
// returns that session rather than spawning a duplicate; otherwise it launches the
// agent's resume command in the session's original cwd.
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

	key := p.Agent + "\x00" + p.AgentSessionID
	d.resumeMu.Lock()
	defer d.resumeMu.Unlock()

	snap := d.reg.Snapshot()
	// Jump to an already-live, controllable session with this agent session id.
	for _, s := range snap {
		if s.Agent == p.Agent && s.AgentSessionID == p.AgentSessionID && s.Controllable() {
			return api.ResumeResult{SessionID: s.ID}, nil
		}
	}
	// Don't relaunch while a prior resume of this session is in flight — a duplicate
	// would race discovery. Jump to that pane if it's already live, else tell the
	// caller to retry (a kill clears this guard immediately; see clearResuming).
	if id, ok := d.resuming[key]; ok {
		for _, s := range snap {
			if s.ID == id && s.Controllable() {
				return api.ResumeResult{SessionID: id}, nil
			}
		}
		return nil, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: "session was resumed moments ago; wait a few seconds and try again",
		}
	}

	name, args, ok := d.adapterFor(p.Agent).ResumeCommand(p.AgentSessionID)
	if !ok {
		return nil, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: "resume not supported for agent " + p.Agent,
		}
	}
	if _, err := exec.LookPath(name); err != nil {
		return nil, &api.RPCError{
			Code:    api.CodeInvalidRequest,
			Message: fmt.Sprintf("cannot resume: %q is not installed on node %s", name, d.label),
		}
	}
	paneID, err := d.launchPane(ctx, "", name, args, p.Cwd)
	if err != nil {
		return nil, err
	}
	sid := string(session.TmuxServerArgus) + ":" + paneID
	d.resuming[key] = sid
	time.AfterFunc(resumeGraceWindow, func() {
		d.resumeMu.Lock()
		delete(d.resuming, key)
		d.resumeMu.Unlock()
	})
	return api.ResumeResult{SessionID: sid}, nil
}

// handleSessionKill kills a session's pane.
func (d *Node) handleSessionKill(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.SessionRef](params)
	if err != nil {
		return nil, err
	}
	s, c, err := d.resolve(p.SessionID)
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
