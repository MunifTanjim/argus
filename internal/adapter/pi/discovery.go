package pi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/gittree"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// spawnProc starts the owned pi child; a var so tests can stub the process
// while exercising the supervisor against a fake RPC peer.
var spawnProc = spawnRPC

// dismissKillAfter bounds graceful shutdown before SIGKILL.
var dismissKillAfter = 5 * time.Second

// pendingUI is one extension dialog awaiting a user decision: the Argus
// interaction surfaced for it, answered by extension_ui_response.
type pendingUI struct {
	uiID    string // extension_ui_request id
	method  string // select | confirm | input | editor
	title   string
	message string
	options []string
}

// sessionProc is one Argus-owned `pi --mode rpc` child process.
type sessionProc struct {
	id          string // pi session id (AgentSessionID)
	cwd         string
	sessionFile string
	conn        *rpcConn
	cmd         *exec.Cmd

	mu           sync.Mutex
	status       session.Status
	streaming    bool
	name         string
	lastActivity time.Time
	pending      map[string]*pendingUI // ui request id -> dialog
}

// discoverer supervises owned pi RPC processes and mirrors them into the
// registry. Liveness is the child process: a session exists while its pi
// process runs. There is no shared daemon to poll (unlike Codex/OpenCode),
// so ScanOnce only reaps exited processes; all other updates are event-driven.
type discoverer struct {
	reg *registry.Registry
	ctx context.Context

	mu    sync.Mutex
	procs map[string]*sessionProc // pi session id -> proc

	seq      atomic.Uint64
	gateOnce sync.Once
	gatePath string
	gateErr  error
}

func newDiscoverer(reg *registry.Registry, _ map[session.TmuxServer]*tmux.Client) *discoverer {
	return &discoverer{
		reg:   reg,
		ctx:   context.Background(),
		procs: map[string]*sessionProc{},
	}
}

func (d *discoverer) nextID() string {
	return fmt.Sprintf("argus-%d", d.seq.Add(1))
}

func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("pi-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (d *discoverer) ScanOnce(_ context.Context) error {
	d.mu.Lock()
	var dead []string
	for id, p := range d.procs {
		p.mu.Lock()
		exited := p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited()
		p.mu.Unlock()
		if exited {
			dead = append(dead, id)
		}
	}
	d.mu.Unlock()
	for _, id := range dead {
		d.forget(id)
	}
	return nil
}

func (d *discoverer) get(id string) *sessionProc {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.procs[id]
}

// spawnSession starts an owned pi RPC process in cwd and returns the pi
// session id. A non-empty prompt is sent after the process is tracked, so a
// send failure still leaves a live, listed session.
func (d *discoverer) spawnSession(ctx context.Context, cwd, prompt string) (string, error) {
	gate, err := d.gateExtension()
	if err != nil {
		return "", err
	}
	id := newSessionID()
	conn, cmd, err := spawnProc(ctx, cwd, id, gate)
	if err != nil {
		return "", err
	}
	p := &sessionProc{
		id:           id,
		cwd:          cwd,
		lastActivity: time.Now(),
		status:       session.StatusWorking,
		pending:      map[string]*pendingUI{},
	}
	p.conn = conn
	p.cmd = cmd
	conn.onEvent = func(raw json.RawMessage) { d.handleEvent(id, raw) }
	d.mu.Lock()
	d.procs[id] = p
	d.mu.Unlock()

	// Reap: an unexpected exit marks the card dead.
	go func() {
		_ = cmd.Wait()
		d.mu.Lock()
		still := d.procs[id] == p
		d.mu.Unlock()
		if still {
			d.forget(id)
		}
	}()

	// Hydrate identity: sessionFile (may not exist on disk yet — pi creates it
	// after the first user/assistant message) and canonical session id.
	if data, err := conn.call(ctx, d.nextID(), "get_state", nil); err == nil {
		var st struct {
			SessionID   string `json:"sessionId"`
			SessionFile string `json:"sessionFile"`
			SessionName string `json:"sessionName"`
		}
		if json.Unmarshal(data, &st) == nil {
			if st.SessionFile != "" {
				p.sessionFile = st.SessionFile
			}
			if st.SessionName != "" {
				p.name = st.SessionName
			}
		}
	}
	d.upsert(p, session.StatusWorking, nil, false)
	if prompt != "" {
		if err := d.sendPrompt(ctx, id, prompt); err != nil {
			return id, err
		}
	}
	return id, nil
}

func (d *discoverer) sendPrompt(ctx context.Context, id, text string) error {
	p := d.get(id)
	if p == nil {
		return fmt.Errorf("pi: unknown session %s", id)
	}
	p.mu.Lock()
	busy := p.streaming
	p.mu.Unlock()
	params := map[string]any{"message": text}
	if busy {
		// A prompt without streamingBehavior errors while the agent runs;
		// steer delivers after the current turn's tool calls.
		params["streamingBehavior"] = "steer"
	}
	_, err := p.conn.call(ctx, d.nextID(), "prompt", params)
	if err != nil {
		return err
	}
	d.upsert(p, session.StatusWorking, nil, false)
	return nil
}

