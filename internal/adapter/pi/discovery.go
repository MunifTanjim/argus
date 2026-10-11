package pi

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
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

// quickFail bounds how long a request waits for pi to reject a command; one
// still running past it finishes in the background.
var quickFail = time.Second

func awaitQuickFail(ctx context.Context, done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(quickFail):
		return nil
	case <-ctx.Done():
		return nil // the command keeps running
	}
}

// pendingUI is one extension dialog awaiting a user decision: the Argus
// interaction surfaced for it, answered by extension_ui_response.
type pendingUI struct {
	uiID    string // extension_ui_request id
	method  string // select | confirm | input | editor | approval (the gate's)
	title   string
	message string
	options []string
}

// card is the state a pi session's card shows, owned or foreign.
type card struct {
	mu        sync.Mutex
	id        string // pi session id (AgentSessionID)
	cwd       string
	repo      string
	file      string // may be empty: pi creates it after the first user/assistant message
	name      string
	streaming bool
	pending   map[string]*pendingUI // ui request id -> dialog
}

// hookUpdate builds the card's push; c.mu must be held. Empty status leaves
// status/interaction alone; non-nil interaction replaces the current one.
func (c *card) hookUpdate(st session.Status, ix *session.Interaction, replace bool) registry.HookUpdate {
	return registry.HookUpdate{
		Agent:              Agent,
		AgentSessionID:     c.id,
		Name:               c.name,
		Cwd:                c.cwd,
		Repo:               c.repo,
		TranscriptPath:     c.file,
		Frontend:           session.FrontendExternal,
		Input:              session.InputAPI,
		Status:             st,
		Interaction:        ix,
		ReplaceInteraction: replace,
	}
}

// pickPending picks the dialog to answer: by RequestID when the client echoes
// it, else the single pending dialog. Ambiguous state is an error rather than
// a guess at which question the user saw.
func (c *card) pickPending(requestID string) (*pendingUI, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var pend *pendingUI
	if requestID != "" {
		pend = c.pending[requestID]
	} else if len(c.pending) == 1 {
		for _, p := range c.pending {
			pend = p
		}
	}
	if pend == nil {
		return nil, fmt.Errorf("pi: no pending dialog for session %s", c.id)
	}
	return pend, nil
}

// dropPending removes a dialog that is no longer waiting. It reports whether
// that took down the last one.
func (c *card) dropPending(uiID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, had := c.pending[uiID]
	delete(c.pending, uiID)
	return had && len(c.pending) == 0
}

// sessionProc is one Argus-owned `pi --mode rpc` child process.
type sessionProc struct {
	card
	conn   *rpcConn
	cmd    *exec.Cmd
	exited chan struct{} // closed once the reaper's cmd.Wait returns
}

// discoverer supervises owned pi RPC processes and mirrors them into the
// registry. Liveness is the child process: a session exists while its pi
// process runs. There is no shared daemon to poll (unlike Codex/OpenCode):
// a reaper per process drops exited ones, and all other updates are
// event-driven.
type discoverer struct {
	reg *registry.Registry

	mu    sync.Mutex
	procs map[string]*sessionProc // pi session id -> owned proc

	// Beacon side (Tier 2): foreign sessions whose gate extension dialed in.
	foreign map[string]*foreignSession
	paneFor func(ctx context.Context, pid int) (session.TmuxServer, string)

	seq atomic.Uint64
}

func newDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) *discoverer {
	return &discoverer{
		reg:     reg,
		procs:   map[string]*sessionProc{},
		foreign: map[string]*foreignSession{},
		paneFor: paneLookup(clients),
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
	d.sweepForeign()
	return nil
}

// running reports whether a pi process runs session id, owned or foreign.
func (d *discoverer) running(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.procs[id] != nil || d.foreign[id] != nil
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
	gate, err := gateExtension()
	if err != nil {
		return "", err
	}
	id := newSessionID()
	// The process outlives the request: a request's context ends with the
	// client connection that sent it.
	conn, cmd, err := spawnProc(context.WithoutCancel(ctx), cwd, id, gate)
	if err != nil {
		return "", err
	}
	p := &sessionProc{
		card:   card{id: id, cwd: cwd, repo: gittree.RepoName(cwd), pending: map[string]*pendingUI{}},
		conn:   conn,
		cmd:    cmd,
		exited: make(chan struct{}),
	}
	conn.start(func(typ string, raw json.RawMessage) { d.handleEvent(id, typ, raw) })
	d.mu.Lock()
	d.procs[id] = p
	d.mu.Unlock()

	// Reap: the only cmd.Wait. An unexpected exit marks the card dead.
	go func() {
		_ = cmd.Wait()
		close(p.exited)
		d.mu.Lock()
		still := d.procs[id] == p
		d.mu.Unlock()
		if still {
			d.forget(id)
		}
	}()

	// Hydrate identity: session file and canonical session id.
	if data, err := conn.call(ctx, d.nextID(), "get_state", nil); err == nil {
		var st struct {
			SessionID   string `json:"sessionId"`
			SessionFile string `json:"sessionFile"`
			SessionName string `json:"sessionName"`
		}
		if json.Unmarshal(data, &st) == nil {
			if st.SessionFile != "" {
				p.file = st.SessionFile
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
	if p := d.get(id); p != nil {
		return d.sendPromptOwned(ctx, p, text)
	}
	if fs := d.lookupForeign(id); fs != nil {
		fs.mu.Lock()
		busy := fs.streaming
		fs.mu.Unlock()
		return d.beaconSend(fs, map[string]any{
			"type": "intent_prompt", "text": text, "busy": busy,
		})
	}
	return fmt.Errorf("pi: unknown session %s", id)
}

func (d *discoverer) sendPromptOwned(ctx context.Context, p *sessionProc, text string) error {
	p.mu.Lock()
	busy := p.streaming
	p.mu.Unlock()
	params := map[string]any{"message": text}
	if busy {
		// A prompt without streamingBehavior errors while the agent runs;
		// steer delivers after the current turn's tool calls.
		params["streamingBehavior"] = "steer"
	}
	// pi answers a slash command only once it ends, dialogs included, so the
	// answer is followed in the background.
	done := make(chan error, 1)
	go func() {
		data, err := p.conn.call(context.Background(), d.nextID(), "prompt", params)
		if err == nil {
			d.afterPrompt(p, data)
		}
		done <- err
	}()
	return awaitQuickFail(ctx, done)
}

func (d *discoverer) afterPrompt(p *sessionProc, data json.RawMessage) {
	var r struct {
		Disposition string `json:"disposition"`
	}
	_ = json.Unmarshal(data, &r)
	if r.Disposition != "handled" {
		d.upsert(p, session.StatusWorking, nil, false)
		return
	}
	// A command or input handler took it: no run follows to settle the card.
	p.mu.Lock()
	settled := !p.streaming && len(p.pending) == 0
	p.mu.Unlock()
	if settled {
		d.upsert(p, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle}, true)
	}
}

func (d *discoverer) dismiss(ctx context.Context, id string) error {
	d.mu.Lock()
	p := d.procs[id]
	delete(d.procs, id)
	d.mu.Unlock()
	if p == nil {
		// Foreign sessions untrack only: the user's own process keeps running.
		// Unknown ids still emit Dead so a lost card is always cleared.
		if !d.forgetForeign(id) {
			d.markDead(id)
		}
		return nil
	}
	_ = p.conn.close() // orderly shutdown: pi disposes the runtime and exits
	ctx, cancel := context.WithTimeout(ctx, dismissKillAfter)
	defer cancel()
	select {
	case <-p.exited:
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
	d.markDead(id)
	return nil
}

// forget drops a session whose process exited on its own.
func (d *discoverer) forget(id string) {
	d.mu.Lock()
	delete(d.procs, id)
	d.mu.Unlock()
	d.markDead(id)
}

func (d *discoverer) markDead(id string) {
	d.reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: id, Status: session.StatusDead})
}

func (d *discoverer) upsert(p *sessionProc, st session.Status, ix *session.Interaction, replace bool) {
	p.mu.Lock()
	u := p.hookUpdate(st, ix, replace)
	p.mu.Unlock()
	d.reg.ApplyHook(u)
}

// handleEvent translates one RPC stdout record for the given session.
func (d *discoverer) handleEvent(id, typ string, raw json.RawMessage) {
	p := d.get(id)
	if p == nil {
		return
	}
	p.mu.Lock()
	hasPending := len(p.pending) > 0
	p.mu.Unlock()
	switch typ {
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
	}
}

// uiRequest is the wire shape of an extension dialog in RPC mode.
type uiRequest struct {
	ID          string   `json:"id"`
	Method      string   `json:"method"`
	Title       string   `json:"title"`
	Message     string   `json:"message"`
	Options     []string `json:"options"`
	Placeholder string   `json:"placeholder"`
	Timeout     int      `json:"timeout"` // ms; pi answers the dialog itself once it passes
}

// approvalMarker prefixes the placeholder of the gate's approval dialog in
// managed mode (APPROVAL_MARKER in gate/argus.ts); the tool detail follows it.
const approvalMarker = "argus:approval:"

// uiInteraction interprets one extension dialog, shared by owned RPC sessions
// (extension_ui_request) and foreign beacons (approval_request mapped onto the
// same shape). It returns the parked dialog and its Argus interaction.
func uiInteraction(req uiRequest) (*pendingUI, *session.Interaction, bool) {
	pend := &pendingUI{uiID: req.ID, method: req.Method, title: req.Title, message: req.Message, options: req.Options}
	if detail, ok := strings.CutPrefix(req.Placeholder, approvalMarker); ok && req.Method == "input" {
		pend.method, req.Method, req.Message = "approval", "confirm", detail
	}
	var ix *session.Interaction
	switch req.Method {
	case "confirm":
		ix = &session.Interaction{
			Kind:    session.InteractionPermission,
			Message: req.Title,
			Options: []session.DecisionOption{
				{Label: "Allow", Value: "allow"},
				{Label: "Deny", Value: "deny", Reject: true, Placeholder: "Tell Pi what to change"},
			},
			ToolName: req.Message,
		}
	case "select", "input", "editor":
		ix = &session.Interaction{
			Kind:    session.InteractionQuestion,
			Message: req.Message,
			Questions: []session.QuestionSpec{{
				Header:   req.Title,
				Question: cmp.Or(req.Title, req.Message),
				Options:  req.Options,
			}},
			// An empty answer is a valid value, unlike a cancel.
			AllowUnanswered: req.Method != "select",
		}
	default:
		return nil, nil, false // notify, setStatus, setWidget, setTitle, set_editor_text
	}
	ix.RequestID = req.ID
	return pend, ix, true
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
	pend, ix, ok := uiInteraction(req)
	if !ok {
		return
	}
	p.mu.Lock()
	p.pending[pend.uiID] = pend
	p.mu.Unlock()
	d.upsert(p, session.StatusAwaitingInput, ix, true)
	if req.Timeout > 0 {
		time.AfterFunc(time.Duration(req.Timeout)*time.Millisecond, func() { d.dropOwnedPending(p, pend.uiID) })
	}
}

// dropOwnedPending removes a dialog that is no longer waiting and, once none
// is left, takes the prompt down from the card.
func (d *discoverer) dropOwnedPending(p *sessionProc, uiID string) {
	if p.dropPending(uiID) {
		d.upsert(p, session.StatusWorking, nil, false)
	}
}