func (d *discoverer) dismiss(ctx context.Context, id string) error {
	d.mu.Lock()
	p := d.procs[id]
	delete(d.procs, id)
	d.mu.Unlock()
	if p == nil {
		// The user's dismiss always removes the card, even one lost locally.
		d.reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: id, Status: session.StatusDead})
		return nil
	}
	_ = p.conn.close() // orderly shutdown: pi disposes the runtime and exits
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(dismissKillAfter):
		_ = p.cmd.Process.Kill()
		<-done
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
		<-done
	}
	d.reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: id, Status: session.StatusDead})
	return nil
}

// forget drops a session whose process exited on its own.
func (d *discoverer) forget(id string) {
	d.mu.Lock()
	delete(d.procs, id)
	d.mu.Unlock()
	d.reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: id, Status: session.StatusDead})
}

// upsert pushes a proc's card. Empty status leaves status/interaction alone;
// non-nil interaction replaces the current one.
func (d *discoverer) upsert(p *sessionProc, st session.Status, ix *session.Interaction, replace bool) {
	p.mu.Lock()
	p.lastActivity = time.Now()
	if st != "" {
		p.status = st
	}
	u := registry.HookUpdate{
		Agent:              Agent,
		AgentSessionID:     p.id,
		Name:               p.name,
		Cwd:                p.cwd,
		Repo:               gittree.RepoName(p.cwd),
		TranscriptPath:     p.sessionFile,
		Frontend:           session.FrontendExternal,
		Input:              session.InputAPI,
		Status:             st,
		Interaction:        ix,
		ReplaceInteraction: replace,
	}
	p.mu.Unlock()
	d.reg.ApplyHook(u)
}

// handleEvent translates one RPC stdout record for the given session.
func (d *discoverer) handleEvent(id string, raw json.RawMessage) {
	p := d.get(id)
	if p == nil {
		return
	}
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return
	}
	p.mu.Lock()
	hasPending := len(p.pending) > 0
	p.mu.Unlock()
	switch probe.Type {
	case "agent_start", "turn_start", "message_start", "tool_execution_start":
		p.mu.Lock()
		p.streaming = true
		p.mu.Unlock()
		// A trailing activity event after a dialog must not clear the pending
		// prompt (same hazard as opencode's post-permission activity).
		if hasPending {
			return
		}
		d.upsert(p, session.StatusWorking, nil, false)
	case "agent_end":
		// One low-level run ended; retries/compaction/queued work may follow.
		// agent_settled is the authoritative idle signal.
		if hasPending {
			return
		}
		d.upsert(p, session.StatusWorking, nil, false)
	case "agent_settled":
		p.mu.Lock()
		p.streaming = false
		p.mu.Unlock()
		if hasPending {
			return
		}
		d.upsert(p, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle}, true)
	case "session_info_changed":
		var ev struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &ev) == nil && ev.Name != "" {
			p.mu.Lock()
			p.name = ev.Name
			p.mu.Unlock()
			d.upsert(p, "", nil, false)
		}
	case "extension_ui_request":
		d.applyUIRequest(p, raw)
	case "queue_update", "message_update", "message_end", "turn_end",
		"tool_execution_update", "tool_execution_end", "entry_appended",
		"thinking_level_changed", "compaction_start", "compaction_end",
		"auto_retry_start", "auto_retry_end", "bash_execution_update",
		"extension_error", "summarization_retry_scheduled",
		"summarization_retry_attempt_start", "summarization_retry_finished":
		// Consumed by transcript streaming / prompt backfill; no card change.
	}
}

// uiRequest is the wire shape of an extension dialog in RPC mode.
type uiRequest struct {
	Type        string   `json:"type"`
	ID          string   `json:"id"`
	Method      string   `json:"method"`
	Title       string   `json:"title"`
	Message     string   `json:"message"`
	Options     []string `json:"options"`
	Placeholder string   `json:"placeholder"`
	Timeout     int      `json:"timeout"`
}

// applyUIRequest surfaces an extension dialog as an Argus interaction and
// parks it for Respond. Dialog methods (select/confirm/input/editor) block the
// extension until extension_ui_response; fire-and-forget methods (notify,
// setStatus, ...) carry no decision and are ignored here.
func (d *discoverer) applyUIRequest(p *sessionProc, raw json.RawMessage) {
	var req uiRequest
	if json.Unmarshal(raw, &req) != nil || req.ID == "" {
		return
	}
	pend := &pendingUI{uiID: req.ID, method: req.Method, title: req.Title, message: req.Message, options: req.Options}
	var ix *session.Interaction
	switch req.Method {
	case "select", "confirm":
		ix = &session.Interaction{
			Kind:    session.InteractionPermission,
			Message: req.Title,
			Options: []session.DecisionOption{
				{Label: "Allow", Value: "allow"},
				{Label: "Deny", Value: "deny", Reject: true, Placeholder: "Tell Pi what to change"},
			},
		}
		if req.Message != "" {
			ix.ToolName = req.Message
		}
	case "input", "editor":
		q := req.Title
		if q == "" {
			q = req.Message
		}
		ix = &session.Interaction{
			Kind:    session.InteractionQuestion,
			Message: req.Message,
			Questions: []session.QuestionSpec{{
				Header:   req.Title,
				Question: q,
			}},
		}
	default:
		return // notify, setStatus, setWidget, setTitle, set_editor_text
	}
	ix.RequestID = req.ID
	p.mu.Lock()
	p.pending[req.ID] = pend
	p.mu.Unlock()
	d.upsert(p, session.StatusAwaitingInput, ix, true)
}
